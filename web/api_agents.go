package web

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/rookery/internal/agentdesigner"
	"github.com/ilijad1/rookery/internal/db"
	"github.com/ilijad1/rookery/internal/secrets"
	"github.com/ilijad1/rookery/internal/skilllibrary"
	"github.com/labstack/echo/v4"
	"github.com/robfig/cron/v3"
)

// registerAgentsAPI registers the JSON CRUD/run/schedule endpoints plus the
// design/SSE family (re-registered UNCHANGED — those keep their legacy
// {"error":"string"} shapes per the API plan's global constraints) on the
// given group (already guarded by requireOwnerAPI + requireActiveWorkspaceAPI
// + requireSetupCompleteAPI).
func (s *Server) registerAgentsAPI(g *echo.Group) {
	g.GET("/agents", s.apiListAgents)
	g.GET("/agents/:id", s.apiGetAgent)
	g.DELETE("/agents/:id", s.apiDeleteAgent)
	g.POST("/agents/:id/run", s.apiRunAgent)
	g.PUT("/agents/:id/schedule", s.apiSaveSchedule)
	g.DELETE("/agents/:id/schedule", s.apiDeleteSchedule)
	g.PUT("/agents/:id/agent-md", s.apiSaveAgentMD)
	g.PUT("/agents/:id/skills", s.apiSaveAgentSkills)
	g.PUT("/agents/:id/connections", s.apiSaveAgentConnections)
	s.registerApprovalRoutes(g)

	// Design/SSE family: unchanged legacy handlers, unchanged legacy error shapes.
	g.POST("/agents/design", s.handleDesignChat)
	g.POST("/agents/design/cancel", s.handleCancelDesign)
	g.POST("/agents/design/resume", s.handleResumeDraft)
	g.POST("/agents/design/dismiss", s.handleDismissDraft)
	g.GET("/agents/design/progress", s.handleDesignProgress)
	g.GET("/agents/design/state", s.handleDesignState)
	g.POST("/agents/:id/edit/start", s.handleStartEditDesign)
	g.GET("/agents/:id/run/progress", s.handleRunProgress)
}

// ── DTOs ─────────────────────────────────────────────────────────────────────

type apiAgent struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Active      bool      `json:"active"`
	CreatedAt   time.Time `json:"created_at"`
	Running     bool      `json:"running"`
}

func (s *Server) toAPIAgent(a *db.Agent) apiAgent {
	return apiAgent{
		ID:          a.ID,
		Name:        a.Name,
		Description: a.Description,
		Active:      a.Active,
		CreatedAt:   a.CreatedAt,
		Running:     s.isAgentRunning(a.ID),
	}
}

func toAPIAgentDraft(d *db.AgentDraft) map[string]any {
	if d == nil {
		return nil
	}
	return map[string]any{
		"agent_id":   d.AgentID,
		"agent_name": d.AgentName,
		"is_edit":    d.IsEdit,
		"state":      d.State,
		"updated_at": d.UpdatedAt,
		"expires_at": d.ExpiresAt,
	}
}

func toAPISchedule(sc *db.AgentSchedule) map[string]any {
	if sc == nil {
		return nil
	}
	return map[string]any{
		"cron_expr":   sc.CronExpr,
		"next_run_at": sc.NextRunAt,
		"last_run_at": sc.LastRunAt,
		"enabled":     sc.Enabled,
	}
}

func toAPIRun(r *db.AgentRun) map[string]any {
	status := "running"
	if r.ExitCode != nil {
		if *r.ExitCode == 0 {
			status = "success"
		} else {
			status = "failed"
		}
	}
	return map[string]any{
		"id":                r.ID,
		"trigger":           r.Trigger,
		"status":            status,
		"exit_code":         r.ExitCode,
		"stdout":            r.Stdout,
		"stderr":            r.Stderr,
		"prompt_tokens":     r.PromptTokens,
		"completion_tokens": r.CompletionTokens,
		"total_tokens":      r.TotalTokens,
		"started_at":        r.StartedAt,
		"finished_at":       r.FinishedAt,
	}
}

