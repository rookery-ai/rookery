package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

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

// testConnectorIdentity decrypts a saved connection's credentials and runs the
// platform's CredSpec.Validate, returning the bot identity (e.g. username).
// Shared by the JSON connectors API.
func (s *Server) testConnectorIdentity(workspaceID, platform string) (string, error) {
	spec, ok := gateway.CredSpecFor(platform)
	if !ok {
		return "", fmt.Errorf("unsupported platform")
	}
	conn, err := s.db.GetPlatformConnection(workspaceID, platform)
	if err != nil {
		return "", fmt.Errorf("connector not found")
	}
	token, err := gateway.DecryptToken(conn.EncryptedToken, s.systemKey)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt token")
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
		return "", nil // nothing to probe; treat as ok
	}
	return spec.Validate(values)
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
