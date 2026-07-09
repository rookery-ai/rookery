package web

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/agentdesigner"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/secrets"
	"github.com/ilijad1/simple-agents/internal/skilllibrary"
	"github.com/labstack/echo/v4"
	"github.com/robfig/cron/v3"
)

type agentsPageData struct {
	*pageData
	Agents  []*db.Agent
	Running map[string]bool // agentID → true if a run is currently in flight
	Draft   *db.AgentDraft  // unfinished design draft, or nil
}

type agentDetailData struct {
	*pageData
	Agent          *db.Agent
	Schedule       *db.AgentSchedule
	Runs           []*db.AgentRun
	Manifest       *agentdesigner.AgentManifest
	AgentMD        string   // AGENT.md
	State          string   // state.json (read-only)
	Logs           []string // sorted log file names (newest first)
	LastLog        string   // content of most recent log
	AttachedSet    map[string]bool       // agent_skills names (core+user) → true; drives checkbox "checked"
	AttachedSkills []string              // agent_skills names in DB order; renders the attached-skill badges
	CoreSkills     []skilllibrary.SkillMeta // core (embedded) checkbox pool, always-on
	AllSkills      []*db.Skill           // user-installed checkbox pool
	MissingSecrets []string
	HasPlatform    bool // user has at least one linked chat platform
	Running        bool // a run is in flight (manual or scheduled) — drives the badge
	LiveRun        bool // a manual run is in flight on THIS server — gates the SSE stream
}

type newAgentPageData struct {
	*pageData
	HasPlatform bool
	Draft       *db.AgentDraft // unfinished design draft, or nil
}

func (s *Server) showAgents(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	agents, _ := s.db.ListAgents(u.ID)
	running := make(map[string]bool, len(agents))
	for _, a := range agents {
		if s.isAgentRunning(a.ID) {
			running[a.ID] = true
		}
	}
	var draft *db.AgentDraft
	if s.designFlow != nil {
		draft = s.designFlow.HasDraft(u.ID)
	}
	return c.Render(http.StatusOK, "dashboard/agents.html", &agentsPageData{
		pageData: s.page(c, "My Agents"),
		Agents:   agents,
		Running:  running,
		Draft:    draft,
	})
}

func (s *Server) showNewAgent(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	var draft *db.AgentDraft
	if s.designFlow != nil {
		draft = s.designFlow.HasDraft(u.ID)
	}
	return c.Render(http.StatusOK, "dashboard/agent_new.html", &newAgentPageData{
		pageData:    s.page(c, "Create Agent"),
		HasPlatform: s.db.HasPlatformIdentity(u.ID),
		Draft:       draft,
	})
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
	type histEntry struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	snap := s.designFlow.Snapshot(u.ID)
	hist := make([]histEntry, 0, len(snap.History))
	for _, m := range snap.History {
		hist = append(hist, histEntry{Role: m.Role, Content: m.Content})
	}
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
		snap := s.designFlow.Snapshot(u.ID)
		return c.JSON(http.StatusOK, map[string]interface{}{
			"response":          response,
			"done":              false,
			"state":             snap.State,
			"generation_failed": snap.GenerationFailed,
			"can_keep_as_is":    snap.CanKeepAsIs,
		})
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

	snap := s.designFlow.Snapshot(u.ID)
	return c.JSON(http.StatusOK, map[string]interface{}{
		"response":          response,
		"done":              false,
		"state":             snap.State,
		"generation_failed": snap.GenerationFailed,
		"can_keep_as_is":    snap.CanKeepAsIs,
	})
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
	type histEntry struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	hist := make([]histEntry, 0, len(snap.History))
	for _, m := range snap.History {
		hist = append(hist, histEntry{Role: m.Role, Content: m.Content})
	}
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

// showEditAgent renders the conversational edit UI for an existing agent.
// GET /dashboard/agents/:id/edit
func (s *Server) showEditAgent(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	id := c.Param("id")

	agent, err := s.db.GetAgent(id)
	if err != nil || agent.WorkspaceID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "agent not found")
	}

	var agentMD string
	if dir := s.agentsDir(); dir != "" {
		if raw, err := os.ReadFile(agentdesigner.AgentDescPath(dir, u.ID, id)); err == nil {
			agentMD = string(raw)
		}
	}

	return c.Render(http.StatusOK, "dashboard/agent_edit.html", &agentDetailData{
		pageData:    s.page(c, "Edit Agent: "+agent.Name),
		Agent:       agent,
		AgentMD:     agentMD,
		HasPlatform: s.db.HasPlatformIdentity(u.ID),
	})
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

	return c.JSON(http.StatusOK, map[string]interface{}{
		"response": response,
		"done":     false,
	})
}