func toAPICoreSkill(m skilllibrary.SkillMeta) map[string]any {
	return map[string]any{"name": m.Name, "description": m.Description}
}

func toAPISkill(sk *db.Skill) map[string]any {
	return map[string]any{
		"id":           sk.ID,
		"name":         sk.Name,
		"description":  sk.Description,
		"installed_at": sk.InstalledAt,
	}
}

func toAPIConnection(cn db.ServiceConnection) map[string]any {
	return map[string]any{
		"id":               cn.ID,
		"provider":         cn.Provider,
		"account_label":    cn.AccountLabel,
		"account_identity": cn.AccountIdentity,
		"status":           cn.Status,
		"created_at":       cn.CreatedAt,
	}
}

// toAPIAgentDetail maps an agentDetailData (loaded via loadAgentDetail) into the
// JSON DTO shape — explicit fields, snake_case keys.
func (s *Server) toAPIAgentDetail(d *agentDetailData) map[string]any {
	runs := make([]map[string]any, 0, len(d.Runs))
	for _, r := range d.Runs {
		runs = append(runs, toAPIRun(r))
	}
	coreSkills := make([]map[string]any, 0, len(d.CoreSkills))
	for _, m := range d.CoreSkills {
		coreSkills = append(coreSkills, toAPICoreSkill(m))
	}
	allSkills := make([]map[string]any, 0, len(d.AllSkills))
	for _, sk := range d.AllSkills {
		allSkills = append(allSkills, toAPISkill(sk))
	}
	wsConns := make([]map[string]any, 0, len(d.WorkspaceConns))
	attachedConnIDs := make([]string, 0, len(d.AttachedConns))
	for _, cn := range d.WorkspaceConns {
		wsConns = append(wsConns, toAPIConnection(cn))
		if d.AttachedConns[cn.ID] {
			attachedConnIDs = append(attachedConnIDs, cn.ID)
		}
	}

	return map[string]any{
		"agent":                   s.toAPIAgent(d.Agent),
		"schedule":                toAPISchedule(d.Schedule),
		"runs":                    runs,
		"agent_md":                d.AgentMD,
		"state":                   d.State,
		"logs":                    orEmpty(d.Logs),
		"last_log":                d.LastLog,
		"attached_skills":         orEmpty(d.AttachedSkills),
		"core_skills":             coreSkills,
		"all_skills":              allSkills,
		"workspace_connections":   wsConns,
		"attached_connection_ids": attachedConnIDs,
		"connection_approval":     s.connectionApprovalModes(d.Agent.ID, attachedConnIDs),
		"missing_secrets":         orEmpty(d.MissingSecrets),
		"running":                 d.Running,
		"live_run":                d.LiveRun,
	}
}

// ── Handlers ─────────────────────────────────────────────────────────────────

// errAgentNotFound is a sentinel signaling the caller should respond 404
// not_found — kept distinct from db errors so it's never confused with the
// (usually nil) return value of writing a JSON response.
var errAgentNotFound = errors.New("agent not found")

// getOwnedAgent loads an agent and verifies it belongs to the workspace.
func (s *Server) getOwnedAgent(workspaceID, id string) (*db.Agent, error) {
	agent, err := s.db.GetAgent(id)
	if err != nil || agent.WorkspaceID != workspaceID {
		return nil, errAgentNotFound
	}
	return agent, nil
}

func (s *Server) apiListAgents(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	agents, err := s.db.ListAgents(u.ID)
	if err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	out := make([]apiAgent, 0, len(agents))
	for _, a := range agents {
		out = append(out, s.toAPIAgent(a))
	}
	var draft *db.AgentDraft
	if s.designFlow != nil {
		draft = s.designFlow.HasDraft(u.ID)
	}
	return c.JSON(http.StatusOK, map[string]any{"agents": out, "draft": toAPIAgentDraft(draft)})
}

func (s *Server) apiGetAgent(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	agent, err := s.getOwnedAgent(u.ID, c.Param("id"))
	if err != nil {
		return jsonErr(c, http.StatusNotFound, "not_found", "agent not found")
	}
	data := s.loadAgentDetail(c.Request().Context(), agent, u.ID)
	return c.JSON(http.StatusOK, s.toAPIAgentDetail(data))
}

