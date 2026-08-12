package web

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/rookery-ai/rookery/internal/agentdesigner"
	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/skilllibrary"
)

// agentDetailData is the agent-detail view model, assembled by loadAgentDetail and
// consumed by the JSON agents API (api_agents.go → toAPIAgentDetail).
type agentDetailData struct {
	Agent          *db.Agent
	Schedule       *db.AgentSchedule
	Runs           []*db.AgentRun
	AgentMD        string                   // AGENT.md
	State          string                   // state.md, verbatim (read-only)
	Logs           []string                 // sorted log file names (newest first)
	LastLog        string                   // content of most recent log
	AttachedSet    map[string]bool          // agent_skills names (core+user) → true; drives checkbox "checked"
	AttachedSkills []string                 // agent_skills names in DB order; renders the attached-skill badges
	CoreSkills     []skilllibrary.SkillMeta // core (embedded) checkbox pool, always-on
	AllSkills      []*db.Skill              // user-installed checkbox pool
	WorkspaceConns []db.ServiceConnection   // connected service accounts (the checkbox pool)
	AttachedConns  map[string]bool          // connection IDs bound to this agent → true
	MissingSecrets []string
	HasPlatform    bool // user has at least one linked chat platform
	Running        bool // a run is in flight (manual or scheduled) — drives the badge
	LiveRun        bool // a manual run is in flight on THIS server — gates the SSE stream
}

// designHistEntry is the wire shape of one design-conversation turn. CreatedAt is
// a preformatted STRING, not a time.Time: `omitempty` does nothing for a struct,
// so a time.Time field would emit "0001-01-01T00:00:00Z" for drafts written
// before turns were timestamped, and the browser would render a bubble stamped
// year 1. RFC3339Nano matches what /api/v1/chats/:id/messages emits for
// created_at, so both chat surfaces feed formatMessageTime identical input.
type designHistEntry struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at,omitempty"`
}

// designHistoryDTO maps session history to the wire shape. Shared by the agent
// resume/state handlers and the skill resume handler so the three cannot drift.
func designHistoryDTO(hist []db.ChatMessage) []designHistEntry {
	out := make([]designHistEntry, 0, len(hist))
	for _, m := range hist {
		e := designHistEntry{Role: m.Role, Content: m.Content}
		if !m.CreatedAt.IsZero() {
			e.CreatedAt = m.CreatedAt.Format(time.RFC3339Nano)
		}
		out = append(out, e)
	}
	return out
}

// designTurnResponse is the body every non-terminal design turn returns. The
// create chat and the first turn of an EDIT share it so they cannot drift: the
// edit page mounts DesignerSurface directly (no pre-screen remount), so this
// response is the only thing that tells the browser which FSM state it is in —
// without `state` the stepper never leaves "Describe" and the Build button never
// appears until the user sends a throwaway second message.
func designTurnResponse(response string, snap agentdesigner.DesignSnapshot) map[string]interface{} {
	return map[string]interface{}{
		"response": response,
		"done":     false,
		"state":    snap.State,
		// Generation is now detached on BOTH surfaces, so the turn that starts a
		// build returns immediately with a placeholder rather than the build's
		// result. Without this flag the SPA would render that placeholder and wait
		// forever: DesignerSurface only attaches the SSE stream — and only refetches
		// /state when it closes — when `building` is true.
		//
		// Reading it from the snapshot rather than hardcoding keeps one source of
		// truth with IsGenerating, which is also what the chat router's concurrency
		// guard reads.
		"building":          snap.Generating,
		"generation_failed": snap.GenerationFailed,
		"can_keep_as_is":    snap.CanKeepAsIs,
	}
}