func (s *Server) showAgentDetail(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	id := c.Param("id")

	agent, err := s.db.GetAgent(id)
	if err != nil || agent.WorkspaceID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "agent not found")
	}

	return s.renderAgentDetail(c, agent, u.ID, s.page(c, "Agent: "+agent.Name))
}

// agentsDir returns the vaults base directory from config (or empty string in
// tests). Agent dirs live at <base>/<workspaceID>/agents/<agentID>.
func (s *Server) agentsDir() string {
	if s.cfg == nil {
		return ""
	}
	return filepath.Join(s.cfg.Data.Dir, "vaults")
}

// renderAgentDetail loads all data needed for the agent detail page and renders it.
func (s *Server) renderAgentDetail(c echo.Context, agent *db.Agent, workspaceID string, p *pageData) error {
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

	data := &agentDetailData{
		pageData:    p,
		Agent:       agent,
		Schedule:    schedule,
		Runs:        runs,
		AttachedSet: attached,
		AttachedSkills: attachedSkills,
		CoreSkills:  skilllibrary.LoadBundled(),
		AllSkills:   allSkills,
		Running:     s.isAgentRunning(agent.ID),
		LiveRun:     s.isLiveRun(agent.ID),
	}

	dir := s.agentsDir()
	if dir != "" {
		manifest, _ := agentdesigner.LoadManifest(dir, workspaceID, agent.ID)
		data.Manifest = manifest

		// Load AGENT.md (fall back to CLAUDE.md for legacy agents).
		if raw, err := os.ReadFile(agentdesigner.AgentDescPath(dir, workspaceID, agent.ID)); err == nil {
			data.AgentMD = string(raw)
		} else if raw, err := os.ReadFile(agentdesigner.AgentMDPath(dir, workspaceID, agent.ID)); err == nil {
			data.AgentMD = string(raw)
		}

		// Load state.json.
		if raw, err := os.ReadFile(agentdesigner.AgentStatePath(dir, workspaceID, agent.ID)); err == nil {
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

		// Compute missing secrets for manifest-declared requirements.
		if manifest != nil && len(manifest.RequiredSecrets) > 0 {
			knownNames, _ := s.db.ListSecretNames(workspaceID)
			knownSet := make(map[string]bool, len(knownNames))
			for _, n := range knownNames {
				knownSet[n] = true
			}
			for _, req := range manifest.RequiredSecrets {
				if !knownSet[req] {
					data.MissingSecrets = append(data.MissingSecrets, req)
				}
			}
		}
	}

	return c.Render(http.StatusOK, "dashboard/agent_detail.html", data)
}

func (s *Server) handleDeleteAgent(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	id := c.Param("id")

	agent, err := s.db.GetAgent(id)
	if err != nil || agent.WorkspaceID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "agent not found")
	}

	if err := s.db.DeleteAgent(id); err != nil {
		return err
	}

	// Remove the agent's directory from the user's vault so no orphaned files
	// linger (otherwise the deleted agent keeps showing up in the knowledge base).
	if dir := s.agentsDir(); dir != "" {
		_ = os.RemoveAll(agentdesigner.AgentDir(dir, u.ID, id))
	}

	s.audit.Log(u.ID, "delete_agent", "agent:"+id, agent.Name, c.RealIP())
	return c.Redirect(http.StatusFound, "/dashboard/agents")
}

