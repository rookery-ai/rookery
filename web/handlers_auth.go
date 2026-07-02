package web

import (
	"errors"
	"net/http"

	"github.com/ilijad1/simple-agents/internal/auth"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/labstack/echo/v4"
)

func (s *Server) showLogin(c echo.Context) error {
	if _, ok := s.currentOwner(c); ok {
		return c.Redirect(http.StatusFound, "/admin")
	}
	return c.Render(http.StatusOK, "auth/login.html", s.page(c, "Login"))
}

func (s *Server) handleLogin(c echo.Context) error {
	username := c.FormValue("username")
	password := c.FormValue("password")

	o, err := auth.Authenticate(s.db, username, password)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCreds) {
			p := s.page(c, "Login")
			p.Error = "Invalid username or password"
			return c.Render(http.StatusUnauthorized, "auth/login.html", p)
		}
		return err
	}

	if err := s.setOwnerSession(c, o.ID); err != nil {
		return err
	}

	s.audit.Log("", "login", "owner:"+o.ID, "", c.RealIP())

	if o.MustChangePassword {
		return c.Redirect(http.StatusFound, "/change-password")
	}
	return c.Redirect(http.StatusFound, "/admin")
}

func (s *Server) handleLogout(c echo.Context) error {
	if o, ok := s.currentOwner(c); ok {
		s.audit.Log("", "logout", "owner:"+o.ID, "", c.RealIP())
	}
	_ = s.clearSession(c)
	return c.Redirect(http.StatusFound, "/login")
}

func (s *Server) showChangePassword(c echo.Context) error {
	return c.Render(http.StatusOK, "auth/change_password.html", s.page(c, "Change Password"))
}

func (s *Server) handleChangePassword(c echo.Context) error {
	o := c.Get("owner").(*db.Owner)
	newPw := c.FormValue("password")
	confirm := c.FormValue("confirm")

	p := s.page(c, "Change Password")
	if len(newPw) < 8 {
		p.Error = "Password must be at least 8 characters"
		return c.Render(http.StatusBadRequest, "auth/change_password.html", p)
	}
	if newPw != confirm {
		p.Error = "Passwords do not match"
		return c.Render(http.StatusBadRequest, "auth/change_password.html", p)
	}

	if err := auth.ChangePassword(s.db, o.ID, newPw); err != nil {
		return err
	}

	s.audit.Log("", "change_password", "owner:"+o.ID, "", c.RealIP())

	return c.Redirect(http.StatusFound, "/admin")
}
