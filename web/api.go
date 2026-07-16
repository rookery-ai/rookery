package web

import (
	"net/http"
	"strings"

	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/labstack/echo/v4"
)

// apiErrBody is the uniform error envelope: {"error":{"code","message"}}.
type apiErrBody struct {
	Error apiErrDetail `json:"error"`
}
type apiErrDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func jsonErr(c echo.Context, status int, code, msg string) error {
	return c.JSON(status, apiErrBody{Error: apiErrDetail{Code: code, Message: msg}})
}

// bindAPI binds a JSON request body, translating bind failures to the envelope.
func bindAPI(c echo.Context, v any) error {
	if err := c.Bind(v); err != nil {
		return jsonErr(c, http.StatusBadRequest, "invalid_request", "malformed JSON body")
	}
	return nil
}

// requireOwnerAPI is requireOwner with JSON responses instead of redirects.
func (s *Server) requireOwnerAPI(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		o, ok := s.currentOwner(c)
		if !ok {
			return jsonErr(c, http.StatusUnauthorized, "not_authenticated", "log in first")
		}
		if o.MustChangePassword && c.Path() != "/api/v1/auth/change-password" {
			return jsonErr(c, http.StatusForbidden, "must_change_password", "password change required")
		}
		c.Set("owner", o)
		return next(c)
	}
}

// requireActiveWorkspaceAPI is requireActiveWorkspace with a JSON 403.
func (s *Server) requireActiveWorkspaceAPI(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		w, ok := s.activeWorkspace(c)
		if !ok {
			return jsonErr(c, http.StatusForbidden, "no_workspace", "enter a workspace first")
		}
		c.Set("workspace", w)
		return next(c)
	}
}

// requireSetupCompleteAPI is requireSetupComplete with a JSON 403.
func (s *Server) requireSetupCompleteAPI(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		w := c.Get("workspace").(*db.Workspace)
		if w.NeedsSetup && !strings.HasPrefix(c.Path(), "/api/v1/setup") {
			return jsonErr(c, http.StatusForbidden, "needs_setup", "complete workspace setup first")
		}
		return next(c)
	}
}

// setupAPIRoutes registers the /api/v1 groups. Endpoint registrations are added
// group-by-group in api_*.go files' registration funcs, called from here.
func (s *Server) setupAPIRoutes() {
	api := s.echo.Group("/api/v1")

	// Public (no auth): session bootstrap + login.
	s.registerAuthAPI(api)

	// Owner-gated (no workspace needed): workspaces, admin, audit.
	owner := api.Group("", s.requireOwnerAPI)
	s.registerWorkspacesAPI(owner)

	// Workspace-gated: everything tenant-scoped.
	dash := api.Group("", s.requireOwnerAPI, s.requireActiveWorkspaceAPI, s.requireSetupCompleteAPI)
	s.registerAgentsAPI(dash)
	s.registerSkillsAPI(dash)
	s.registerSecretsAPI(dash)
	s.registerConnectorsAPI(dash)
}
