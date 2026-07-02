package web

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/auth"
	"github.com/ilijad1/simple-agents/internal/coder"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/sandbox"
	"github.com/ilijad1/simple-agents/internal/secrets"
	"github.com/labstack/echo/v4"
)

// ── Owner dashboard ──────────────────────────────────────────────────────────

type adminDashData struct {
	*pageData
	WorkspaceCount int
	AgentCount     int
	Workspaces     []*db.Workspace
	AuditLogs      []*db.AuditLog
}

func (s *Server) showAdminDashboard(c echo.Context) error {
	workspaces, _ := s.db.ListWorkspaces()
	agentCount, _ := s.db.CountAgents("")
	logs, _ := s.db.ListAuditLogs(20)
	return c.Render(http.StatusOK, "admin/dashboard.html", &adminDashData{
		pageData:       s.page(c, "Workspaces"),
		WorkspaceCount: len(workspaces),
		AgentCount:     agentCount,
		Workspaces:     workspaces,
		AuditLogs:      logs,
	})
}

// ── Workspace management ─────────────────────────────────────────────────────

type adminWorkspacesData struct {
	*pageData
	Workspaces []*db.Workspace
}

func (s *Server) showAdminWorkspaces(c echo.Context) error {
	workspaces, _ := s.db.ListWorkspaces()
	return c.Render(http.StatusOK, "admin/workspaces.html", &adminWorkspacesData{
		pageData:   s.page(c, "Workspaces"),
		Workspaces: workspaces,
	})
}

func (s *Server) handleAdminCreateWorkspace(c echo.Context) error {
	name := c.FormValue("name")
	about := c.FormValue("about")

	if name == "" {
		workspaces, _ := s.db.ListWorkspaces()
		p := s.page(c, "Workspaces")
		p.Error = "Workspace name is required"
		return c.Render(http.StatusBadRequest, "admin/workspaces.html", &adminWorkspacesData{pageData: p, Workspaces: workspaces})
	}

	w, err := auth.CreateWorkspace(s.db, name, about)
	if err != nil {
		workspaces, _ := s.db.ListWorkspaces()
		p := s.page(c, "Workspaces")
		if err == auth.ErrWorkspaceExists {
			p.Error = "A workspace with that name already exists"
		} else {
			p.Error = "Failed to create workspace: " + err.Error()
		}
		return c.Render(http.StatusBadRequest, "admin/workspaces.html", &adminWorkspacesData{pageData: p, Workspaces: workspaces})
	}

	if o, ok := s.currentOwner(c); ok {
		s.audit.Log(w.ID, "create_workspace", "workspace:"+w.ID, "owner:"+o.ID, c.RealIP())
	}

	// A newly created workspace has no master password yet, so it can't go through
	// the enter gate — take the owner straight into the onboarding wizard.
	if err := s.setActiveWorkspace(c, w.ID); err != nil {
		return err
	}
	return c.Redirect(http.StatusFound, "/dashboard/setup")
}

type permEntry struct {
	Name    string
	Granted bool
}

type adminWorkspaceDetailData struct {
	*pageData
	Target         *db.Workspace
	Permissions    []string
	AllPermissions []permEntry
	DetectedCoders []coder.Installed
}

var allPermissions = []string{"bash", "web-browser", "system-tools", "mcp-servers"}
var validPermissions = allPermissions

func (s *Server) showAdminWorkspace(c echo.Context) error {
	id := c.Param("id")
	target, err := s.db.GetWorkspaceByID(id)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "workspace not found")
	}
	perms, _ := s.db.ListPermissions(id)
	return c.Render(http.StatusOK, "admin/workspace_detail.html",
		s.buildWorkspaceDetailData(s.page(c, "Workspace: "+target.Name), target, perms))
}

func (s *Server) buildWorkspaceDetailData(p *pageData, target *db.Workspace, perms []string) *adminWorkspaceDetailData {
	granted := make(map[string]bool, len(perms))
	for _, perm := range perms {
		granted[perm] = true
	}
	entries := make([]permEntry, len(allPermissions))
	for i, name := range allPermissions {
		entries[i] = permEntry{Name: name, Granted: granted[name]}
	}
	return &adminWorkspaceDetailData{
		pageData:       p,
		Target:         target,
		Permissions:    perms,
		AllPermissions: entries,
		DetectedCoders: coder.DetectInstalled(),
	}
}

// handleEnterWorkspace verifies the workspace master password and enters it. A
// not-yet-set-up workspace has no master password, so it goes straight to the
// onboarding wizard. Re-entering (switching) always re-prompts for the password.
func (s *Server) handleEnterWorkspace(c echo.Context) error {
	id := c.Param("id")
	w, err := s.db.GetWorkspaceByID(id)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "workspace not found")
	}

	if w.NeedsSetup {
		if err := s.setActiveWorkspace(c, w.ID); err != nil {
			return err
		}
		return c.Redirect(http.StatusFound, "/dashboard/setup")
	}

	password := c.FormValue("master_password")
	if !s.verifyWorkspaceMasterPassword(w, password) {
		workspaces, _ := s.db.ListWorkspaces()
		p := s.page(c, "Workspaces")
		p.Error = "Incorrect master password for workspace '" + w.Name + "'"
		return c.Render(http.StatusUnauthorized, "admin/workspaces.html", &adminWorkspacesData{pageData: p, Workspaces: workspaces})
	}

	if err := s.setActiveWorkspace(c, w.ID); err != nil {
		return err
	}
	s.audit.Log(w.ID, "enter_workspace", "workspace:"+w.ID, "", c.RealIP())
	return c.Redirect(http.StatusFound, "/dashboard")
}

