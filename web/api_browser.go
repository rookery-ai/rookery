package web

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/rookery-ai/rookery/internal/browser"
	"github.com/rookery-ai/rookery/internal/db"
)

// registerBrowserRoutes wires the browser's control surface.
//
// Without this the grants are unreachable: SetAgentBrowserGrants has no other
// caller, so every agent would be permanently read-only and the acting tools —
// and the whole of the second design — would be dead code on every install.
func (s *Server) registerBrowserRoutes(g *echo.Group) {
	g.GET("/browser/status", s.apiBrowserStatus)
	g.PUT("/agents/:id/browser", s.apiSetAgentBrowserGrants)
}

// apiBrowserStatus reports whether this host can render pages at all.
//
// The SPA needs this to decide whether to show the grant controls: offering an
// owner a permission switch for a capability the server does not have would let
// them grant something that silently never happens, and then wonder why their
// agent cannot log in.
func (s *Server) apiBrowserStatus(c echo.Context) error {
	av := browser.Availability{Reason: "the browser subsystem is not wired on this server"}
	if s.browserMgr != nil {
		av = s.browserMgr.Available()
	}
	return c.JSON(http.StatusOK, map[string]any{
		"available": av.OK,
		// Empty when available. Carries the install command otherwise, so the
		// page can tell the owner what to run rather than just greying a control.
		"reason": av.Reason,
	})
}

// apiSetAgentBrowserGrants records the owner's two browser permissions for one
// agent.
func (s *Server) apiSetAgentBrowserGrants(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)

	agent, err := s.db.GetAgent(c.Param("id"))
	if err != nil || agent == nil || agent.WorkspaceID != w.ID {
		return jsonErr(c, http.StatusNotFound, "not_found", "agent not found")
	}

	// A pointer, so "not sent" is distinguishable from "sent as false" — a
	// client that omits the field must not silently revoke a permission the
	// owner granted.
	var req struct {
		Irreversible *bool `json:"irreversible"`
	}
	if err := bindAPI(c, &req); err != nil {
		return err
	}

	irreversible := agent.BrowserIrreversible
	if req.Irreversible != nil {
		irreversible = *req.Irreversible
	}

	if err := s.db.SetAgentBrowserGrant(agent.ID, irreversible); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	return c.JSON(http.StatusOK, map[string]any{
		"ok":           true,
		"irreversible": irreversible,
	})
}
