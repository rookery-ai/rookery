package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/gateway"
)

// ErrBotAlreadyConnected is returned when the credentials name a bot that
// another workspace already uses. Typed so the JSON layer can answer with its
// own error code instead of the generic invalid_credentials, which would be a
// lie — the credentials are perfectly valid, they are just spoken for.
var ErrBotAlreadyConnected = errors.New("bot already connected to another workspace")

// ensureBotUnused refuses to connect a bot that another workspace already uses.
//
// Sharing one bot across workspaces cannot work, and fails in a way that looks
// like a product bug rather than a misconfiguration. platform_identities carries
// UNIQUE(platform, platform_user_id), so a given chat account can be linked to
// exactly ONE workspace; the second workspace's /start is refused by
// Router.handleStart ("You're already linked!") and its connect wizard waits for
// a handshake that can never arrive. Worse, both workspaces open their own
// gateway session for the same token, so every inbound DM is delivered twice,
// dispatched twice, and answered twice — by the FIRST workspace both times,
// since GatewayManager.dispatch resolves the identity globally. The user sees
// duplicate replies and pays for two coder turns per message.
//
// Keyed on the bot's platform user id rather than the token: a token reset in
// the provider's console yields the same bot with different bytes, which a
// token hash would wave straight through.
//
// Fails OPEN when the identity is unknown (a platform with no Validate, or one
// that returns no user id). Blocking then would reject every connect on that
// platform to prevent a collision we cannot even detect.
func (s *Server) ensureBotUnused(workspaceID, platform string, spec gateway.CredSpec, identity gateway.BotIdentity) error {
	if identity.UserID == "" {
		return nil
	}

	rows, err := s.db.ListPlatformBotIdentities(platform, workspaceID, gateway.BotIdentitySettingKey(platform))
	if err != nil {
		// Fail open: a read failure here must not block an otherwise valid
		// connect. The cost of a missed duplicate is confusion; the cost of a
		// false block is an unusable product.
		return nil
	}

	for _, r := range rows {
		if gateway.BotIdentityFromSetting(r.IdentityJSON).UserID != identity.UserID {
			continue
		}
		label := spec.Label
		if label == "" {
			label = platform
		}
		return fmt.Errorf("%w: this %s bot is already connected to the workspace %q. "+
			"Each workspace needs its own bot — create a second one and use its token here",
			ErrBotAlreadyConnected, label, r.WorkspaceName)
	}
	return nil
}

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

	if err := s.ensureBotUnused(workspaceID, platform, spec, identity); err != nil {
		return gateway.BotIdentity{}, nil, err
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
