package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/rookery-ai/rookery/internal/agentrunner"
	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/gateway"
)

// maxRetainedLines bounds the live buffer. A runaway run must not grow the
// server's memory without limit, and nobody reads 2000 lines of tail anyway.
// Over the cap the OLDEST lines are dropped, not the newest: this buffer feeds
// a live progress view, where freezing at the first 2000 lines would be worse
// than losing the beginning.
const maxRetainedLines = 2000

// agentRunState tracks one in-flight manual ("Run Now") agent run. The run executes
// in a goroutine on a detached context so navigating away from the page never kills
// it; this state lets the SSE endpoint stream the run's [CHAT] output live.
//
// Progress is RETAINED rather than piped. This used to be a `chan string`, which
// is consume-once and single-reader: leaving the agent page closed the SSE stream
// and every line already delivered was gone, so returning to a running agent
// showed an empty card and a timer counting from zero. Two tabs on the same run
// also stole each other's lines, because each message went to whichever reader
// happened to receive it.
//
// Readers follow by INDEX into `lines` and wait on `notify`, rather than each
// holding a channel the producer writes to. That is what makes the fan-out
// lossless: a per-subscriber channel has to either block the run on a slow
// reader or drop, and dropping is how a live view silently disagrees with the
// record it is supposed to be showing.
type agentRunState struct {
	mu        sync.Mutex
	startedAt time.Time
	lines     []string
	// dropped counts lines discarded off the front once the cap was hit, so a
	// reader's absolute position stays meaningful across a truncation.
	dropped int
	// notify is closed (and replaced) on every append and on finish. Closing is
	// the broadcast: every waiting reader wakes, and a reader that grabbed the
	// channel before the append still sees it closed rather than missing the
	// wakeup.
	notify chan struct{}
	done   bool
	err    error
}

func newAgentRunState() *agentRunState {
	return &agentRunState{
		startedAt: time.Now(),
		notify:    make(chan struct{}),
	}
}

// broadcast wakes every waiting reader. Caller must hold mu.
func (rs *agentRunState) broadcast() {
	close(rs.notify)
	rs.notify = make(chan struct{})
}

// appendLine records one progress line and wakes readers.
func (rs *agentRunState) appendLine(msg string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.lines = append(rs.lines, msg)
	if over := len(rs.lines) - maxRetainedLines; over > 0 {
		rs.lines = append([]string(nil), rs.lines[over:]...)
		rs.dropped += over
	}
	rs.broadcast()
}

// finish marks the run complete and releases every reader.
func (rs *agentRunState) finish(err error) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.done = true
	rs.err = err
	rs.broadcast()
}

func (rs *agentRunState) isDone() bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.done
}

// since reports how long the run has been going. Sent to the browser instead of
// an absolute timestamp so the elapsed clock cannot be thrown off by a client
// whose clock disagrees with the server's.
func (rs *agentRunState) since() time.Duration {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return time.Since(rs.startedAt)
}

// readFrom returns the lines at or after absolute index `next`, the next index
// to ask for, whether the run has finished, and a channel to wait on when there
// is nothing new yet.
//
// A reader whose position was truncated away is fast-forwarded to the oldest
// retained line: it has already missed those lines, and blocking or erroring
// would be worse than resuming from what is still held.
func (rs *agentRunState) readFrom(next int) (batch []string, advanced int, done bool, wait chan struct{}) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if next < rs.dropped {
		next = rs.dropped
	}
	if from := next - rs.dropped; from < len(rs.lines) {
		batch = append(batch, rs.lines[from:]...)
		next += len(batch)
	}
	return batch, next, rs.done, rs.notify
}