// handleLeaveWorkspace leaves the active workspace (owner stays logged in).
func (s *Server) handleLeaveWorkspace(c echo.Context) error {
	_ = s.clearActiveWorkspace(c)
	return c.Redirect(http.StatusFound, "/admin")
}

func (s *Server) handleAdminDeleteWorkspace(c echo.Context) error {
	id := c.Param("id")
	if err := s.db.DeleteWorkspace(id); err != nil {
		return err
	}
	// If the deleted workspace was active, leave it.
	if w, ok := s.activeWorkspace(c); ok && w.ID == id {
		_ = s.clearActiveWorkspace(c)
	}
	s.audit.Log("", "delete_workspace", "workspace:"+id, "", c.RealIP())
	return c.Redirect(http.StatusFound, "/admin/workspaces")
}

// verifyWorkspaceMasterPassword decrypts the stored (system-key encrypted) master
// password and compares it to the supplied one. The stored form must remain (the
// scheduler decrypts it for headless cron runs), so this is an access gate, not the
// encryption key itself.
func (s *Server) verifyWorkspaceMasterPassword(w *db.Workspace, password string) bool {
	if password == "" || w.EncryptedMasterPassword == "" {
		return false
	}
	stored, err := secrets.DecryptMasterPassword(w.EncryptedMasterPassword, s.systemKey)
	if err != nil {
		return false
	}
	return stored == password
}

// ── Workspace permissions ────────────────────────────────────────────────────

func (s *Server) handleAdminGrantPermission(c echo.Context) error {
	o := c.Get("owner").(*db.Owner)
	workspaceID := c.Param("id")
	perm := c.FormValue("permission")

	valid := false
	for _, p := range validPermissions {
		if p == perm {
			valid = true
			break
		}
	}
	if !valid {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid permission")
	}

	if err := s.db.GrantPermission(&db.WorkspacePermission{
		ID:          uuid.New().String(),
		WorkspaceID: workspaceID,
		Permission:  perm,
		GrantedBy:   o.ID,
	}); err != nil {
		return err
	}

	s.audit.Log(workspaceID, "grant_permission", "workspace:"+workspaceID, perm, c.RealIP())
	return c.Redirect(http.StatusFound, "/admin/workspaces/"+workspaceID)
}

func (s *Server) handleAdminRevokePermission(c echo.Context) error {
	workspaceID := c.Param("id")
	perm := c.Param("perm")

	if err := s.db.RevokePermission(workspaceID, perm); err != nil {
		return err
	}

	s.audit.Log(workspaceID, "revoke_permission", "workspace:"+workspaceID, perm, c.RealIP())
	return c.Redirect(http.StatusFound, "/admin/workspaces/"+workspaceID)
}

// ── System settings ──────────────────────────────────────────────────────────

type adminSettingsData struct {
	*pageData
	ClaudeBin     string
	CoderTimeout  string
	AgentTimeout  string
	MemoryMB      string
	SandboxOn     bool // sandbox enabled in config
	LandlockReady bool // kernel actually supports Landlock
}

func (s *Server) loadAdminSettings() *adminSettingsData {
	get := func(key, fallback string) string {
		if v, err := s.db.GetSystemSetting(key); err == nil && v != "" {
			return v
		}
		return fallback
	}
	return &adminSettingsData{
		ClaudeBin:     get("claude_bin", "claude"),
		CoderTimeout:  get("coder_timeout", "120"),
		AgentTimeout:  get("agent_timeout", "300"),
		MemoryMB:      get("memory_mb", "256"),
		SandboxOn:     s.cfg.Sandbox.Enabled,
		LandlockReady: sandbox.Supported(),
	}
}

func (s *Server) showAdminSettings(c echo.Context) error {
	d := s.loadAdminSettings()
	d.pageData = s.page(c, "System Settings")
	return c.Render(http.StatusOK, "admin/settings.html", d)
}

func (s *Server) handleAdminSaveSettings(c echo.Context) error {
	fields := map[string]string{
		"claude_bin":    c.FormValue("claude_bin"),
		"coder_timeout": c.FormValue("coder_timeout"),
		"agent_timeout": c.FormValue("agent_timeout"),
		"memory_mb":     c.FormValue("memory_mb"),
	}

	for key, val := range fields {
		if val != "" {
			if err := s.db.SetSystemSetting(key, val); err != nil {
				d := s.loadAdminSettings()
				d.pageData = s.page(c, "System Settings")
				d.pageData.Error = "Failed to save: " + err.Error()
				return c.Render(http.StatusInternalServerError, "admin/settings.html", d)
			}
		}
	}

	s.audit.Log("", "update_system_settings", "system", "", c.RealIP())
	d := s.loadAdminSettings()
	d.pageData = s.page(c, "System Settings")
	d.pageData.Success = "Settings saved. Restart the server for binary path changes to take effect."
	return c.Render(http.StatusOK, "admin/settings.html", d)
}

func (s *Server) showAuditLog(c echo.Context) error {
	logs, _ := s.db.ListAuditLogs(100)
	type auditData struct {
		*pageData
		Logs []*db.AuditLog
	}
	return c.Render(http.StatusOK, "admin/audit.html", &auditData{
		pageData: s.page(c, "Audit Log"),
		Logs:     logs,
	})
}
