package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/rookery-ai/rookery/internal/chat"
	codersvc "github.com/rookery-ai/rookery/internal/coder"
	"github.com/rookery-ai/rookery/internal/db"
)

// chatTurnState tracks one in-flight chat turn.
//
// The turn executes in a goroutine on a detached context so navigating away
// never kills it; this state is what lets the SSE endpoint stream its progress
// and what lets a client that comes BACK mid-turn re-attach instead of
// rendering an empty conversation.
//
// Deliberately in-memory, mirroring agentRunState rather than adding a table: a
// turn is minutes long, not days. A turn killed by a server restart leaves a
// persisted user message with no reply — visible and self-explanatory — rather
// than a spinner that never resolves.
type chatTurnState struct {
	id         string
	progressCh chan string

	mu   sync.Mutex
	done bool
	err  error
	// lines accumulates every milestone so a client attaching MID-turn receives
	// the history it missed rather than only what arrives after it connects.
	// Following the live channel alone would show such a client an empty card on
	// a busy turn, which is the same "nothing is happening" impression this
	// whole change exists to remove.
	lines []string
}

// snapshot returns the turn's observable state without exposing the lock.
func (st *chatTurnState) snapshot() (lines []string, done bool, err error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return append([]string(nil), st.lines...), st.done, st.err
}

// startChatTurn persists the user's message, registers the turn, and runs the
// coder on a detached context. Returns the turn id, or false if a turn is
// already in flight for this chat — a double-send must not point two coders at
// one conversation, the same rule startManualRun applies to agent runs.
func (s *Server) startChatTurn(workspaceID, chatID, text string) (string, bool) {
	s.chatTurnsMu.Lock()
	if existing, ok := s.chatTurns[chatID]; ok {
		existing.mu.Lock()
		running := !existing.done
		existing.mu.Unlock()
		if running {
			s.chatTurnsMu.Unlock()
			return "", false
		}
	}
	st := &chatTurnState{id: uuid.NewString(), progressCh: make(chan string, 64)}
	s.chatTurns[chatID] = st
	s.chatTurnsMu.Unlock()

	// Read history BEFORE persisting the new message. History comes from
	// ListChatMessages, so writing first would feed this turn's own text twice —
	// once as a prior turn, once as the message being answered.
	//
	// Cleaned, not raw: a previously-leaked reply sitting in the history is
	// few-shot evidence that protocol markers are how one answers here, and the
	// model copies it. Assistant turns only — see chat.CleanHistory.
	rawHistory, _ := s.db.ListChatMessages(chatID)
	history := chat.CleanHistory(rawHistory)
	if s.testHistoryHook != nil {
		s.testHistoryHook(len(history))
	}

	// Durable before the coder runs. This single line is the fix: leaving the
	// page can no longer destroy the only copy of what the owner typed.
	if err := s.db.AddChatMessage(chatID, "user", text); err != nil {
		slog.Error("chat: persist user message", "chat", chatID, "error", err)
	}

	go func() {
		// Detached: the turn must outlive the HTTP request that started it. The
		// coder profile's own timeout still bounds it, so this does not run
		// unbounded.
		ctx := context.Background()

		onProgress := func(msg string) {
			st.mu.Lock()
			st.lines = append(st.lines, msg)
			st.mu.Unlock()
			select {
			case st.progressCh <- msg:
			default:
				// Buffer full — drop for the LIVE view only. lines still has it,
				// so an attaching client's replay is complete either way.
			}
		}

		reply, err := s.runChatCoder(ctx, workspaceID, chatID, history, text, onProgress)

		st.mu.Lock()
		st.done = true
		st.err = err
		st.mu.Unlock()

		switch {
		case err != nil:
			// The user's message STAYS. Not persisting on failure was defensible
			// while the browser held the bubble in memory; now that the message is
			// durable, removing it would be actively worse — the owner typed it,
			// and it is the context for their retry.
			slog.Warn("chat: turn failed", "chat", chatID, "turn", st.id, "error", err)
			// The OWNER gets a classified sentence, not the raw error. This
			// message is not merely displayed: it lands in the chat transcript,
			// which is reflected into the vault and can be relayed to a
			// connected chat platform — so internal wording travels further
			// here than it does in a log line. The classification also matches
			// what every other surface says about the same provider failures
			// (see agentrunner.FriendlyRunError).
			onProgress("⚠️ " + chatTurnFailureMessage(err))
		default:
			// CleanReply never returns "" (a genuinely empty model reply gets its
			// own placeholder), so a blank bubble cannot be persisted here.
			cleaned := chat.CleanReply(reply)
			if err := s.db.AddChatMessage(chatID, "assistant", cleaned); err != nil {
				slog.Error("chat: persist assistant message", "chat", chatID, "error", err)
			}
			_ = s.db.TouchChat(chatID)
			if ch, gerr := s.db.GetChat(chatID); gerr == nil {
				chat.MaybeAutoTitle(s.db, s.titleGen, ch, text, cleaned)
			}
		}
		close(st.progressCh)

		// Evict after a grace period so a late or reconnecting viewer can still
		// observe the terminal state; the durable record is the chat history.
		time.AfterFunc(90*time.Second, func() {
			s.chatTurnsMu.Lock()
			if cur, ok := s.chatTurns[chatID]; ok && cur == st {
				delete(s.chatTurns, chatID)
			}
			s.chatTurnsMu.Unlock()
		})
	}()
	return st.id, true
}