func (s *Server) apiDeleteAgent(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	agent, err := s.getOwnedAgent(u.ID, c.Param("id"))
	if err != nil {
		return jsonErr(c, http.StatusNotFound, "not_found", "agent not found")
	}

	if err := s.db.DeleteAgent(agent.ID); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}

	// Remove the agent's directory from the user's vault so no orphaned files
	// linger (otherwise the deleted agent keeps showing up in the knowledge base).
	if dir := s.agentsDir(); dir != "" {
		_ = os.RemoveAll(agentdesigner.AgentDir(dir, u.ID, agent.ID))
	}
	// The run NOTES lived inside that directory, but their db-export sidecars do
	// not: they sit under .kb/db-export/agent_runs/ keyed by RUN id, so removing
	// the agent dir leaves them orphaned with no row and no agent to belong to.
	if s.vault != nil {
		_, _ = s.vault.Reflector().UnreflectAgentRuns(u.ID, agent.ID)
	}

	s.audit.Log(u.ID, "delete_agent", "agent:"+agent.ID, agent.Name, c.RealIP())
	return c.JSON(http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) apiRunAgent(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	agent, err := s.getOwnedAgent(u.ID, c.Param("id"))
	if err != nil {
		return jsonErr(c, http.StatusNotFound, "not_found", "agent not found")
	}

	s.audit.Log(u.ID, "run_agent", "agent:"+agent.ID, agent.Name, c.RealIP())

	if s.runner == nil {
		return jsonErr(c, http.StatusServiceUnavailable, "not_configured", "agent runner is not configured")
	}

	// Decrypt the user's stored master password the same way the scheduler does
	// for cron runs, so manual "Run Now" gets secret injection too.
	var masterPw string
	if u.EncryptedMasterPassword != "" {
		if pw, err := secrets.DecryptMasterPassword(u.EncryptedMasterPassword, s.systemKey); err == nil {
			masterPw = pw
		}
	}

	if !s.startManualRun(u.ID, agent, masterPw) {
		// A run for this agent is already in flight (manual or scheduled) — not
		// an error, just informational; the client should attach to the
		// existing SSE stream rather than show a failure.
		return c.JSON(http.StatusAccepted, map[string]any{"status": "already_running"})
	}
	return c.JSON(http.StatusAccepted, map[string]any{"status": "started"})
}

func (s *Server) apiSaveSchedule(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	agent, err := s.getOwnedAgent(u.ID, c.Param("id"))
	if err != nil {
		return jsonErr(c, http.StatusNotFound, "not_found", "agent not found")
	}

	var req struct {
		CronExpr string `json:"cron_expr"`
	}
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	cronExpr := strings.TrimSpace(req.CronExpr)
	if cronExpr == "" {
		return jsonErr(c, http.StatusBadRequest, "invalid_cron", "cron expression is required")
	}

	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	sched, parseErr := parser.Parse(cronExpr)
	if parseErr != nil {
		return jsonErr(c, http.StatusBadRequest, "invalid_cron", "invalid cron expression: "+parseErr.Error())
	}

	nextRun := sched.Next(time.Now())
	// Reuse existing schedule ID so ON CONFLICT(id) updates rather than inserts.
	existing, _ := s.db.GetScheduleForAgent(agent.ID)
	schedID := uuid.New().String()
	if existing != nil {
		schedID = existing.ID
	}
	row := &db.AgentSchedule{
		ID:          schedID,
		AgentID:     agent.ID,
		WorkspaceID: u.ID,
		CronExpr:    cronExpr,
		NextRunAt:   &nextRun,
		Enabled:     true,
	}
	if err := s.db.UpsertAgentSchedule(row); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "failed to save schedule: "+err.Error())
	}
	s.audit.Log(u.ID, "save_schedule", "agent:"+agent.ID, cronExpr, c.RealIP())
	return c.JSON(http.StatusOK, toAPISchedule(row))
}

