package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/gateway"
	"github.com/labstack/echo/v4"
)

type connectorsPageData struct {
	*pageData
	Connections []*db.PlatformConnection
	Platforms   []string
}

// supportedPlatformNames derives the list of connectable platforms from the
// registered credential specs, so adding an adapter needs no UI/handler edit.
func supportedPlatformNames() []string {
	var out []string
	for _, sp := range gateway.CredSpecs() {
		out = append(out, sp.Platform)
	}
	return out
}

func (s *Server) showConnectors(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	platforms := supportedPlatformNames()
	var connections []*db.PlatformConnection
	for _, p := range platforms {
		if conn, err := s.db.GetPlatformConnection(u.ID, p); err == nil {
			connections = append(connections, conn)
		}
	}
	return c.Render(http.StatusOK, "dashboard/connectors.html", &connectorsPageData{
		pageData:    s.page(c, "Chat Connectors"),
		Connections: connections,
		Platforms:   platforms,
	})
}

// saveConnector validates + encrypts + persists a platform's credentials for a
// workspace, stores the bot username (Telegram), and (re)starts the gateway
// adapter. Shared by the connectors page and the setup wizard's connector step.
// Driven entirely by the platform's registered gateway.CredSpec, so a new
// adapter needs no new save logic here. botStartErr is non-nil only when the
// credentials saved but the bot failed to start (non-fatal).
func (s *Server) saveConnector(workspaceID, platform string, values map[string]string) (identity string, botStartErr error, err error) {
	spec, ok := gateway.CredSpecFor(platform)
	if !ok {
		return "", nil, fmt.Errorf("unsupported platform: %s", platform)
	}
	if spec.Validate != nil {
		if identity, err = spec.Validate(values); err != nil {
			return "", nil, fmt.Errorf("invalid credentials: %w", err)
		}
	}

	token, configJSON, err := gateway.SplitCreds(spec, values)
	if err != nil {
		return "", nil, err
	}

	encToken, err := gateway.EncryptToken(token, s.systemKey)
	if err != nil {
		return "", nil, fmt.Errorf("failed to encrypt token: %w", err)
	}
	encConfig := ""
	if configJSON != "" {
		if encConfig, err = gateway.EncryptToken(configJSON, s.systemKey); err != nil {
			return "", nil, fmt.Errorf("failed to encrypt config: %w", err)
		}
	}

	if err := s.db.UpsertPlatformConnection(&db.PlatformConnection{
		ID:              uuid.New().String(),
		WorkspaceID:     workspaceID,
		Platform:        platform,
		EncryptedToken:  encToken,
		EncryptedConfig: encConfig,
		Active:          true,
	}); err != nil {
		return "", nil, fmt.Errorf("failed to save connector: %w", err)
	}

	if platform == "telegram" && identity != "" {
		_ = s.db.SetSetting(workspaceID, "telegram_bot_username", "@"+identity)
	}

	if s.gateway != nil {
		if e := s.gateway.Reload(context.Background(), workspaceID, platform); e != nil {
			botStartErr = e // token is valid but bot may be temporarily unreachable
		}
	}
	return identity, botStartErr, nil
}

func (s *Server) handleSaveConnector(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	platform := c.FormValue("platform")

	values := map[string]string{}
	if spec, ok := gateway.CredSpecFor(platform); ok {
		for _, f := range spec.Fields {
			values[f.Key] = c.FormValue(f.Key)
		}
	}

	if platform == "" || values["token"] == "" {
		p := s.page(c, "Chat Connectors")
		p.Error = "Platform and token are required"
		return s.renderConnectors(c, u, p)
	}

	identity, botStartErr, err := s.saveConnector(u.ID, platform, values)
	if err != nil {
		p := s.page(c, "Chat Connectors")
		p.Error = err.Error()
		return s.renderConnectors(c, u, p)
	}
	s.audit.Log(u.ID, "connect_platform", "platform:"+platform, "", c.RealIP())

	if botStartErr != nil {
		p := s.page(c, "Chat Connectors")
		p.Error = "Connector saved but bot failed to start: " + botStartErr.Error()
		return s.renderConnectors(c, u, p)
	}

	// Allow setup wizard to redirect back to its flow.
	if next := c.FormValue("next"); next != "" && len(next) > 0 && next[0] == '/' {
		return c.Redirect(http.StatusFound, next)
	}

	botDisplay := ""
	if identity != "" {
		botDisplay = "@" + identity
	}
	p := s.page(c, "Chat Connectors")
	if botDisplay != "" {
		p.Success = "Bot " + botDisplay + " connected! Send /start to it in Telegram to link your account."
	} else {
		p.Success = "Connected to " + platform + " successfully! Send /start to your bot to link your account."
	}
	return s.renderConnectors(c, u, p)
}

func (s *Server) handleDeleteConnector(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	platform := c.Param("platform")

	if s.gateway != nil {
		if err := s.gateway.Reload(context.Background(), u.ID, platform); err != nil {
			// Log but don't block deletion.
			_ = err
		}
	}

	if err := s.db.DeletePlatformConnection(u.ID, platform); err != nil {
		return err
	}

	s.audit.Log(u.ID, "disconnect_platform", "platform:"+platform, "", c.RealIP())
	return c.Redirect(http.StatusFound, "/dashboard/connectors")
}

func (s *Server) handleTestConnector(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	platform := c.Param("platform")

	conn, err := s.db.GetPlatformConnection(u.ID, platform)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"status": "error", "message": "connector not found"})
	}

	token, err := gateway.DecryptToken(conn.EncryptedToken, s.systemKey)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"status": "error", "message": "failed to decrypt token"})
	}

	switch platform {
	case "telegram":
		// Validate token by hitting the Telegram getMe endpoint.
		botUser, err := testTelegramToken(token)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"status": "error", "message": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{
			"status":   "ok",
			"platform": platform,
			"bot":      "@" + botUser,
		})
	default:
		return c.JSON(http.StatusBadRequest, map[string]string{"status": "error", "message": "unsupported platform"})
	}
}

// testTelegramToken calls Telegram's getMe API to validate the bot token.
// Returns the bot username on success.
func testTelegramToken(token string) (string, error) {
	resp, err := http.Get("https://api.telegram.org/bot" + token + "/getMe")
	if err != nil {
		return "", fmt.Errorf("telegram api unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			Username string `json:"username"`
		} `json:"result"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("invalid response from telegram")
	}
	if !result.OK {
		return "", fmt.Errorf("telegram rejected token: %s", result.Description)
	}
	return result.Result.Username, nil
}

func (s *Server) renderConnectors(c echo.Context, u *db.Workspace, p *pageData) error {
	platforms := supportedPlatformNames()
	var connections []*db.PlatformConnection
	for _, pl := range platforms {
		if conn, err := s.db.GetPlatformConnection(u.ID, pl); err == nil {
			connections = append(connections, conn)
		}
	}
	return c.Render(http.StatusOK, "dashboard/connectors.html", &connectorsPageData{
		pageData:    p,
		Connections: connections,
		Platforms:   platforms,
	})
}
