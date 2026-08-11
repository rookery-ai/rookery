package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/ilijad1/rookery/internal/db"
	"github.com/ilijad1/rookery/internal/secrets"
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
	HTTP      *http.Client     // injectable for tests; nil → a 30s client

	// sessions caches session_exchange bearer tokens (Bluesky) per connection id.
	sessMu   sync.Mutex
	sessions map[string]sessionCacheEntry
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
	prov, _ := s.Reg.ProviderByName(row.Provider)
	// Keyless providers (Open-Meteo) carry no credential. Return empty rather than
	// falling through: an unset expiry reads as "expired", which would send this row
	// down the refresh path and fail with "missing OAuth app credentials".
	if prov.IsKeyless() {
		return "", nil
	}
	// session_exchange: the STORED credential (an app password) never expires, but the
	// value sent on a request does. Swap on demand and cache, rather than persisting a
	// short-lived JWT that would be stale for most of its life in the DB.
	if prov.UsesSessionExchange() {
		return s.sessionToken(ctx, prov, row)
	}
	// API-key connections hold a static credential in encrypted_access_token — never refresh.
	if prov.IsAPIKey() {
		tok, err := secrets.DecryptWithSystemKey(row.EncryptedAccessToken, s.SystemKey)
		if err != nil {
			return "", &ConnectorError{KindOther, "decrypt api key: " + err.Error()}
		}
		return tok, nil
	}
	// Non-expiring providers (GitHub, Notion) never refresh — use the stored token as-is.
	if prov.NonExpiring() || !s.expired(row.ExpiresAt) {
		tok, err := secrets.DecryptWithSystemKey(row.EncryptedAccessToken, s.SystemKey)
		if err != nil {
			return "", &ConnectorError{KindOther, "decrypt access token: " + err.Error()}
		}
		return tok, nil
	}
	return s.refresh(ctx, row)
}

// definitiveRejection reports whether err means the provider has rejected the
// credential itself, as opposed to being temporarily unable to answer.
func definitiveRejection(err error) bool {
	var ce *ConnectorError
	if !errors.As(err, &ce) {
		// An unclassified error is not proof of rejection. Failing open here
		// costs one more retry; failing closed costs the connection.
		return false
	}
	return ce.Kind == KindAuth
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
	prov, ok := s.Reg.OAuthProvider(row.Provider)
	if !ok {
		return "", &ConnectorError{KindOther, "unknown provider " + row.Provider}
	}
	cfg, err := s.DB.GetServiceProviderConfig(ctx, row.WorkspaceID, prov.Name)
	if err != nil || cfg == nil {
		return "", &ConnectorError{KindNeedsReauth, "missing OAuth app credentials for " + prov.Name}
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
		// Only a definitive rejection marks the connection dead. A row set to
		// NEEDS_REAUTH leaves ConnectionsNearExpiry's status='ACTIVE' filter and
		// is never renewed again, so treating a 500 or a network blip as fatal
		// permanently bricks a healthy connection that would have recovered on
		// the next tick.
		if !definitiveRejection(err) {
			return "", err
		}
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

// sessionCacheEntry holds a swapped bearer token until shortly before it expires.
type sessionCacheEntry struct {
	token   string
	expires time.Time
}

// sessionToken exchanges a connection's stored credential for a short-lived bearer
// token, caching it in memory.
//
// The cache is per-process and deliberately not persisted: a Bluesky accessJwt lives
// about two hours, so storing it would mean a DB row that is stale far more often than
// it is fresh, and a restart simply re-exchanges — the app password is the durable
// credential.
func (s *DBTokenStore) sessionToken(ctx context.Context, prov Provider, row *db.ServiceConnection) (string, error) {
	s.sessMu.Lock()
	if e, ok := s.sessions[row.ID]; ok && s.now().Add(expirySkew).Before(e.expires) {
		s.sessMu.Unlock()
		return e.token, nil
	}
	s.sessMu.Unlock()

	cred, err := secrets.DecryptWithSystemKey(row.EncryptedAccessToken, s.SystemKey)
	if err != nil {
		return "", &ConnectorError{KindOther, "decrypt credential: " + err.Error()}
	}
	identity := ParseExtra(row.Extra)[prov.Auth.SessionIdentityKey]
	if identity == "" {
		identity = row.AccountIdentity
	}

	body, _ := json.Marshal(map[string]string{"identifier": identity, "password": cred})
	req, _ := http.NewRequestWithContext(ctx, "POST", prov.Auth.SessionURL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	client := s.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", &ConnectorError{KindNetwork, err.Error()}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		// An app password that was revoked shows up here, so this must read as
		// needs-reauth rather than a generic failure.
		return "", &ConnectorError{KindNeedsReauth,
			fmt.Sprintf("could not start a %s session (%d) — the app password may have been revoked: %s",
				prov.Label, resp.StatusCode, string(raw))}
	}
	var out struct {
		AccessJwt string `json:"accessJwt"`
		Did       string `json:"did"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", &ConnectorError{KindOther, err.Error()}
	}
	if out.AccessJwt == "" {
		return "", &ConnectorError{KindNeedsReauth, "session response carried no access token"}
	}

	s.sessMu.Lock()
	if s.sessions == nil {
		s.sessions = map[string]sessionCacheEntry{}
	}
	// One hour, well inside Bluesky's ~2h lifetime: the cost of re-exchanging is one
	// request, while serving an expired token fails the agent's actual call.
	s.sessions[row.ID] = sessionCacheEntry{token: out.AccessJwt, expires: s.now().Add(time.Hour)}
	s.sessMu.Unlock()
	return out.AccessJwt, nil
}