func (s *Server) handleRunAgent(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	id := c.Param("id")

	agent, err := s.db.GetAgent(id)
	if err != nil || agent.WorkspaceID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "agent not found")
	}

	s.audit.Log(u.ID, "run_agent", "agent:"+id, agent.Name, c.RealIP())

	p := s.page(c, "Agent: "+agent.Name)

	if s.runner == nil {
		p.Error = "Agent runner is not configured"
		return s.renderAgentDetail(c, agent, u.ID, p)
	}

	// Decrypt the user's stored master password the same way the scheduler does for
	// cron runs, so manual "Run Now" gets secret injection too. There is no
	// password-entry field on this form — agent execution (unlike viewing secret
	// values) doesn't require live re-entry.
	var masterPw string
	if u.EncryptedMasterPassword != "" {
		if pw, err := secrets.DecryptMasterPassword(u.EncryptedMasterPassword, s.systemKey); err == nil {
			masterPw = pw
		}
	}

	// Fire the run in the background on a detached context so it survives the user
	// navigating away (the old synchronous path was tied to the request context and
	// got SIGKILLed on navigation, surfacing as "exit -1"). Progress streams to the
	// detail page over SSE; the final result is also delivered to the user's chat.
	//
	// MUST redirect (303 See Other) rather than render the page in response to the
	// POST. Rendering left the browser on a POST-loaded page; the detail-page JS
	// reloads when the SSE run completes, and on a POST-loaded page `reload()` replays
	// the POST — firing "Run Now" again in an infinite loop, one run per ~15-30s,
	// burning tokens. Post/Redirect/Get breaks the loop: the browser lands on a
	// GET-loaded page so reload is a safe GET. The running badge + live-progress
	// panel convey "started"/"already running" visually, so no flash message is lost.
	s.startManualRun(u.ID, agent, masterPw)
	return c.Redirect(http.StatusSeeOther, "/dashboard/agents/"+id)
}

func (s *Server) handleSaveSchedule(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	id := c.Param("id")

	agent, err := s.db.GetAgent(id)
	if err != nil || agent.WorkspaceID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "agent not found")
	}

	cronExpr := strings.TrimSpace(c.FormValue("cron_expr"))
	p := s.page(c, "Agent: "+agent.Name)

	if cronExpr == "" {
		p.Error = "Cron expression is required"
	} else {
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		sched, parseErr := parser.Parse(cronExpr)
		if parseErr != nil {
			p.Error = "Invalid cron expression: " + parseErr.Error()
		} else {
			nextRun := sched.Next(time.Now())
			// Reuse existing schedule ID so ON CONFLICT(id) updates rather than inserts.
			existing, _ := s.db.GetScheduleForAgent(agent.ID)
			schedID := uuid.New().String()
			if existing != nil {
				schedID = existing.ID
			}
			row := &db.AgentSchedule{
				ID:        schedID,
				AgentID:   agent.ID,
				WorkspaceID:    u.ID,
				CronExpr:  cronExpr,
				NextRunAt: &nextRun,
				Enabled:   true,
			}
			if err := s.db.UpsertAgentSchedule(row); err != nil {
				p.Error = "Failed to save schedule: " + err.Error()
			} else {
				p.Success = "Schedule saved"
				s.audit.Log(u.ID, "save_schedule", "agent:"+id, cronExpr, c.RealIP())
			}
		}
	}

	return s.renderAgentDetail(c, agent, u.ID, p)
}

func (s *Server) handleDeleteSchedule(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	id := c.Param("id")

	agent, err := s.db.GetAgent(id)
	if err != nil || agent.WorkspaceID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "agent not found")
	}

	_ = s.db.DeleteAgentSchedule(id)
	s.audit.Log(u.ID, "delete_schedule", "agent:"+id, "", c.RealIP())
	return c.Redirect(http.StatusFound, "/dashboard/agents/"+id)
}
