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

// agentRunState tracks one in-flight manual ("Run Now") agent run. The run executes
// in a goroutine on a detached context so navigating away from the page never kills
// it; this state lets the SSE endpoint stream the run's [CHAT] output live.
type agentRunState struct {
	progressCh chan string
	mu         sync.Mutex
	done       bool
	err        error
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
	rs := &agentRunState{progressCh: make(chan string, 64)}
	s.runs[agent.ID] = rs
	s.runsMu.Unlock()

	go func() {
		// Detached context: the run must outlive the HTTP request that started it.
		// coder.Generate already bounds each turn by the coder profile's timeout, so
		// a plain background context is correct here — a single per-turn budget applied
		// to the whole multi-turn run would kill normal runs early.
		ctx := context.Background()

		onProgress := func(msg string) {
			select {
			case rs.progressCh <- msg:
			default: // buffer full — drop for the live view; run history keeps the record
			}
		}
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

		rs.mu.Lock()
		rs.done = true
		rs.err = runErr
		rs.mu.Unlock()

		if runErr != nil {
			// Surface the failure on the live stream too (SendOutput already delivered
			// the friendly message to the chat platform).
			onProgress("⚠️ " + runErr.Error())
		}
		close(rs.progressCh)

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
	if ok {
		rs.mu.Lock()
		done := rs.done
		rs.mu.Unlock()
		if !done {
			return true
		}
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
	defer s.runsMu.Unlock()
	rs, ok := s.runs[agentID]
	if !ok {
		return false
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return !rs.done
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

	for {
		select {
		case <-reqCtx.Done():
			return nil
		case msg, ok := <-rs.progressCh:
			if !ok {
				// data must be non-empty or the browser won't dispatch the event.
				fmt.Fprint(w, "event: done\ndata: 1\n\n")
				w.Flush()
				return nil
			}
			// SSE data fields cannot contain raw newlines — emit one per line.
			for _, line := range strings.Split(msg, "\n") {
				fmt.Fprintf(w, "data: %s\n\n", line)
			}
			w.Flush()
		}
	}
}
