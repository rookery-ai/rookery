package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/ilijad1/rookery/internal/db"
	"github.com/ilijad1/rookery/internal/gateway"
)

// saveConnector validates + encrypts + persists a platform's credentials for a
// workspace, stores the bot username (Telegram), and (re)starts the gateway
// adapter. Shared by the JSON connectors API and the setup-wizard connector step.
// Driven entirely by the platform's registered gateway.CredSpec, so a new
// adapter needs no new save logic here. botStartErr is non-nil only when the
// credentials saved but the bot failed to start (non-fatal).
func (s *Server) saveConnector(workspaceID, platform string, values map[string]string) (identity gateway.BotIdentity, botStartErr error, err error) {
	spec, ok := gateway.CredSpecFor(platform)
	if !ok {
		return gateway.BotIdentity{}, nil, fmt.Errorf("unsupported platform: %s", platform)
	}
	if spec.Validate != nil {
		if identity, err = spec.Validate(values); err != nil {
			return gateway.BotIdentity{}, nil, fmt.Errorf("invalid credentials: %w", err)
		}
	}

	token, configJSON, err := gateway.SplitCreds(spec, values)
	if err != nil {
		return gateway.BotIdentity{}, nil, err
	}

	encToken, err := gateway.EncryptToken(token, s.systemKey)
	if err != nil {
		return gateway.BotIdentity{}, nil, fmt.Errorf("failed to encrypt token: %w", err)
	}
	encConfig := ""
	if configJSON != "" {
		if encConfig, err = gateway.EncryptToken(configJSON, s.systemKey); err != nil {
			return gateway.BotIdentity{}, nil, fmt.Errorf("failed to encrypt config: %w", err)
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
		return gateway.BotIdentity{}, nil, fmt.Errorf("failed to save connector: %w", err)
	}

	// Persist the bot's public identifiers so the connectors list endpoint can
	// build deep links without a network call. Best-effort: a failure here must
	// not fail an otherwise-good connect.
	if encoded, mErr := identity.MarshalSetting(); mErr == nil {
		_ = s.db.SetSetting(workspaceID, gateway.BotIdentitySettingKey(platform), encoded)
	}
	if platform == "telegram" && identity.Username != "" {
		// Legacy key, still read by the settings page.
		_ = s.db.SetSetting(workspaceID, "telegram_bot_username", "@"+identity.Username)
	}

	if s.gateway != nil {
		if e := s.gateway.Reload(context.Background(), workspaceID, platform); e != nil {
			botStartErr = e // token is valid but bot may be temporarily unreachable
		}
	}
	return identity, botStartErr, nil
}

// testConnectorIdentity decrypts a saved connection's credentials and runs the
// platform's CredSpec.Validate, returning the bot identity (e.g. username).
// Shared by the JSON connectors API.
func (s *Server) testConnectorIdentity(workspaceID, platform string) (gateway.BotIdentity, error) {
	spec, ok := gateway.CredSpecFor(platform)
	if !ok {
		return gateway.BotIdentity{}, fmt.Errorf("unsupported platform")
	}
	conn, err := s.db.GetPlatformConnection(workspaceID, platform)
	if err != nil {
		return gateway.BotIdentity{}, fmt.Errorf("connector not found")
	}
	token, err := gateway.DecryptToken(conn.EncryptedToken, s.systemKey)
	if err != nil {
		return gateway.BotIdentity{}, fmt.Errorf("failed to decrypt token")
	}
	values := map[string]string{"token": token}
	if conn.EncryptedConfig != "" {
		cfg, err := gateway.DecryptToken(conn.EncryptedConfig, s.systemKey)
		if err == nil {
			var extra map[string]string
			if json.Unmarshal([]byte(cfg), &extra) == nil {
				for k, v := range extra {
					values[k] = v
				}
			}
		}
	}
	if spec.Validate == nil {
		return gateway.BotIdentity{}, nil // nothing to probe; treat as ok
	}
	return spec.Validate(values)
}

// testTelegramToken calls Telegram's getMe API to validate the bot token.
// Returns the bot identity on success.
func testTelegramToken(token string) (gateway.BotIdentity, error) {
	resp, err := http.Get("https://api.telegram.org/bot" + token + "/getMe")
	if err != nil {
		return gateway.BotIdentity{}, fmt.Errorf("telegram api unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"result"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return gateway.BotIdentity{}, fmt.Errorf("invalid response from telegram")
	}
	if !result.OK {
		return gateway.BotIdentity{}, fmt.Errorf("telegram rejected token: %s", result.Description)
	}
	return gateway.BotIdentity{
		Username: result.Result.Username,
		UserID:   strconv.FormatInt(result.Result.ID, 10),
	}, nil
}