func (s *Server) apiDeleteSchedule(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	agent, err := s.getOwnedAgent(u.ID, c.Param("id"))
	if err != nil {
		return jsonErr(c, http.StatusNotFound, "not_found", "agent not found")
	}
	_ = s.db.DeleteAgentSchedule(agent.ID)
	s.audit.Log(u.ID, "delete_schedule", "agent:"+agent.ID, "", c.RealIP())
	return c.JSON(http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) apiSaveAgentMD(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	agent, err := s.getOwnedAgent(u.ID, c.Param("id"))
	if err != nil {
		return jsonErr(c, http.StatusNotFound, "not_found", "agent not found")
	}

	var req struct {
		Content string `json:"content"`
	}
	if err := bindAPI(c, &req); err != nil {
		return err
	}

	if err := agentdesigner.CheckEthics(req.Content, ""); err != nil {
		return jsonErr(c, http.StatusBadRequest, "ethics_blocked", "AGENT.md failed safety check: "+err.Error())
	}

	dir := s.agentsDir()
	if err := os.WriteFile(agentdesigner.AgentDescPath(dir, u.ID, agent.ID), []byte(req.Content), 0o640); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "write failed: "+err.Error())
	}

	data := s.loadAgentDetail(c.Request().Context(), agent, u.ID)
	return c.JSON(http.StatusOK, s.toAPIAgentDetail(data))
}

func (s *Server) apiSaveAgentSkills(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	agent, err := s.getOwnedAgent(u.ID, c.Param("id"))
	if err != nil {
		return jsonErr(c, http.StatusNotFound, "not_found", "agent not found")
	}

	var req struct {
		SkillNames []string `json:"skill_names"`
	}
	if err := bindAPI(c, &req); err != nil {
		return err
	}

	// Allowed universe = core skill names ∪ the user's own skill names. Core
	// skills have no DB row, so the request carries names (not IDs); only names
	// can represent core skills. Drop anything not in the universe (defensive).
	coreNames := make(map[string]bool)
	for _, cs := range skilllibrary.LoadBundled() {
		coreNames[cs.Name] = true
	}
	userSkills, _ := s.db.ListSkills(u.ID)
	userByName := make(map[string]*db.Skill, len(userSkills))
	for _, sk := range userSkills {
		userByName[sk.Name] = sk
	}

	var valid []string
	seen := make(map[string]bool)
	for _, name := range req.SkillNames {
		if name == "" || seen[name] {
			continue
		}
		if !coreNames[name] && userByName[name] == nil {
			continue // unknown skill — ignore
		}
		seen[name] = true
		valid = append(valid, name)
	}

	// The agent_skills DB table is the single source of truth for an agent's
	// skills (core + user, by name). AGENT.md is only the LLM's instructions, so
	// we never touch it here.
	if err := s.db.SetAgentSkills(agent.ID, valid); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "failed to save skills: "+err.Error())
	}

	data := s.loadAgentDetail(c.Request().Context(), agent, u.ID)
	return c.JSON(http.StatusOK, s.toAPIAgentDetail(data))
}

func (s *Server) apiSaveAgentConnections(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	agent, err := s.getOwnedAgent(u.ID, c.Param("id"))
	if err != nil {
		return jsonErr(c, http.StatusNotFound, "not_found", "agent not found")
	}
	ctx := c.Request().Context()

	var req struct {
		ConnectionIDs []string `json:"connection_ids"`
	}
	if err := bindAPI(c, &req); err != nil {
		return err
	}

	// Only accept connection IDs that belong to this workspace.
	conns, _ := s.db.ListServiceConnections(ctx, u.ID)
	valid := make(map[string]bool, len(conns))
	for _, cn := range conns {
		valid[cn.ID] = true
	}
	var ids []string
	seen := make(map[string]bool)
	for _, cid := range req.ConnectionIDs {
		if cid != "" && valid[cid] && !seen[cid] {
			ids = append(ids, cid)
			seen[cid] = true
		}
	}
	if err := s.db.SetAgentConnections(ctx, agent.ID, ids); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "failed to save connections")
	}

	data := s.loadAgentDetail(ctx, agent, u.ID)
	return c.JSON(http.StatusOK, s.toAPIAgentDetail(data))
}