// handleResumeDraft reconstructs the user's saved design draft as an active
// session and returns the conversation history + resumption message so the
// browser can replay the chat and continue. The coder is never re-run here —
// generation only happens when the user next says "approve".
// POST /dashboard/agents/design/resume
func (s *Server) handleResumeDraft(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.designFlow == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "agent designer not configured"})
	}
	resp, err := s.designFlow.ResumeDraft(c.Request().Context(), u.ID)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	snap := s.designFlow.Snapshot(u.ID)
	hist := designHistoryDTO(snap.History)
	out := map[string]interface{}{
		"response":          resp,
		"state":             snap.State,
		"history":           hist,
		"agent_id":          snap.AgentID,
		"agent_name":        snap.AgentName,
		"generation_failed": snap.GenerationFailed,
		"can_keep_as_is":    snap.CanKeepAsIs,
	}
	return c.JSON(http.StatusOK, out)
}

// handleDismissDraft clears the user's saved draft (and, for create-mode
// verifying drafts, removes the orphaned pre-approved agent directory).
// POST /dashboard/agents/design/dismiss
func (s *Server) handleDismissDraft(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.designFlow != nil {
		_ = s.designFlow.DismissDraft(u.ID)
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// handleDesignChat drives the conversational agent creation via JSON API.
// POST /dashboard/agents/design
// Body: {"name": "my-agent", "message": "..."}
// Response: {"response": "...", "done": false} or {"response": "...", "done": true, "agent_id": "..."}
func (s *Server) handleDesignChat(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)

	var req struct {
		Name    string `json:"name"`
		Message string `json:"message"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Message = strings.TrimSpace(req.Message)

	if req.Message == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "message is required"})
	}

	if s.designFlow == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "agent designer not configured"})
	}

	// Reject concurrent design turns while a build is running. Generation is
	// detached from the request context, so it keeps running after the user
	// navigates away — a returning tab must not launch a second coder run on the
	// same session. The live result surfaces via the SSE progress stream and the
	// /design/state endpoint, not this POST.
	if s.designFlow.IsGenerating(u.ID) {
		return c.JSON(http.StatusOK, map[string]interface{}{
			"response": "⏳ Still building your agent — I'll show the result here as soon as it's done.",
			"done":     false,
			"building": true,
		})
	}

	ctx := c.Request().Context()

	// If no active session and a name is provided, start a new design session.
	if s.designFlow.GetSession(u.ID) == nil {
		if req.Name == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "name is required to start a new session"})
		}
		response, err := s.designFlow.StartDesign(ctx, u.ID, req.Name, req.Message)
		if err != nil {
			slog.Error("agentdesigner: start design failed", "workspace_id", u.ID, "name", req.Name, "err", err)
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, designTurnResponse(response, s.designFlow.Snapshot(u.ID)))
	}

	// Existing session: step the FSM. Capture whether this is an edit session (and
	// its name, for the audit log) *before* stepping — Step deletes the session
	// from memory once isDone, so sess fields would no longer be readable afterwards.
	sess := s.designFlow.GetSession(u.ID)
	wasEdit := sess != nil && sess.IsEdit
	auditName := req.Name
	if sess != nil && sess.AgentName != "" {
		auditName = sess.AgentName
	}

	response, isDone, agentID, err := s.designFlow.Step(ctx, u.ID, req.Message)
	if err != nil {
		slog.Error("agentdesigner: design step failed", "workspace_id", u.ID, "err", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	if isDone {
		action := "create_agent"
		if wasEdit {
			action = "edit_agent"
		}
		s.audit.Log(u.ID, action, "agent:"+agentID, auditName, c.RealIP())
		return c.JSON(http.StatusOK, map[string]interface{}{
			"response": response,
			"done":     true,
			"agent_id": agentID,
		})
	}

	return c.JSON(http.StatusOK, designTurnResponse(response, s.designFlow.Snapshot(u.ID)))
}

// handleDesignState returns the live in-memory design session (if any) so a
// reloading page can restore the conversation and, when a build is still running,
// reconnect to it via the SSE progress stream. When no live session exists the
// DB draft is the durable fallback (shown as a resume banner) — e.g. after a
// server restart, which does not preserve the in-memory session.
// GET /dashboard/agents/design/state
func (s *Server) handleDesignState(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.designFlow == nil {
		return c.JSON(http.StatusOK, map[string]interface{}{"active": false})
	}
	snap := s.designFlow.Snapshot(u.ID)
	if !snap.Active {
		return c.JSON(http.StatusOK, map[string]interface{}{"active": false})
	}
	hist := designHistoryDTO(snap.History)
	return c.JSON(http.StatusOK, map[string]interface{}{
		"active":            true,
		"generating":        snap.Generating,
		"state":             snap.State,
		"history":           hist,
		"name":              snap.AgentName,
		"agent_id":          snap.AgentID,
		"is_edit":           snap.IsEdit,
		"last_progress":     snap.LastProgress,
		"generation_failed": snap.GenerationFailed,
		"can_keep_as_is":    snap.CanKeepAsIs,
		"pending_agent_md":  snap.PendingAgentMD,
		"pending_tools":     snap.PendingTools,
	})
}

// handleCancelDesign cancels the active design session, killing any in-flight
// coder subprocess and closing the SSE progress channel.
// POST /dashboard/agents/design/cancel
func (s *Server) handleCancelDesign(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.designFlow != nil {
		s.designFlow.Cancel(u.ID)
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "cancelled"})
}

// handleDesignProgress streams generation milestone events via Server-Sent Events.
// The browser opens this endpoint when the user sends an approval message. The
// handler polls for progressCh for up to 30 s (the POST and runGeneration start
// concurrently, so the channel may not exist yet). Once found it streams until
// the channel closes or the client disconnects.
// GET /dashboard/agents/design/progress
func (s *Server) handleDesignProgress(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	reqCtx := c.Request().Context()

	if s.designFlow == nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "no design flow"})
	}

	// Poll up to 30 s for progressCh to appear. The browser opens this endpoint
	// before (or concurrently with) the approval POST, so the channel may not
	// exist yet. Stop early if the client disconnects.
	var ch <-chan string
	for i := 0; i < 150; i++ {
		select {
		case <-reqCtx.Done():
			return nil
		default:
		}
		if c2, ok := s.designFlow.GetProgressChan(u.ID); ok {
			ch = c2
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if ch == nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "no active generation"})
	}

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
		case msg, ok := <-ch:
			if !ok {
				return nil
			}
			fmt.Fprintf(w, "data: %s\n\n", msg)
			w.Flush()
		}
	}
}

// handleStartEditDesign starts a new edit session for an existing agent and
// returns the coder's first response. Continuation reuses handleDesignChat /
// handleCancelDesign — the session, once created, is keyed by workspaceID like any
// other design session.
// POST /dashboard/agents/:id/edit/start
// Body: {"message": "..."}
func (s *Server) handleStartEditDesign(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	id := c.Param("id")

	agent, err := s.db.GetAgent(id)
	if err != nil || agent.WorkspaceID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "agent not found")
	}

	var req struct {
		Message string `json:"message"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "message is required"})
	}

	if s.designFlow == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "agent designer not configured"})
	}

	response, err := s.designFlow.StartEditDesign(c.Request().Context(), u.ID, id, req.Message)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	// The full design-turn body, not just {response,done}: the edit page mounts
	// DesignerSurface directly now, so nothing remounts into GET /design/state to
	// pick the FSM state up afterwards — see designTurnResponse.
	return c.JSON(http.StatusOK, designTurnResponse(response, s.designFlow.Snapshot(u.ID)))
}

