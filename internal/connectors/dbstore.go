package connectors

import (
	"context"
	"fmt"
	"time"

	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/secrets"
)

// expirySkew treats a token as expired this far ahead of its real expiry, so a call
// never races a token that lapses mid-request.
const expirySkew = 2 * time.Minute

// DBTokenStore implements TokenStore against the service_connections table. It reads
// the stored (encrypted) access token, refreshes + re-encrypts + persists when near
// expiry, and flips status to NEEDS_REAUTH on unrecoverable refresh failure — all
// headlessly (system key, no master password).
type DBTokenStore struct {
	DB        *db.DB
	SystemKey []byte
	Reg       *Registry
	OAuth     OAuthClient
	Now       func() time.Time // injectable for tests; nil → time.Now
}

func (s *DBTokenStore) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// AccessToken returns a currently-valid bearer token for conn, refreshing if needed.
func (s *DBTokenStore) AccessToken(ctx context.Context, conn ConnRef) (string, error) {
	row, err := s.DB.GetServiceConnection(ctx, conn.ID)
	if err != nil || row == nil {
		return "", &ConnectorError{KindOther, fmt.Sprintf("connection %s not found", conn.ID)}
	}
	if row.Status != "ACTIVE" {
		return "", &ConnectorError{KindNeedsReauth, fmt.Sprintf("connection %s is %s — reconnect it in Settings → Connectors", row.AccountLabel, row.Status)}
	}
	// Non-expiring providers (GitHub, Notion) never refresh — use the stored token as-is.
	prov, _ := s.Reg.ProviderByName(row.Provider)
	if prov.NonExpiring() || !s.expired(row.ExpiresAt) {
		tok, err := secrets.DecryptWithSystemKey(row.EncryptedAccessToken, s.SystemKey)
		if err != nil {
			return "", &ConnectorError{KindOther, "decrypt access token: " + err.Error()}
		}
		return tok, nil
	}
	return s.refresh(ctx, row)
}

func (s *DBTokenStore) expired(expiresAt string) bool {
	if expiresAt == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return true
	}
	return s.now().Add(expirySkew).After(t)
}

func (s *DBTokenStore) refresh(ctx context.Context, row *db.ServiceConnection) (string, error) {
	prov, ok := s.Reg.ProviderByName(row.Provider)
	if !ok {
		return "", &ConnectorError{KindOther, "unknown provider " + row.Provider}
	}
	cfg, err := s.DB.GetServiceProviderConfig(ctx, row.WorkspaceID, row.Provider)
	if err != nil || cfg == nil {
		return "", &ConnectorError{KindNeedsReauth, "missing OAuth app credentials for " + row.Provider}
	}
	cid, _ := secrets.DecryptWithSystemKey(cfg.EncryptedClientID, s.SystemKey)
	csec, _ := secrets.DecryptWithSystemKey(cfg.EncryptedClientSecret, s.SystemKey)
	refreshTok, err := secrets.DecryptWithSystemKey(row.EncryptedRefreshToken, s.SystemKey)
	if err != nil || refreshTok == "" {
		s.DB.UpdateConnectionStatus(ctx, row.ID, "NEEDS_REAUTH")
		return "", &ConnectorError{KindNeedsReauth, "no refresh token — reconnect " + row.AccountLabel}
	}
	ts, err := s.OAuth.Refresh(ctx, prov, cid, csec, refreshTok)
	if err != nil {
		s.DB.UpdateConnectionStatus(ctx, row.ID, "NEEDS_REAUTH")
		return "", &ConnectorError{KindNeedsReauth, "token refresh failed for " + row.AccountLabel + "; reconnect it (" + err.Error() + ")"}
	}
	encNew, err := secrets.EncryptWithSystemKey(ts.AccessToken, s.SystemKey)
	if err != nil {
		return "", &ConnectorError{KindOther, "encrypt refreshed token: " + err.Error()}
	}
	exp := s.now().Add(time.Duration(ts.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
	// Refresh-token rotation (Atlassian): when the provider returns a NEW refresh token,
	// persist it too — the old one is now invalid. OAuthClient.Refresh keeps the old token
	// when the response omits one (Google), so a change here always means a real rotation.
	if ts.RefreshToken != refreshTok {
		encRefresh, encErr := secrets.EncryptWithSystemKey(ts.RefreshToken, s.SystemKey)
		if encErr == nil {
			if err := s.DB.UpdateConnectionTokensFull(ctx, row.ID, encNew, encRefresh, exp, "ACTIVE"); err != nil {
				return "", &ConnectorError{KindOther, err.Error()}
			}
			return ts.AccessToken, nil
		}
	}
	if err := s.DB.UpdateConnectionTokens(ctx, row.ID, encNew, exp, "ACTIVE"); err != nil {
		return "", &ConnectorError{KindOther, err.Error()}
	}
	return ts.AccessToken, nil
}
