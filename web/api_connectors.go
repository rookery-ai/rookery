package web

import (
	"net/http"

	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/gateway"
	"github.com/labstack/echo/v4"
)

// registerConnectorsAPI registers the JSON chat-app connector endpoints on the
// given group (already guarded by requireOwnerAPI + requireActiveWorkspaceAPI
// + requireSetupCompleteAPI). Business logic (validate/save/test) lives in
// web/handlers_connectors.go and is shared with the template handlers — this
// file only adapts it to the JSON envelope.
func (s *Server) registerConnectorsAPI(g *echo.Group) {
	g.GET("/connectors", s.apiListConnectors)
	g.POST("/connectors", s.apiSaveConnector)
	g.DELETE("/connectors/:platform", s.apiDeleteConnector)
	g.POST("/connectors/:platform/test", s.apiTestConnector)
}

// ── DTOs ─────────────────────────────────────────────────────────────────────

type apiConnectorField struct {
	Name   string `json:"name"`
	Label  string `json:"label"`
	Secret bool   `json:"secret"`
}

type apiConnectorPlatform struct {
	Platform   string              `json:"platform"`
	Label      string              `json:"label"`
	Blurb      string              `json:"blurb"`
	SetupSteps []string            `json:"setup_steps"`
	Fields     []apiConnectorField `json:"fields"`
	Connected  bool                `json:"connected"`
	Identity   string              `json:"identity"`
}

type apiConnectorListResponse struct {
	Platforms []apiConnectorPlatform `json:"platforms"`
}

type apiSaveConnectorRequest struct {
	Platform string            `json:"platform"`
	Values   map[string]string `json:"values"`
}

type apiSaveConnectorResponse struct {
	OK       bool   `json:"ok"`
	Identity string `json:"identity,omitempty"`
	Warning  string `json:"warning,omitempty"`
}

type apiTestConnectorResponse struct {
	OK       bool   `json:"ok"`
	Identity string `json:"identity,omitempty"`
	Error    string `json:"error,omitempty"`
}

// ── Handlers ──────────────────────────────────────────────────────────────────

// apiListConnectors lists every registered CredSpec platform, annotated with
// this workspace's connection state. GET /api/v1/connectors → {"platforms":[...]}
func (s *Server) apiListConnectors(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	return c.JSON(http.StatusOK, apiConnectorListResponse{Platforms: s.connectorPlatformList(u)})
}

// connectorPlatformList builds the CredSpec-driven platform catalog annotated
// with this workspace's connection state. Shared by apiListConnectors and the
// setup wizard's step-5 (chat app) payload (apiGetSetup) — the setup wizard
// runs while needs_setup is still true, when GET /api/v1/connectors itself is
// blocked by requireSetupCompleteAPI, so it needs this same data inline.
func (s *Server) connectorPlatformList(u *db.Workspace) []apiConnectorPlatform {
	specs := gateway.CredSpecs()
	out := make([]apiConnectorPlatform, 0, len(specs))
	for _, spec := range specs {
		fields := make([]apiConnectorField, 0, len(spec.Fields))
		for _, f := range spec.Fields {
			fields = append(fields, apiConnectorField{Name: f.Key, Label: f.Label, Secret: f.Secret})
		}

		entry := apiConnectorPlatform{
			Platform:   spec.Platform,
			Label:      spec.Label,
			Blurb:      spec.Blurb,
			SetupSteps: orEmpty(spec.SetupSteps),
			Fields:     fields,
		}

		if conn, err := s.db.GetPlatformConnection(u.ID, spec.Platform); err == nil {
			entry.Connected = conn.Active
			// Telegram is the only platform with a persisted bot identity
			// (stored by saveConnector as a workspace setting); other
			// adapters have no equivalent stored value, so identity stays
			// empty rather than making a live network call from a list
			// endpoint.
			if spec.Platform == "telegram" {
				if uname, _ := s.db.GetSetting(u.ID, "telegram_bot_username"); uname != "" {
					entry.Identity = uname
				}
			}
		}

		out = append(out, entry)
	}
	return out
}

// apiSaveConnector validates + saves a platform's credentials, reusing
// s.saveConnector (shared with the template handler). POST /api/v1/connectors
// {platform, values} → 200 {"ok":true,"identity":...} (+ "warning" if the bot
// failed to (re)start) or 400 invalid_credentials.
func (s *Server) apiSaveConnector(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)

	var req apiSaveConnectorRequest
	if err := bindAPI(c, &req); err != nil {
		return err
	}

	if req.Platform == "" {
		return jsonErr(c, http.StatusBadRequest, "missing_field", "platform is required")
	}
	if _, ok := gateway.CredSpecFor(req.Platform); !ok {
		return jsonErr(c, http.StatusNotFound, "not_found", "unknown platform: "+req.Platform)
	}

	values := req.Values
	if values == nil {
		values = map[string]string{}
	}

	identity, botStartErr, err := s.saveConnector(u.ID, req.Platform, values)
	if err != nil {
		return jsonErr(c, http.StatusBadRequest, "invalid_credentials", err.Error())
	}
	s.audit.Log(u.ID, "connect_platform", "platform:"+req.Platform, "", c.RealIP())

	resp := apiSaveConnectorResponse{OK: true, Identity: identity}
	if botStartErr != nil {
		resp.Warning = "Connector saved but bot failed to start: " + botStartErr.Error()
	}
	return c.JSON(http.StatusOK, resp)
}

// apiDeleteConnector disconnects a platform. DELETE /api/v1/connectors/:platform
// → 200 {"ok":true}; unknown platform → 404.
func (s *Server) apiDeleteConnector(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	platform := c.Param("platform")

	if _, ok := gateway.CredSpecFor(platform); !ok {
		return jsonErr(c, http.StatusNotFound, "not_found", "unknown platform: "+platform)
	}

	if s.gateway != nil {
		_ = s.gateway.Reload(c.Request().Context(), u.ID, platform)
	}

	if err := s.db.DeletePlatformConnection(u.ID, platform); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "failed to delete connector")
	}

	s.audit.Log(u.ID, "disconnect_platform", "platform:"+platform, "", c.RealIP())
	return c.JSON(http.StatusOK, apiOKResponse{OK: true})
}

// apiTestConnector probes a saved connection's credentials via
// s.testConnectorIdentity. POST /api/v1/connectors/:platform/test → 200
// {"ok":true,"identity":...} or 200 {"ok":false,"error":...}; unknown
// platform → 404.
func (s *Server) apiTestConnector(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	platform := c.Param("platform")

	if _, ok := gateway.CredSpecFor(platform); !ok {
		return jsonErr(c, http.StatusNotFound, "not_found", "unknown platform: "+platform)
	}

	identity, err := s.testConnectorIdentity(u.ID, platform)
	if err != nil {
		return c.JSON(http.StatusOK, apiTestConnectorResponse{OK: false, Error: err.Error()})
	}
	return c.JSON(http.StatusOK, apiTestConnectorResponse{OK: true, Identity: identity})
}
