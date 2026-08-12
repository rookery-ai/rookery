package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/rookery-ai/rookery/internal/auth"
	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/health"
)

// registerWorkspacesAPI registers the owner-gated workspace + admin endpoints on
// the given group (already guarded by requireOwnerAPI). Direct JSON ports of the
// template handlers in web/handlers_admin.go.
// registerWorkspacesAPI splits its routes across two groups.
//
// g is owner-gated only: listing is already in the session payload, and
// entering already demands that workspace's own master password — re-asking for
// the owner password on every switch would be punitive. Both must stay open or
// the verification gate becomes inescapable (see
// TestOwnerGateLeavesEscapeHatchesOpen).
//
// verified additionally requires a fresh owner-password confirmation: deleting a
// workspace destroys a tenant, and the admin routes are the install's settings.
//
// Creating is on verified too. It reads as additive and reversible, which is why
// it originally sat on g — but a workspace is a TENANT, and anyone holding an
// unattended owner session could mint one. Delete was gated from the start; the
// asymmetry predates this gate rather than expressing anything.
func (s *Server) registerWorkspacesAPI(g, verified *echo.Group) {
	g.GET("/workspaces", s.apiListWorkspaces)
	g.POST("/workspaces/leave", s.apiLeaveWorkspace)
	g.POST("/workspaces/:id/enter", s.apiEnterWorkspace)

	verified.POST("/workspaces", s.apiCreateWorkspace)
	verified.DELETE("/workspaces/:id", s.apiDeleteWorkspace)

	verified.GET("/admin/overview", s.apiAdminOverview)
	verified.GET("/admin/audit", s.apiAdminAudit)
	verified.GET("/admin/settings", s.apiAdminGetSettings)
	verified.GET("/admin/public-url", s.apiPublicURLState)
	verified.PUT("/admin/public-url", s.apiSavePublicURL)
	verified.POST("/admin/public-url/test", s.apiTestPublicURL)
}

// ── Workspace lifecycle ──────────────────────────────────────────────────────

func (s *Server) apiListWorkspaces(c echo.Context) error {
	workspaces, err := s.db.ListWorkspaces()
	if err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	out := make([]apiWorkspace, 0, len(workspaces))
	for _, w := range workspaces {
		out = append(out, toAPIWorkspace(w))
	}
	return c.JSON(http.StatusOK, map[string]any{"workspaces": out})
}

func (s *Server) apiCreateWorkspace(c echo.Context) error {
	var req struct{ Name, About string }
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	if req.Name == "" {
		return jsonErr(c, http.StatusBadRequest, "missing_field", "workspace name is required")
	}

	w, err := auth.CreateWorkspace(s.db, req.Name, req.About)
	if err != nil {
		if err == auth.ErrWorkspaceExists {
			return jsonErr(c, http.StatusConflict, "workspace_exists", "a workspace with that name already exists")
		}
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}

	if o, ok := s.currentOwner(c); ok {
		s.audit.Log(w.ID, "create_workspace", "workspace:"+w.ID, "owner:"+o.ID, c.RealIP())
	}

	// The About text the owner just typed is what agents and chat are told this
	// workspace is for, and memory/ABOUT.md is where they read it from.
	s.seedIdentityFiles(w.ID, "create_workspace")

	// A newly created workspace has no master password yet, so it can't go through
	// the enter gate — set it active straight away (mirrors handleAdminCreateWorkspace).
	if err := s.setActiveWorkspace(c, w.ID); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	return c.JSON(http.StatusCreated, toAPIWorkspace(w))
}

func (s *Server) apiEnterWorkspace(c echo.Context) error {
	id := c.Param("id")
	w, err := s.db.GetWorkspaceByID(id)
	if err != nil {
		return jsonErr(c, http.StatusNotFound, "not_found", "workspace not found")
	}

	if w.NeedsSetup {
		if err := s.setActiveWorkspace(c, w.ID); err != nil {
			return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
		}
		return c.JSON(http.StatusOK, map[string]any{"ok": true, "needs_setup": w.NeedsSetup})
	}

	var req struct {
		MasterPassword string `json:"master_password"`
	}
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	if !s.verifyWorkspaceMasterPassword(w, req.MasterPassword) {
		return jsonErr(c, http.StatusUnauthorized, "wrong_master_password", "incorrect master password for workspace '"+w.Name+"'")
	}

	if err := s.setActiveWorkspace(c, w.ID); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	s.audit.Log(w.ID, "enter_workspace", "workspace:"+w.ID, "", c.RealIP())
	return c.JSON(http.StatusOK, map[string]any{"ok": true, "needs_setup": w.NeedsSetup})
}

