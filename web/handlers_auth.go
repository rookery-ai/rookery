package web

import (
	"errors"
	"net/http"

	"github.com/ilijad1/simple-agents/internal/auth"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/labstack/echo/v4"
)

func (s *Server) showLogin(c echo.Context) error {
	_, ok := s.currentUser(c)
	if ok {
		return c.Redirect(http.StatusFound, "/dashboard")
	}
	return c.Render(http.StatusOK, "auth/login.html", s.page(c, "Login"))
}

func (s *Server) handleLogin(c echo.Context) error {
	username := c.FormValue("username")
	password := c.FormValue("password")

	u, err := auth.Authenticate(s.db, username, password)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCreds) {
			p := s.page(c, "Login")
			p.Error = "Invalid username or password"
			return c.Render(http.StatusUnauthorized, "auth/login.html", p)
		}
		return err
	}

	if err := s.setSession(c, u.ID); err != nil {
		return err
	}

	s.audit.Log(u.ID, "login", "user:"+u.ID, "", c.RealIP())

	if u.MustChangePassword {
		return c.Redirect(http.StatusFound, "/change-password")
	}
	if u.NeedsSetup {
		return c.Redirect(http.StatusFound, "/setup")
	}
	return c.Redirect(http.StatusFound, "/dashboard")
}

func (s *Server) handleLogout(c echo.Context) error {
	if u, ok := s.currentUser(c); ok {
		s.audit.Log(u.ID, "logout", "user:"+u.ID, "", c.RealIP())
	}
	_ = s.clearSession(c)
	return c.Redirect(http.StatusFound, "/login")
}

func (s *Server) showChangePassword(c echo.Context) error {
	return c.Render(http.StatusOK, "auth/change_password.html", s.page(c, "Change Password"))
}

func (s *Server) handleChangePassword(c echo.Context) error {
	u := c.Get("user").(*db.User)
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

	if err := auth.ChangePassword(s.db, u.ID, newPw); err != nil {
		return err
	}

	s.audit.Log(u.ID, "change_password", "user:"+u.ID, "", c.RealIP())

	if u.NeedsSetup {
		return c.Redirect(http.StatusFound, "/setup")
	}
	return c.Redirect(http.StatusFound, "/dashboard")
}
