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
	"github.com/rookery-ai/rookery/internal/logsafe"
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
	// closed guards progressCh, which has more than one sender: the turn
	// goroutine and the quiet-turn timer. time.Timer.Stop reports false when the
	// callback is ALREADY RUNNING and does not wait for it, so stopping the
	// timer on the way out is not enough on its own — the callback can be
	// mid-flight while this goroutine closes the channel. That is a send on a
	// closed channel, which panics the server rather than merely racing.
	//
	// Set and read under mu, together with the close itself, so "may I send?"
	// and "close" cannot interleave.
	closed bool
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
		slog.Error("chat: persist user message", "chat", logsafe.Value(chatID), "error", err)
	}

	go func() {
		// Detached: the turn must outlive the HTTP request that started it. The
		// coder profile's own timeout still bounds it, so this does not run
		// unbounded.
		ctx := context.Background()

		onProgress := func(msg string) {
			// The append and the send are under ONE lock hold, so a concurrent
			// caller cannot observe the channel open, be descheduled, and send
			// into it after it has been closed. The send is non-blocking, so
			// holding the lock across it cannot deadlock.
			st.mu.Lock()
			defer st.mu.Unlock()
			if st.closed {
				// The turn is over. A late milestone has nothing to attach to
				// and no reader left; recording it would also grow lines after
				// the transcript was considered final.
				return
			}
			st.lines = append(st.lines, msg)
			select {
			case st.progressCh <- msg:
			default:
				// Buffer full — drop for the LIVE view only. lines still has it,
				// so an attaching client's replay is complete either way.
			}
		}

		// A one-shot notice for a turn that has gone quiet. The fail-fast
		// classification handles a coder that is NOT there; this handles one
		// that is there and slow, which on a self-hosted local model is the
		// commoner case — loading weights on the first request of the day can
		// take a minute, and the two look identical from the browser.
		//
		// It fires only if nothing else has been said since the opening
		// milestone, so a turn making visible tool calls never accumulates
		// filler. AfterFunc rather than a goroutine and a select: there is
		// nothing to wait on.
		//
		// Stopping it below is an optimisation, NOT the safety property. Stop
		// reports false when the callback is already running and does not wait
		// for it, so this callback can still be in flight while the turn
		// goroutine finishes — which is why onProgress checks st.closed under
		// the lock rather than relying on the Stop. Sending on the closed
		// channel would panic the server, and -race caught it where a local run
		// did not.
		slowNotice := time.AfterFunc(chatSlowTurnAfter, func() {
			st.mu.Lock()
			quiet := len(st.lines) <= 1 && !st.done
			st.mu.Unlock()
			if quiet {
				onProgress("⏳ Still waiting for the first response — a local model may be loading.")
			}
		})

		reply, err := s.runChatCoder(ctx, workspaceID, chatID, history, text, onProgress)
		slowNotice.Stop()

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
			slog.Warn("chat: turn failed", "chat", logsafe.Value(chatID), "turn", st.id, "error", err)
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
			// Chat turns logged NOTHING on the happy path, so a turn that
			// produced no text left no trace anywhere — which is why the server
			// log was silent about two turns the owner reported as broken, and
			// the whole diagnosis had to come out of the database.
			// agentrunner gained its "run finished" line for exactly this
			// reason. `empty` is the field worth grepping for.
			// chatID reaches here from a path parameter. It has been validated
			// against the database by now, so nothing arbitrary should get
			// this far — but the log line should not be the thing depending on
			// that, and logsafe costs nothing.
			slog.Info("chat: turn finished",
				"chat", logsafe.Value(chatID), "turn", st.id,
				"milestones", len(st.lines),
				"reply_bytes", len(cleaned),
				"empty", strings.TrimSpace(reply) == "")
			if err := s.db.AddChatMessage(chatID, "assistant", cleaned); err != nil {
				slog.Error("chat: persist assistant message", "chat", logsafe.Value(chatID), "error", err)
			}
			_ = s.db.TouchChat(chatID)
			if ch, gerr := s.db.GetChat(chatID); gerr == nil {
				chat.MaybeAutoTitle(s.db, s.titleGen, ch, text, cleaned)
			}
		}
		// Under the lock, and paired with the closed flag onProgress checks:
		// slowNotice.Stop() above cannot promise the timer callback has
		// finished, only that it will not start again.
		st.mu.Lock()
		st.closed = true
		close(st.progressCh)
		st.mu.Unlock()

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
	case errors.Is(err, codersvc.ErrCoderUnreachable):
		// The detail is generated at the failure site and names the model and
		// endpoint, or the missing binary. Included verbatim because that is the
		// entire remedy: without it this reads as "something went wrong", which
		// is what it did before the sentinel existed.
		return "The coder could not be reached — " + codersvc.UnreachableDetail(err) +
			". Check that it is running and that the coder settings point at the right place."
	case errors.Is(err, codersvc.ErrProviderEmpty):
		return "The provider returned nothing on every retry — a temporary problem at their end. Nothing was lost; try again."
	default:
		return chatTurnGenericFailure
	}
}

// chatTurnGenericFailure is the fallback for an error nothing classified. A
// constant so the parity test can assert "this surface did NOT fall through"
// against the real string rather than a copy of it that would drift.
const chatTurnGenericFailure = "The chat turn failed. See the server log for details."

// chatSlowTurnAfter is how long a turn may stay quiet before it says so.
//
// A var rather than a const so a test can shorten it: the behaviour under test
// is "a quiet turn eventually explains itself", and waiting twenty real seconds
// to assert that would be twenty seconds on every CI run.
//
// Twenty seconds is chosen to sit above a normal hosted round-trip (so an
// ordinary turn never sees it) and below the point where a person concludes the
// thing is broken.
var chatSlowTurnAfter = 20 * time.Second

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
