package web

import (
	"errors"
	"net/http"
	"time"

	"github.com/ilijad1/simple-agents/internal/auth"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/labstack/echo/v4"
)

type apiWorkspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Icon is a preset image slug the SPA resolves to a bundled graphic.
	// Empty means the UI falls back to the workspace name's initial.
	Icon       string    `json:"icon"`
	About      string    `json:"about"`
	NeedsSetup bool      `json:"needs_setup"`
	CreatedAt  time.Time `json:"created_at"`
}

func toAPIWorkspace(w *db.Workspace) apiWorkspace {
	return apiWorkspace{
		ID: w.ID, Name: w.Name, Icon: w.Icon, About: w.About,
		NeedsSetup: w.NeedsSetup, CreatedAt: w.CreatedAt,
	}
}

func (s *Server) registerAuthAPI(g *echo.Group) {
	g.GET("/auth/session", s.apiAuthSession)
	g.POST("/auth/login", s.apiLogin)
	g.POST("/auth/logout", s.apiLogout)
	g.POST("/auth/change-password", s.apiChangePassword, s.requireOwnerAPI)
}

func (s *Server) apiAuthSession(c echo.Context) error {
	o, ok := s.currentOwner(c)
	if !ok {
		return c.JSON(http.StatusOK, map[string]any{"authenticated": false})
	}
	out := map[string]any{
		"authenticated": true,
		"owner": map[string]any{
			"id": o.ID, "username": o.Username, "must_change_password": o.MustChangePassword,
		},
		"workspace": nil,
	}
	wss, _ := s.db.ListWorkspaces()
	list := make([]apiWorkspace, 0, len(wss))
	for _, w := range wss {
		list = append(list, toAPIWorkspace(w))
	}
	out["workspaces"] = list
	if w, ok := s.activeWorkspace(c); ok {
		out["workspace"] = toAPIWorkspace(w)
	}
	return c.JSON(http.StatusOK, out)
}

func (s *Server) apiLogin(c echo.Context) error {
	var req struct{ Username, Password string }
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	o, err := auth.Authenticate(s.db, req.Username, req.Password)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCreds) {
			return jsonErr(c, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
		}
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	if err := s.setOwnerSession(c, o.ID); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	s.audit.Log("", "login", "owner:"+o.ID, "", c.RealIP())
	return c.JSON(http.StatusOK, map[string]any{"ok": true, "must_change_password": o.MustChangePassword})
}

func (s *Server) apiLogout(c echo.Context) error {
	if o, ok := s.currentOwner(c); ok {
		s.audit.Log("", "logout", "owner:"+o.ID, "", c.RealIP())
	}
	_ = s.clearSession(c)
	return c.JSON(http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) apiChangePassword(c echo.Context) error {
	o := c.Get("owner").(*db.Owner)
	var req struct{ Password, Confirm string }
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	if len(req.Password) < 8 {
		return jsonErr(c, http.StatusBadRequest, "password_too_short", "password must be at least 8 characters")
	}
	if req.Password != req.Confirm {
		return jsonErr(c, http.StatusBadRequest, "password_mismatch", "passwords do not match")
	}
	if err := auth.ChangePassword(s.db, o.ID, req.Password); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	s.audit.Log("", "change_password", "owner:"+o.ID, "", c.RealIP())
	return c.JSON(http.StatusOK, map[string]any{"ok": true})
}
