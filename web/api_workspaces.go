package web

import (
	"net/http"
	"strconv"
	"time"

	"github.com/ilijad1/simple-agents/internal/auth"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/labstack/echo/v4"
)

// registerWorkspacesAPI registers the owner-gated workspace + admin endpoints on
// the given group (already guarded by requireOwnerAPI). Direct JSON ports of the
// template handlers in web/handlers_admin.go.
func (s *Server) registerWorkspacesAPI(g *echo.Group) {
	g.GET("/workspaces", s.apiListWorkspaces)
	g.POST("/workspaces", s.apiCreateWorkspace)
	g.POST("/workspaces/leave", s.apiLeaveWorkspace)
	g.POST("/workspaces/:id/enter", s.apiEnterWorkspace)
	g.DELETE("/workspaces/:id", s.apiDeleteWorkspace)

	g.GET("/admin/overview", s.apiAdminOverview)
	g.GET("/admin/audit", s.apiAdminAudit)
	g.GET("/admin/settings", s.apiAdminGetSettings)
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

func (s *Server) apiAdminAudit(c echo.Context) error {
	limit := 100
	if v := c.QueryParam("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	logs, err := s.db.ListAuditLogs(limit)
	if err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	out := make([]apiAuditLog, 0, len(logs))
	for _, l := range logs {
		out = append(out, toAPIAuditLog(l))
	}
	return c.JSON(http.StatusOK, map[string]any{"logs": out})
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
type apiAdminSettings struct {
	SandboxOn     bool `json:"sandbox_on"`
	LandlockReady bool `json:"landlock_ready"`
}

func (s *Server) apiLoadAdminSettings() apiAdminSettings {
	d := s.loadAdminSettings()
	return apiAdminSettings{
		SandboxOn:     d.SandboxOn,
		LandlockReady: d.LandlockReady,
	}
}

func (s *Server) apiAdminGetSettings(c echo.Context) error {
	return c.JSON(http.StatusOK, s.apiLoadAdminSettings())
}
