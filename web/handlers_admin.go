package web

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/auth"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/labstack/echo/v4"
)

type adminUsersData struct {
	*pageData
	Users []*db.User
}

type permEntry struct {
	Name    string
	Granted bool
}

type adminUserDetailData struct {
	*pageData
	Target         *db.User
	Permissions    []string
	AllPermissions []permEntry
	AssignedCoder  *db.Coder
	AllCoders      []*db.Coder
}

var allPermissions = []string{"bash", "web-browser", "system-tools", "mcp-servers"}

type adminDashData struct {
	*pageData
	UserCount  int
	AgentCount int
	CoderCount int
	AuditLogs  []*db.AuditLog
}

func (s *Server) showAdminDashboard(c echo.Context) error {
	userCount, _ := s.db.CountUsers()
	agentCount, _ := s.db.CountAgents("")
	coderCount, _ := s.db.CountCoders()
	logs, _ := s.db.ListAuditLogs(20)
	return c.Render(http.StatusOK, "admin/dashboard.html", &adminDashData{
		pageData:   s.page(c, "Admin Dashboard"),
		UserCount:  userCount,
		AgentCount: agentCount,
		CoderCount: coderCount,
		AuditLogs:  logs,
	})
}

func (s *Server) showAdminUsers(c echo.Context) error {
	users, _ := s.db.ListUsers()
	return c.Render(http.StatusOK, "admin/users.html", &adminUsersData{
		pageData: s.page(c, "Manage Users"),
		Users:    users,
	})
}

func (s *Server) handleAdminCreateUser(c echo.Context) error {
	admin := c.Get("user").(*db.User)
	username := c.FormValue("username")

	if username == "" {
		p := s.page(c, "Manage Users")
		p.Error = "Username is required"
		users, _ := s.db.ListUsers()
		return c.Render(http.StatusBadRequest, "admin/users.html", &adminUsersData{pageData: p, Users: users})
	}

	_, tempPw, err := auth.CreateUser(s.db, username)
	if err != nil {
		p := s.page(c, "Manage Users")
		if err == auth.ErrUserExists {
			p.Error = "Username already taken"
		} else {
			p.Error = "Failed to create user: " + err.Error()
		}
		users, _ := s.db.ListUsers()
		return c.Render(http.StatusBadRequest, "admin/users.html", &adminUsersData{pageData: p, Users: users})
	}

	s.audit.Log(admin.ID, "create_user", "user:"+username, "", c.RealIP())

	p := s.page(c, "Manage Users")
	p.Success = "User '" + username + "' created. Temporary password: " + tempPw
	users, _ := s.db.ListUsers()
	return c.Render(http.StatusOK, "admin/users.html", &adminUsersData{pageData: p, Users: users})
}

func (s *Server) showAdminUser(c echo.Context) error {
	id := c.Param("id")
	target, err := s.db.GetUserByID(id)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "user not found")
	}
	perms, _ := s.db.ListPermissions(id)
	assignedCoder, _ := s.db.GetUserCoder(id)
	allCoders, _ := s.db.ListCoders()
	d := buildUserDetailData(s.page(c, "User: "+target.Username), target, perms)
	d.AssignedCoder = assignedCoder
	d.AllCoders = allCoders
	return c.Render(http.StatusOK, "admin/user_detail.html", d)
}

var validPermissions = []string{"bash", "web-browser", "system-tools", "mcp-servers"}

func (s *Server) handleAdminGrantPermission(c echo.Context) error {
	admin := c.Get("user").(*db.User)
	userID := c.Param("id")
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

	if err := s.db.GrantPermission(&db.UserPermission{
		ID:        uuid.New().String(),
		UserID:    userID,
		Permission: perm,
		GrantedBy: admin.ID,
	}); err != nil {
		return err
	}

	s.audit.Log(admin.ID, "grant_permission", "user:"+userID, perm, c.RealIP())
	return c.Redirect(http.StatusFound, "/admin/users/"+userID)
}

func (s *Server) handleAdminRevokePermission(c echo.Context) error {
	admin := c.Get("user").(*db.User)
	userID := c.Param("id")
	perm := c.Param("perm")

	if err := s.db.RevokePermission(userID, perm); err != nil {
		return err
	}

	s.audit.Log(admin.ID, "revoke_permission", "user:"+userID, perm, c.RealIP())
	return c.Redirect(http.StatusFound, "/admin/users/"+userID)
}

