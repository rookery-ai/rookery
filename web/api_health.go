package web

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/rookery-ai/rookery/internal/health"
)

// apiHealthz serves the unauthenticated capability report. It sits outside
// /api/v1 deliberately: it is infrastructure (container HEALTHCHECK, CI smoke
// test, operator triage), not part of the application API, and it must answer
// before any workspace has been entered.
//
// It discloses version, commit and tool PRESENCE to anyone who can reach the
// port. That is accepted — the app binds a LAN or loopback interface by default
// — but it must never grow to include paths or configuration values.
func (s *Server) apiHealthz(c echo.Context) error {
	return c.JSON(http.StatusOK, health.Detect(s.sandboxEnabled(), s.coderMode()))
}