// chatTurnFailureMessage turns a failed turn into one actionable sentence.
//
// The known provider failures get the same account they get from a scheduled
// agent run, because they are the same failures and a user who has seen one
// should recognise the other. Anything unclassified stays deliberately vague
// and points at the log: an unrecognised error is exactly the case where we do
// not know what it contains, and this string is written into the transcript.
func chatTurnFailureMessage(err error) string {
	switch {
	case errors.Is(err, codersvc.ErrRateLimited):
		return "The coder was rate-limited by the provider. Try again in a moment — your quota is fine."
	case errors.Is(err, codersvc.ErrUsageLimit):
		return "The coder has hit its usage limit (quota or credits exhausted)."
	case errors.Is(err, codersvc.ErrAPIAuth):
		return "The coder could not authenticate with the provider. Check the API key in coder settings."
	case errors.Is(err, codersvc.ErrTimeout):
		return "The coder took too long and the turn was stopped. Try a smaller question, or raise the coder timeout."
	default:
		return "The chat turn failed. See the server log for details."
	}
}

// chatTurn returns the tracked turn for a chat, or nil.
func (s *Server) chatTurn(chatID string) *chatTurnState {
	s.chatTurnsMu.Lock()
	defer s.chatTurnsMu.Unlock()
	return s.chatTurns[chatID]
}

// isChatTurnLive reports whether THIS server has a turn in flight for the chat
// — i.e. whether an SSE stream would actually have a producer. A client uses it
// to decide whether to attach, so reporting true for a finished turn would open
// a stream that immediately closes.
func (s *Server) isChatTurnLive(chatID string) bool {
	st := s.chatTurn(chatID)
	if st == nil {
		return false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return !st.done
}

// handleChatTurnProgress streams an in-flight chat turn's milestones via SSE.
//
// Closing this stream (navigating away) does NOT cancel the turn — the turn
// runs on a detached context and this handler only observes it. That asymmetry
// is the entire point of the change.
//
// GET /api/v1/chats/:id/turn/progress
func (s *Server) handleChatTurnProgress(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	id := c.Param("id")
	ch, err := s.db.GetChat(id)
	if err != nil || ch.WorkspaceID != u.ID {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "chat not found"})
	}
	st := s.chatTurn(id)
	if st == nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "no active turn"})
	}

	reqCtx := c.Request().Context()
	w := c.Response()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx/caddy buffering
	w.WriteHeader(http.StatusOK)

	// Replay what this client missed, then follow the live channel. A client
	// attaching to a turn already in progress would otherwise watch an empty
	// card until the next tool call, which on a slow turn is indistinguishable
	// from nothing happening at all.
	backlog, done, turnErr := st.snapshot()
	for _, line := range backlog {
		writeSSELines(w, line)
	}
	w.Flush()

	// A turn that finished before this client attached has a closed channel, so
	// the loop below would report done immediately — but say so explicitly
	// rather than relying on that, since the replay above is the only thing this
	// client will ever see of it.
	if done {
		if turnErr != nil {
			fmt.Fprint(w, "event: error\ndata: 1\n\n")
		} else {
			fmt.Fprint(w, "event: done\ndata: 1\n\n")
		}
		w.Flush()
		return nil
	}

	for {
		select {
		case <-reqCtx.Done():
			return nil
		case msg, ok := <-st.progressCh:
			if !ok {
				// data must be non-empty or the browser won't dispatch the event.
				fmt.Fprint(w, "event: done\ndata: 1\n\n")
				w.Flush()
				return nil
			}
			writeSSELines(w, msg)
			w.Flush()
		}
	}
}

// writeSSELines emits one data field per line: SSE data fields cannot contain
// raw newlines, so a multi-line milestone must be split or the stream desyncs.
func writeSSELines(w io.Writer, msg string) {
	for _, line := range strings.Split(msg, "\n") {
		fmt.Fprintf(w, "data: %s\n\n", line)
	}
}