func (s *Server) handleAdminResetPassword(c echo.Context) error {
	admin := c.Get("user").(*db.User)
	userID := c.Param("id")

	target, err := s.db.GetUserByID(userID)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "user not found")
	}

	tempPw := auth.GenerateTempPassword()
	if err := auth.ChangePassword(s.db, userID, tempPw); err != nil {
		return err
	}

	// Force user to change password on next login
	if _, err := s.db.Exec(`UPDATE users SET must_change_password=1 WHERE id=?`, userID); err != nil {
		return err
	}

	s.audit.Log(admin.ID, "reset_password", "user:"+userID, "", c.RealIP())

	p := s.page(c, "User: "+target.Username)
	p.Success = "Password reset. New temporary password: " + tempPw
	perms, _ := s.db.ListPermissions(userID)
	assignedCoder, _ := s.db.GetUserCoder(userID)
	allCoders, _ := s.db.ListCoders()
	d := buildUserDetailData(p, target, perms)
	d.AssignedCoder = assignedCoder
	d.AllCoders = allCoders
	return c.Render(http.StatusOK, "admin/user_detail.html", d)
}

const systemUserID = "system"

type adminSettingsData struct {
	*pageData
	ClaudeBin    string
	CoderTimeout string
	FirejailBin  string
	AgentTimeout string
	MemoryMB     string
}

func (s *Server) loadAdminSettings() *adminSettingsData {
	get := func(key, fallback string) string {
		if v, err := s.db.GetSetting(systemUserID, key); err == nil && v != "" {
			return v
		}
		return fallback
	}
	return &adminSettingsData{
		ClaudeBin:    get("claude_bin", "claude"),
		CoderTimeout: get("coder_timeout", "120"),
		FirejailBin:  get("firejail_bin", "firejail"),
		AgentTimeout: get("agent_timeout", "300"),
		MemoryMB:     get("memory_mb", "256"),
	}
}

func (s *Server) showAdminSettings(c echo.Context) error {
	d := s.loadAdminSettings()
	d.pageData = s.page(c, "System Settings")
	return c.Render(http.StatusOK, "admin/settings.html", d)
}

func (s *Server) handleAdminSaveSettings(c echo.Context) error {
	admin := c.Get("user").(*db.User)
	fields := map[string]string{
		"claude_bin":    c.FormValue("claude_bin"),
		"coder_timeout": c.FormValue("coder_timeout"),
		"firejail_bin":  c.FormValue("firejail_bin"),
		"agent_timeout": c.FormValue("agent_timeout"),
		"memory_mb":     c.FormValue("memory_mb"),
	}

	for key, val := range fields {
		if val != "" {
			if err := s.db.SetSetting(systemUserID, key, val); err != nil {
				d := s.loadAdminSettings()
				d.pageData = s.page(c, "System Settings")
				d.pageData.Error = "Failed to save: " + err.Error()
				return c.Render(http.StatusInternalServerError, "admin/settings.html", d)
			}
		}
	}

	s.audit.Log(admin.ID, "update_system_settings", "system", "", c.RealIP())
	d := s.loadAdminSettings()
	d.pageData = s.page(c, "System Settings")
	d.pageData.Success = "Settings saved. Restart the server for binary path changes to take effect."
	return c.Render(http.StatusOK, "admin/settings.html", d)
}

func buildUserDetailData(p *pageData, target *db.User, perms []string) *adminUserDetailData {
	granted := make(map[string]bool, len(perms))
	for _, perm := range perms {
		granted[perm] = true
	}
	entries := make([]permEntry, len(allPermissions))
	for i, name := range allPermissions {
		entries[i] = permEntry{Name: name, Granted: granted[name]}
	}
	return &adminUserDetailData{
		pageData:       p,
		Target:         target,
		Permissions:    perms,
		AllPermissions: entries,
	}
}

// ── Coder CRUD ─────────────────────────────────────────────────────────────

type adminCodersData struct {
	*pageData
	Coders []*db.Coder
}

type adminCoderDetailData struct {
	*pageData
	Coder *db.Coder
}

func (s *Server) showAdminCoders(c echo.Context) error {
	coders, _ := s.db.ListCoders()
	return c.Render(http.StatusOK, "admin/coders.html", &adminCodersData{
		pageData: s.page(c, "Coder Profiles"),
		Coders:   coders,
	})
}