// startManualRun launches an agent run in the background on a detached context and
// registers an agentRunState for SSE streaming. Returns false if a run for this
// agent is already in flight (so a double-click can't fire it twice).
func (s *Server) startManualRun(workspaceID string, agent *db.Agent, masterPw string) bool {
	s.runsMu.Lock()
	if existing, ok := s.runs[agent.ID]; ok {
		existing.mu.Lock()
		running := !existing.done
		existing.mu.Unlock()
		if running {
			s.runsMu.Unlock()
			return false
		}
	}
	s.runsMu.Unlock()

	// Also refuse if a run (manual OR scheduled) is already in flight per the DB, so a
	// manual run can't collide with a cron run in the same agent dir.
	if run, err := s.db.GetUnfinishedAgentRun(agent.ID); err == nil && run != nil {
		return false
	}

	s.runsMu.Lock()
	rs := newAgentRunState()
	s.runs[agent.ID] = rs
	s.runsMu.Unlock()

	go func() {
		// Detached context: the run must outlive the HTTP request that started it.
		// coder.Generate already bounds each turn by the coder profile's timeout, so
		// a plain background context is correct here — a single per-turn budget applied
		// to the whole multi-turn run would kill normal runs early.
		ctx := context.Background()

		// Never drops: appendLine retains the line and wakes readers, so a viewer
		// who was not attached at the time still sees it on replay.
		onProgress := rs.appendLine
		agentName := agent.Name
		send := func(msg string) {
			// Durable delivery to the user's chat platform (same path cron runs use),
			// so the result arrives even after the user has left the page.
			//
			// Labelled with the agent name for the same reason the scheduler does
			// it: on the chat side these messages are otherwise anonymous. Only
			// the CHAT copy is labelled — OnProgress feeds the live SSE view,
			// which is already scoped to this agent's page.
			if s.gateway != nil {
				_ = s.gateway.SendToUser(workspaceID, gateway.AgentPrefixed(agentName, msg))
			}
		}

		runErr := s.runner.Run(ctx, agentrunner.RunInput{
			AgentID:     agent.ID,
			WorkspaceID: workspaceID,
			Trigger:     "manual",
			MasterPw:    masterPw,
			OnProgress:  onProgress,
			SendOutput:  send,
		})

		if runErr != nil {
			// Surface the failure on the live stream too (SendOutput already delivered
			// the friendly message to the chat platform). Appended BEFORE finish, or a
			// reader can observe done and stop before the line it most needs arrives.
			onProgress("⚠️ " + runErr.Error())
		}
		rs.finish(runErr)

		// Evict after a grace period so a late/reconnecting viewer can still observe
		// the terminal state; the durable record lives in run history.
		time.AfterFunc(90*time.Second, func() {
			s.runsMu.Lock()
			if cur, ok := s.runs[agent.ID]; ok && cur == rs {
				delete(s.runs, agent.ID)
			}
			s.runsMu.Unlock()
		})
	}()
	return true
}

// isAgentRunning reports whether a manual run for this agent is in flight. It checks
// the in-memory tracker first, then the durable DB flag (finished_at IS NULL) so the
// "Running…" badge survives a page reload or a tracker eviction.
func (s *Server) isAgentRunning(agentID string) bool {
	s.runsMu.Lock()
	rs, ok := s.runs[agentID]
	s.runsMu.Unlock()
	if ok && !rs.isDone() {
		return true
	}
	if run, err := s.db.GetUnfinishedAgentRun(agentID); err == nil && run != nil {
		return true
	}
	return false
}

// isLiveRun reports whether THIS server has a manual run for the agent in flight
// in memory — i.e. an SSE stream at /run/progress would actually have a producer.
// Distinct from isAgentRunning, which also returns true for scheduled runs tracked
// only in the DB (those drive the badge but must NOT open an SSE that would 404).
func (s *Server) isLiveRun(agentID string) bool {
	s.runsMu.Lock()
	rs, ok := s.runs[agentID]
	s.runsMu.Unlock()
	return ok && !rs.isDone()
}

// handleRunProgress streams a manual run's [CHAT] output to the browser via SSE.
// Closing this stream (navigating away) does NOT cancel the run — the run executes
// on a detached context and this handler only observes it.
// GET /dashboard/agents/:id/run/progress
func (s *Server) handleRunProgress(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	id := c.Param("id")

	agent, err := s.db.GetAgent(id)
	if err != nil || agent.WorkspaceID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "agent not found")
	}

	s.runsMu.Lock()
	rs, ok := s.runs[id]
	s.runsMu.Unlock()
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "no active run"})
	}

	reqCtx := c.Request().Context()
	w := c.Response()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx/caddy buffering
	w.WriteHeader(http.StatusOK)

	// The meta event carries how long the run has ALREADY been going, so a page
	// returned to mid-run shows a continuous elapsed time. Sent as a duration
	// rather than a start timestamp deliberately: the browser anchors it against
	// its own clock, and an absolute time would be wrong by however much the two
	// clocks disagree — on a self-hosted LAN install, potentially minutes.
	fmt.Fprintf(w, "event: meta\ndata: {\"elapsed_ms\":%d}\n\n", rs.since().Milliseconds())
	w.Flush()

	// Follow by absolute index from the beginning: everything retained is
	// replayed first, so a viewer who arrives late — or comes back — sees the
	// whole run rather than only what happens next.
	next := 0
	for {
		batch, advanced, done, wait := rs.readFrom(next)
		next = advanced
		for _, msg := range batch {
			// SSE data fields cannot contain raw newlines — emit one per line.
			for _, line := range strings.Split(msg, "\n") {
				fmt.Fprintf(w, "data: %s\n\n", line)
			}
		}
		if len(batch) > 0 {
			w.Flush()
		}
		// Checked AFTER draining, so the terminal batch is never dropped in
		// favour of the done event.
		if done {
			// data must be non-empty or the browser won't dispatch the event.
			fmt.Fprint(w, "event: done\ndata: 1\n\n")
			w.Flush()
			return nil
		}
		select {
		case <-reqCtx.Done():
			return nil
		case <-wait:
		}
	}
}