// agentsDir returns the vaults base directory from config (or empty string in
// tests). Agent dirs live at <base>/<workspaceID>/agents/<agentID>.
func (s *Server) agentsDir() string {
	if s.cfg == nil {
		return ""
	}
	return filepath.Join(s.cfg.Data.Dir, "vaults")
}

// loadAgentDetail loads all data needed for the agent detail JSON API without
// rendering. Consumed by api_agents.go (toAPIAgentDetail).
func (s *Server) loadAgentDetail(ctx context.Context, agent *db.Agent, workspaceID string) *agentDetailData {
	schedule, _ := s.db.GetScheduleForAgent(agent.ID)
	runs, _ := s.db.ListAgentRuns(agent.ID, 10)
	allSkills, _ := s.db.ListSkills(workspaceID)

	// AttachedSet is the source of truth for the Skills card: the agent's skill
	// attachments from the agent_skills DB table (names, core+user). AGENT.md is
	// only the LLM's instructions — skills are never derived from it.
	attached := make(map[string]bool)
	var attachedSkills []string
	if names, err := s.db.ListAgentSkillNames(agent.ID); err == nil {
		attachedSkills = names
		for _, name := range names {
			attached[name] = true
		}
	}

	// Connections card: the workspace's connected accounts (pool) + which are bound to
	// this agent (agent_connections). Binding is the source of truth for run-time tools —
	// not the AGENT.md "# Connections:" header (which the model may forget to emit).
	wsConns, _ := s.db.ListServiceConnections(ctx, workspaceID)
	attachedConns := make(map[string]bool)
	if bound, err := s.db.ListAgentConnections(ctx, agent.ID); err == nil {
		for _, b := range bound {
			attachedConns[b.ID] = true
		}
	}

	data := &agentDetailData{
		Agent:          agent,
		Schedule:       schedule,
		Runs:           runs,
		AttachedSet:    attached,
		AttachedSkills: attachedSkills,
		CoreSkills:     skilllibrary.LoadBundled(),
		AllSkills:      allSkills,
		WorkspaceConns: wsConns,
		AttachedConns:  attachedConns,
		Running:        s.isAgentRunning(agent.ID),
		LiveRun:        s.isLiveRun(agent.ID),
	}

	dir := s.agentsDir()
	if dir != "" {
		// Load AGENT.md (fall back to CLAUDE.md for legacy agents).
		if raw, err := os.ReadFile(agentdesigner.AgentDescPath(dir, workspaceID, agent.ID)); err == nil {
			data.AgentMD = string(raw)
		} else if raw, err := os.ReadFile(agentdesigner.AgentMDPath(dir, workspaceID, agent.ID)); err == nil {
			data.AgentMD = string(raw)
		}

		// Load state.md verbatim — it is a document now, shown as-is (not
		// re-marshalled). Missing file → empty string, not an error.
		if raw, err := os.ReadFile(agentdesigner.StateFilePath(dir, workspaceID, agent.ID)); err == nil {
			data.State = string(raw)
		}

		// List log files (newest first).
		logsDir := agentdesigner.AgentLogsDir(dir, workspaceID, agent.ID)
		if entries, err := os.ReadDir(logsDir); err == nil {
			for i := len(entries) - 1; i >= 0; i-- {
				e := entries[i]
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".txt") {
					data.Logs = append(data.Logs, e.Name())
				}
			}
			// Load content of the most recent log.
			if len(data.Logs) > 0 {
				if raw, err := os.ReadFile(filepath.Join(logsDir, data.Logs[0])); err == nil {
					data.LastLog = string(raw)
				}
			}
		}

		// Compute missing secrets declared in AGENT.md's "# Required secrets:"
		// block. agent.json (the old cache of this) is gone — AGENT.md, already
		// loaded above, is the only record of what an agent needs.
		if requiredSecrets := agentdesigner.ParseRequiredSecrets(data.AgentMD); len(requiredSecrets) > 0 {
			knownNames, _ := s.db.ListSecretNames(workspaceID)
			knownSet := make(map[string]bool, len(knownNames))
			for _, n := range knownNames {
				knownSet[n] = true
			}
			for _, req := range requiredSecrets {
				if !knownSet[req] {
					data.MissingSecrets = append(data.MissingSecrets, req)
				}
			}
		}
	}

	return data
}
