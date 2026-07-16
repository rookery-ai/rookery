package web

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

func (s *Server) registerAuthAPI(g *echo.Group) {
	// Filled in Task 2. Session endpoint is needed by Task 1's middleware test:
	g.GET("/auth/session", s.apiAuthSession)
}

func (s *Server) apiAuthSession(c echo.Context) error {
	if _, ok := s.currentOwner(c); !ok {
		return c.JSON(http.StatusOK, map[string]any{"authenticated": false})
	}
	return c.JSON(http.StatusOK, map[string]any{"authenticated": true})
}