func (s *Server) handleAdminCreateCoder(c echo.Context) error {
	admin := c.Get("user").(*db.User)
	name := c.FormValue("name")
	description := c.FormValue("description")
	claudeBin := c.FormValue("claude_bin")
	timeoutStr := c.FormValue("timeout_s")

	if name == "" {
		p := s.page(c, "Coder Profiles")
		p.Error = "Name is required"
		coders, _ := s.db.ListCoders()
		return c.Render(http.StatusBadRequest, "admin/coders.html", &adminCodersData{pageData: p, Coders: coders})
	}
	if claudeBin == "" {
		claudeBin = "claude"
	}
	timeoutS := 120
	if timeoutStr != "" {
		if v, err := strconv.Atoi(timeoutStr); err == nil && v > 0 {
			timeoutS = v
		}
	}

	coderObj := &db.Coder{
		ID:          uuid.New().String(),
		Name:        name,
		Description: description,
		ClaudeBin:   claudeBin,
		TimeoutS:    timeoutS,
	}
	if err := s.db.CreateCoder(coderObj); err != nil {
		p := s.page(c, "Coder Profiles")
		p.Error = "Failed to create coder: " + err.Error()
		coders, _ := s.db.ListCoders()
		return c.Render(http.StatusInternalServerError, "admin/coders.html", &adminCodersData{pageData: p, Coders: coders})
	}

	s.audit.Log(admin.ID, "create_coder", "coder:"+coderObj.ID, name, c.RealIP())
	return c.Redirect(http.StatusFound, "/admin/coders/"+coderObj.ID)
}

func (s *Server) showAdminCoder(c echo.Context) error {
	id := c.Param("id")
	coderObj, err := s.db.GetCoder(id)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "coder not found")
	}
	return c.Render(http.StatusOK, "admin/coder_detail.html", &adminCoderDetailData{
		pageData: s.page(c, "Edit Coder: "+coderObj.Name),
		Coder:    coderObj,
	})
}

func (s *Server) handleAdminUpdateCoder(c echo.Context) error {
	admin := c.Get("user").(*db.User)
	id := c.Param("id")
	coderObj, err := s.db.GetCoder(id)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "coder not found")
	}

	if name := c.FormValue("name"); name != "" {
		coderObj.Name = name
	}
	coderObj.Description = c.FormValue("description")
	if bin := c.FormValue("claude_bin"); bin != "" {
		coderObj.ClaudeBin = bin
	}
	if v, err := strconv.Atoi(c.FormValue("timeout_s")); err == nil && v > 0 {
		coderObj.TimeoutS = v
	}

	if err := s.db.UpdateCoder(coderObj); err != nil {
		p := s.page(c, "Edit Coder: "+coderObj.Name)
		p.Error = "Failed to update: " + err.Error()
		return c.Render(http.StatusInternalServerError, "admin/coder_detail.html", &adminCoderDetailData{pageData: p, Coder: coderObj})
	}

	s.audit.Log(admin.ID, "update_coder", "coder:"+id, coderObj.Name, c.RealIP())
	p := s.page(c, "Edit Coder: "+coderObj.Name)
	p.Success = "Coder profile updated."
	return c.Render(http.StatusOK, "admin/coder_detail.html", &adminCoderDetailData{pageData: p, Coder: coderObj})
}

func (s *Server) handleAdminDeleteCoder(c echo.Context) error {
	admin := c.Get("user").(*db.User)
	id := c.Param("id")
	if err := s.db.DeleteCoder(id); err != nil {
		return err
	}
	s.audit.Log(admin.ID, "delete_coder", "coder:"+id, "", c.RealIP())
	return c.Redirect(http.StatusFound, "/admin/coders")
}

// ── Coder assignment ────────────────────────────────────────────────────────

func (s *Server) handleAdminAssignCoder(c echo.Context) error {
	admin := c.Get("user").(*db.User)
	userID := c.Param("id")
	coderID := c.FormValue("coder_id")

	if err := s.db.AssignUserCoder(userID, coderID); err != nil {
		return err
	}
	s.audit.Log(admin.ID, "assign_coder", "user:"+userID, "coder:"+coderID, c.RealIP())
	return c.Redirect(http.StatusFound, "/admin/users/"+userID)
}

func (s *Server) handleAdminUnassignCoder(c echo.Context) error {
	admin := c.Get("user").(*db.User)
	userID := c.Param("id")

	if err := s.db.UnassignUserCoder(userID); err != nil {
		return err
	}
	s.audit.Log(admin.ID, "unassign_coder", "user:"+userID, "", c.RealIP())
	return c.Redirect(http.StatusFound, "/admin/users/"+userID)
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