func (s *Server) apiLeaveWorkspace(c echo.Context) error {
	_ = s.clearActiveWorkspace(c)
	return c.JSON(http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) apiDeleteWorkspace(c echo.Context) error {
	id := c.Param("id")
	if err := s.db.DeleteWorkspace(id); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	// If the deleted workspace was active, leave it.
	if w, ok := s.activeWorkspace(c); ok && w.ID == id {
		_ = s.clearActiveWorkspace(c)
	}
	s.audit.Log("", "delete_workspace", "workspace:"+id, "", c.RealIP())
	return c.JSON(http.StatusOK, map[string]any{"ok": true})
}

// ── Owner/admin ───────────────────────────────────────────────────────────────

type apiAuditLog struct {
	WorkspaceID string    `json:"workspace_id"`
	Action      string    `json:"action"`
	Target      string    `json:"target"`
	Detail      string    `json:"detail"`
	IP          string    `json:"ip"`
	CreatedAt   time.Time `json:"created_at"`
}

func toAPIAuditLog(a *db.AuditLog) apiAuditLog {
	wsID := ""
	if a.WorkspaceID != nil {
		wsID = *a.WorkspaceID
	}
	return apiAuditLog{
		WorkspaceID: wsID,
		Action:      a.Action,
		Target:      a.Target,
		Detail:      a.Detail,
		IP:          a.IPAddress,
		CreatedAt:   a.CreatedAt,
	}
}

func (s *Server) apiAdminOverview(c echo.Context) error {
	workspaces, _ := s.db.ListWorkspaces()
	agentCount, _ := s.db.CountAgents("")
	logs, _ := s.db.ListAuditLogs(20)
	out := make([]apiAuditLog, 0, len(logs))
	for _, l := range logs {
		out = append(out, toAPIAuditLog(l))
	}
	return c.JSON(http.StatusOK, map[string]any{
		"workspace_count": len(workspaces),
		"agent_count":     agentCount,
		"recent_audit":    out,
	})
}

// apiAdminAudit serves the audit log, optionally filtered.
//
// Filters are applied in SQL, not over the returned page: narrowing an
// already-truncated list of the most recent 100 events would report "no
// matches" for something that merely happened 101 events ago — an answer that
// looks authoritative and is wrong.
//
// The response also carries the distinct action values so the UI can offer a
// picker rather than expecting the operator to recall exact action strings.
func (s *Server) apiAdminAudit(c echo.Context) error {
	f := db.AuditLogFilter{
		WorkspaceID: c.QueryParam("workspace_id"),
		Action:      c.QueryParam("action"),
		Query:       strings.TrimSpace(c.QueryParam("q")),
		Limit:       100,
	}
	if v := c.QueryParam("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			f.Limit = n
		}
	}
	// "since" is a window in days, which is how the filter is actually used
	// ("last 7 days"); an absolute timestamp would push timezone handling into
	// the client for no gain.
	if v := c.QueryParam("since_days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			f.Since = time.Now().AddDate(0, 0, -n)
		}
	}

	logs, err := s.db.ListAuditLogsFiltered(f)
	if err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	actions, err := s.db.DistinctAuditActions()
	if err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	out := make([]apiAuditLog, 0, len(logs))
	for _, l := range logs {
		out = append(out, toAPIAuditLog(l))
	}
	return c.JSON(http.StatusOK, map[string]any{
		"logs":    out,
		"actions": orEmpty(actions),
	})
}

// apiAdminSettings is read-only status, not configuration.
//
// This payload used to also carry claude_bin / coder_timeout / agent_timeout /
// memory_mb, persisted into system_settings by a PUT. Nothing ever read them
// back: the coder binary and timeout come from config.yaml (cfg.Coder), the
// per-workspace timeout from workspaces.coder_timeout_s, and the sandbox
// memory cap from cfg.Sandbox.DefaultMemoryMB. They were a form that appeared
// to configure the system but did not, so they were removed rather than wired
// up — inventing runtime meaning for them was never asked for.
// It now carries the whole health.Report as well. The owner's System status
// page reported two booleans, of which only one was ever green, so it read as
// "Landlock, and nothing else" — while /healthz already computed the version,
// commit, Landlock ABI, coder mode and host-tool presence, and nothing showed
// them to the operator. Warnings matter most: without python3 the agent-tool
// AST guardrail silently self-skips, and only /healthz said so.
//
// Booleans only, never paths — the same disclosure rule /healthz follows.
type apiAdminSettings struct {
	SandboxOn     bool `json:"sandbox_on"`
	LandlockReady bool `json:"landlock_ready"`

	Version     string       `json:"version"`
	Commit      string       `json:"commit"`
	LandlockABI int          `json:"landlock_abi"`
	CoderMode   string       `json:"coder_mode"`
	Tools       health.Tools `json:"tools"`
	Warnings    []string     `json:"warnings"`
}

func (s *Server) apiLoadAdminSettings() apiAdminSettings {
	d := s.loadAdminSettings()
	rep := health.Detect(s.sandboxEnabled(), s.coderMode())
	return apiAdminSettings{
		SandboxOn:     d.SandboxOn,
		LandlockReady: d.LandlockReady,
		Version:       rep.Version,
		Commit:        rep.Commit,
		LandlockABI:   rep.Sandbox.ABI,
		CoderMode:     rep.CoderMode,
		Tools:         rep.Tools,
		// Never nil: a nil slice marshals to JSON null, and a TypeScript
		// default parameter substitutes only for undefined — so `warnings.map`
		// would throw and unmount the whole settings route.
		Warnings: orEmpty(rep.Warnings()),
	}
}

func (s *Server) apiAdminGetSettings(c echo.Context) error {
	return c.JSON(http.StatusOK, s.apiLoadAdminSettings())
}
