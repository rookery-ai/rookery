package web

import (
	"net/http"

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
}

var allPermissions = []string{"bash", "web-browser", "system-tools", "mcp-servers"}

type adminDashData struct {
	*pageData
	UserCount  int
	AgentCount int
	AuditLogs  []*db.AuditLog
}

func (s *Server) showAdminDashboard(c echo.Context) error {
	userCount, _ := s.db.CountUsers()
	agentCount, _ := s.db.CountAgents("")
	logs, _ := s.db.ListAuditLogs(20)
	return c.Render(http.StatusOK, "admin/dashboard.html", &adminDashData{
		pageData:   s.page(c, "Admin Dashboard"),
		UserCount:  userCount,
		AgentCount: agentCount,
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
	return c.Render(http.StatusOK, "admin/user_detail.html", buildUserDetailData(s.page(c, "User: "+target.Username), target, perms))
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

	// Generate a new temp password
	_, tempPw, err := auth.CreateUser(s.db, "__temp_"+target.Username)
	if err != nil {
		// Fall through to direct reset
		tempPw = "reset-error"
	}

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
	return c.Render(http.StatusOK, "admin/user_detail.html", buildUserDetailData(p, target, perms))
}

func (s *Server) showAdminSettings(c echo.Context) error {
	return c.Render(http.StatusOK, "admin/settings.html", s.page(c, "System Settings"))
}

func (s *Server) handleAdminSaveSettings(c echo.Context) error {
	p := s.page(c, "System Settings")
	p.Success = "Settings saved"
	return c.Render(http.StatusOK, "admin/settings.html", p)
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
