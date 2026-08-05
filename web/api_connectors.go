package web

import (
	"errors"
	"net/http"

	"github.com/ilijad1/rookery/internal/db"
	"github.com/ilijad1/rookery/internal/gateway"
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
	g.PUT("/connectors/:platform/primary", s.apiSetPrimaryConnector)
	g.DELETE("/connectors/:platform/identity", s.apiUnlinkConnector)
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
	Identity   string              `json:"identity"` // the BOT's username
	// Linked reports whether the OPERATOR has completed the /start handshake.
	// Connected means only that the token authenticates; Linked is what makes
	// the integration usable, and the two are routinely different.
	Linked         bool   `json:"linked"`
	LinkedIdentity string `json:"linked_identity"` // the operator's platform user id
	Primary        bool   `json:"primary"`         // receives unprompted delivery
	DMURL          string `json:"dm_url"`
	InviteURL      string `json:"invite_url"`
	// BotOnline reports whether a live adapter is running for this platform
	// right now. A saved connection whose server is down is otherwise
	// indistinguishable from one simply waiting for /start — the exact
	// ambiguity that made a dead server read as a misconfigured Discord app.
	// Advisory: Linked remains the only proof the handshake completed.
	BotOnline bool `json:"bot_online"`
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

	// One read for the whole list rather than one per platform.
	identities, _ := s.db.ListPlatformIdentities(u.ID, "")
	linkedBy := make(map[string]*db.PlatformIdentity, len(identities))
	for _, i := range identities {
		linkedBy[i.Platform] = i
	}
	primary, _ := s.db.GetSetting(u.ID, gateway.PrimaryPlatformSettingKey)
	if primary == "" && len(identities) > 0 {
		// Unset primary means "first linked" — defined, not arbitrary.
		primary = identities[0].Platform
	}

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

			// A nil gateway is the test/no-wiring case: report offline rather
			// than claim a liveness we cannot observe.
			entry.BotOnline = conn.Active && s.gateway != nil &&
				s.gateway.IsRunning(u.ID, spec.Platform)

			// Without credentials there is no bot, so a username and an
			// invite link would be stale artefacts of a previous connection —
			// only surface them while the platform is actually connected.
			bot := gateway.BotIdentityFromSetting(mustSetting(s, u.ID, gateway.BotIdentitySettingKey(spec.Platform)))
			entry.Identity = bot.Username
			if spec.LinkURLs != nil {
				targets := spec.LinkURLs(bot)
				entry.DMURL, entry.InviteURL = targets.DMURL, targets.InviteURL
			}
		}

		if id, ok := linkedBy[spec.Platform]; ok {
			entry.Linked = true
			entry.LinkedIdentity = id.PlatformUserID
			entry.Primary = spec.Platform == primary
		}

		out = append(out, entry)
	}
	return out
}

// mustSetting reads a setting, treating a miss as empty — every caller here
// degrades gracefully on absence.
func mustSetting(s *Server, workspaceID, key string) string {
	v, _ := s.db.GetSetting(workspaceID, key)
	return v
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
		// The credentials are valid here — the bot is simply already spoken
		// for — so invalid_credentials would send the user back to re-check a
		// token that is perfectly fine.
		if errors.Is(err, ErrBotAlreadyConnected) {
			return jsonErr(c, http.StatusConflict, "bot_already_connected", err.Error())
		}
		return jsonErr(c, http.StatusBadRequest, "invalid_credentials", err.Error())
	}
	s.audit.Log(u.ID, "connect_platform", "platform:"+req.Platform, "", c.RealIP())

	resp := apiSaveConnectorResponse{OK: true, Identity: identity.Username}
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

	// Delete the row BEFORE reloading. Reload stops the adapter, re-reads the
	// connection and starts it again when the row is still present and active
	// — so calling it first stopped the bot and immediately RESTARTED it, and
	// the delete then removed the row out from under a live adapter. The bot
	// kept its gateway session and went on answering messages for a connector
	// the UI reported as disconnected, until the next server restart.
	// Reload's own "connection was deleted — nothing to start" branch is what
	// this ordering is meant to hit.
	if err := s.db.DeletePlatformConnection(u.ID, platform); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "failed to delete connector")
	}

	if s.gateway != nil {
		_ = s.gateway.Reload(c.Request().Context(), u.ID, platform)
	}
	// A disconnected platform must not retain a bot identity — best-effort,
	// like the write side; a failure here must not fail the disconnect.
	_ = s.db.SetSetting(u.ID, gateway.BotIdentitySettingKey(platform), "")

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
	return c.JSON(http.StatusOK, apiTestConnectorResponse{OK: true, Identity: identity.Username})
}

// apiSetPrimaryConnector chooses which linked chat app receives unprompted
// delivery. PUT /api/v1/connectors/:platform/primary → 200 {"ok":true}.
func (s *Server) apiSetPrimaryConnector(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	platform := c.Param("platform")

	if _, ok := gateway.CredSpecFor(platform); !ok {
		return jsonErr(c, http.StatusNotFound, "not_found", "unknown platform: "+platform)
	}
	// Only a LINKED platform can be primary; otherwise the setting names a
	// target that can never receive anything.
	rows, err := s.db.ListPlatformIdentities(u.ID, platform)
	if err != nil || len(rows) == 0 {
		return jsonErr(c, http.StatusBadRequest, "not_linked",
			"link "+platform+" before making it the primary app")
	}
	if err := s.db.SetSetting(u.ID, gateway.PrimaryPlatformSettingKey, platform); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "failed to save primary app")
	}
	s.audit.Log(u.ID, "set_primary_platform", "platform:"+platform, "", c.RealIP())
	return c.JSON(http.StatusOK, apiOKResponse{OK: true})
}

// apiUnlinkConnector removes the operator's identity link while KEEPING the
// saved credentials, so a wrong link is self-serviceable. The router otherwise
// refuses a re-link with "contact your administrator", which is a dead end in a
// single-owner product.
// DELETE /api/v1/connectors/:platform/identity → 200 {"ok":true}.
func (s *Server) apiUnlinkConnector(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	platform := c.Param("platform")

	if _, ok := gateway.CredSpecFor(platform); !ok {
		return jsonErr(c, http.StatusNotFound, "not_found", "unknown platform: "+platform)
	}
	if err := s.db.DeletePlatformIdentity(u.ID, platform); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "failed to unlink")
	}
	// Clear a primary that now names an unlinked platform.
	if cur, _ := s.db.GetSetting(u.ID, gateway.PrimaryPlatformSettingKey); cur == platform {
		_ = s.db.SetSetting(u.ID, gateway.PrimaryPlatformSettingKey, "")
	}
	s.audit.Log(u.ID, "unlink_platform", "platform:"+platform, "", c.RealIP())
	return c.JSON(http.StatusOK, apiOKResponse{OK: true})
}
