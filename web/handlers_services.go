package web

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/connectors"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/secrets"
	"github.com/labstack/echo/v4"
)

// ── Signed OAuth state ──────────────────────────────────────────────────────
// The `state` round-trips through the provider, so it must be tamper-proof and
// time-bounded. Format (before base64): "<unix>|<payload>|<hmac>".

const stateTTL = 10 * time.Minute

func stateMAC(secret []byte, msg string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(msg))
	return hex.EncodeToString(m.Sum(nil))
}

func signState(secret []byte, payload string, now time.Time) string {
	ts := strconv.FormatInt(now.Unix(), 10)
	msg := ts + "|" + payload
	tok := msg + "|" + stateMAC(secret, msg)
	return base64.RawURLEncoding.EncodeToString([]byte(tok))
}

func verifyState(secret []byte, tok string, now time.Time) (string, bool) {
	b, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		return "", false
	}
	parts := strings.SplitN(string(b), "|", 3)
	if len(parts) != 3 {
		return "", false
	}
	ts, payload, mac := parts[0], parts[1], parts[2]
	if !hmac.Equal([]byte(mac), []byte(stateMAC(secret, ts+"|"+payload))) {
		return "", false
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || now.Sub(time.Unix(sec, 0)) > stateTTL {
		return "", false
	}
	return payload, true
}

// ── Service connections (OAuth + API-key) ────────────────────────────────────

// serviceProviders is the set of providers exposed in the UI: every provider the registry
// loaded. Derived rather than hardcoded so adding a service really is "two YAML files, no
// Go changes" — the maintained slice this replaced silently omitted them. Shared with the
// JSON services API (api_services.go).
func (s *Server) serviceProviders() []string {
	return s.connectors.ProviderNames()
}

// redirectWithError performs a PRG redirect carrying a user-facing error message in
// the query string. Used by the OAuth callback (handleOAuthCallback) to land the
// browser back on the SPA connections page with an alert.
func (s *Server) redirectWithError(c echo.Context, path, msg string) error {
	return c.Redirect(http.StatusSeeOther, path+"?error="+url.QueryEscape(msg))
}

// callbackURL is the redirect URI the workspace registers with the provider.
func (s *Server) callbackURL(c echo.Context, provider string) string {
	return s.publicBaseURL(c) + "/dashboard/connectors/services/callback/" + provider
}

func (s *Server) publicBaseURL(c echo.Context) string {
	if b := os.Getenv("SA_PUBLIC_URL"); b != "" {
		return strings.TrimRight(b, "/")
	}
	scheme := c.Scheme()
	return scheme + "://" + c.Request().Host
}

// consentURLError classifies a failure building an OAuth consent URL so both
// the template handler (redirect + flash message) and the JSON API
// (status + code) can render it their own way without duplicating the
// underlying resolution logic.
type consentURLError struct {
	Code string // "unknown_provider" | "missing_creds" | "unreadable_creds"
	Msg  string
}

func (e *consentURLError) Error() string { return e.Msg }

// buildConsentURL resolves a provider's saved OAuth app credentials and
// constructs the signed-state consent URL the user visits to authorize this
// workspace. Shared by handleConnectService (redirect) and apiConnectService
// (JSON) — the only two callers.
func (s *Server) buildConsentURL(c echo.Context, w *db.Workspace, provider, label string, inputs map[string]string) (string, error) {
	child, ok := s.connectors.ProviderByName(provider)
	if !ok {
		return "", &consentURLError{"unknown_provider", "Unknown provider."}
	}
	oauth, ok := s.connectors.OAuthProvider(provider) // parent when aliased, else self
	if !ok {
		return "", &consentURLError{"unknown_provider", "Unknown provider."}
	}
	cfg, _ := s.db.GetServiceProviderConfig(c.Request().Context(), w.ID, oauth.Name)
	if cfg == nil {
		return "", &consentURLError{"missing_creds", "Save your " + oauth.Label + " OAuth app credentials first."}
	}
	clientID, err := secrets.DecryptWithSystemKey(cfg.EncryptedClientID, s.systemKey)
	if err != nil {
		return "", &consentURLError{"unreadable_creds", "Stored credentials are unreadable; re-enter them."}
	}
	nonce := uuid.New().String()
	// connect_inputs ride the signed state rather than server-side pending storage: the
	// state is already HMAC-signed and TTL'd, so it cannot be tampered with, and there is
	// no row to garbage-collect when a user abandons the consent screen. Base64 keeps the
	// JSON clear of the "~" field separator.
	encoded := ""
	if len(inputs) > 0 {
		if b, err := json.Marshal(inputs); err == nil {
			encoded = base64.RawURLEncoding.EncodeToString(b)
		}
	}
	payload := strings.Join([]string{w.ID, provider, label, nonce, encoded}, "~")
	state := signState(s.systemKey, payload, time.Now())
	return oauth.ConsentURL(clientID, s.callbackURL(c, provider), state, child.DefaultScopes), nil
}

// connectAPIKeyCore validates the connect-input fields, derives any
// provider-specific extra values (DeriveKeyExtra), encrypts the API key, and
// inserts the service connection row. Shared by handleConnectAPIKey (redirect)
// and apiConnectAPIKey (JSON) — the only two callers. Provider lookup/validation
// and the "key is required" check are the caller's job (their not-found/empty-key
// wording differs), as is auditing (only the caller has request IP). userErrMsg
// is a user-facing validation problem (400-class: a required connect-input is
// missing); err is an unexpected failure (500-class: key encryption or the
// DB insert failed) — both callers treat a non-nil err as an internal error.
func (s *Server) connectAPIKeyCore(ctx context.Context, w *db.Workspace, prov connectors.Provider, provider, apiKey, label string, inputs map[string]string) (conn *db.ServiceConnection, userErrMsg string, err error) {
	if label == "" {
		label = "default"
	}

	extra := map[string]string{}
	for _, ci := range prov.ConnectInputs {
		v := strings.TrimSpace(inputs[ci.Key])
		if ci.Required && v == "" {
			return nil, ci.Label + " is required.", nil
		}
		if v != "" {
			extra[ci.Key] = v
		}
	}
	for k, v := range connectors.DeriveKeyExtra(prov, apiKey) {
		extra[k] = v
	}
	extraJSON := ""
	if len(extra) > 0 {
		if b, jerr := json.Marshal(extra); jerr == nil {
			extraJSON = string(b)
		}
	}

	enc, encErr := secrets.EncryptWithSystemKey(apiKey, s.systemKey)
	if encErr != nil {
		return nil, "", errors.New("Failed to store the API key.")
	}

	row := db.ServiceConnection{
		ID: uuid.New().String(), WorkspaceID: w.ID, Provider: provider,
		AccountLabel: label, AccountIdentity: label,
		EncryptedAccessToken: enc, Status: "ACTIVE", Extra: extraJSON,
	}
	if insErr := s.db.InsertServiceConnection(ctx, row); insErr != nil {
		return nil, "", fmt.Errorf("Failed to save the connection: %w", insErr)
	}
	return &row, "", nil
}

func (s *Server) handleOAuthCallback(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	provider := c.Param("provider")
	ctx := c.Request().Context()

	if errParam := c.QueryParam("error"); errParam != "" {
		return s.redirectWithError(c, "/connections", "Authorization was denied: "+errParam)
	}
	code := c.QueryParam("code")
	payload, ok := verifyState(s.systemKey, c.QueryParam("state"), time.Now())
	if !ok || code == "" {
		return s.redirectWithError(c, "/connections", "Invalid or expired authorization request; try again.")
	}
	parts := strings.Split(payload, "~")
	// 4 fields is a state issued before connect_inputs existed; the 10-minute TTL means
	// such a state can still be in flight across a deploy, so both shapes are accepted.
	if len(parts) < 4 || len(parts) > 5 || parts[0] != w.ID || parts[1] != provider {
		return s.redirectWithError(c, "/connections", "Authorization did not match this workspace; try again.")
	}
	label := parts[2]
	connectInputs := map[string]string{}
	if len(parts) == 5 && parts[4] != "" {
		if raw, derr := base64.RawURLEncoding.DecodeString(parts[4]); derr == nil {
			_ = json.Unmarshal(raw, &connectInputs)
		}
	}

	prov, ok := s.connectors.ProviderByName(provider)
	if !ok {
		return s.redirectWithError(c, "/connections", "Unknown provider.")
	}
	// authProv is the OAuth parent when this provider is aliased (e.g. google_drive → google),
	// else the provider itself. It governs endpoints, token settings, and the app-credentials
	// lookup key; `prov` (the child) still governs scopes/post_connect/expiry below.
	authProv, ok := s.connectors.OAuthProvider(provider)
	if !ok {
		return s.redirectWithError(c, "/connections", "Unknown provider.")
	}
	cfg, _ := s.db.GetServiceProviderConfig(ctx, w.ID, authProv.Name)
	if cfg == nil {
		return s.redirectWithError(c, "/connections", "Missing OAuth app credentials.")
	}
	clientID, _ := secrets.DecryptWithSystemKey(cfg.EncryptedClientID, s.systemKey)
	clientSecret, _ := secrets.DecryptWithSystemKey(cfg.EncryptedClientSecret, s.systemKey)

	oauth := connectors.OAuthClient{}
	ts, err := oauth.ExchangeCode(ctx, authProv, clientID, clientSecret, code, s.callbackURL(c, provider))
	if err != nil {
		return s.redirectWithError(c, "/connections", "Token exchange failed: "+err.Error())
	}
	identity, _ := oauth.FetchIdentity(ctx, authProv, ts.AccessToken)

	// Post-connect resolution (e.g. Jira cloud id) + token_extra fields (e.g. Salesforce
	// instance_url) → merged into extra, exposed to request templates as {{conn.<key>}}.
	extraMap := map[string]string{}
	if prov.PostConnect != "" {
		res, perr := connectors.RunPostConnect(ctx, prov.PostConnect, nil, ts.AccessToken)
		if perr != nil {
			return s.redirectWithError(c, "/connections", "Connected, but setup failed: "+perr.Error())
		}
		for k, v := range res.Extra {
			extraMap[k] = v
		}
		// A hook may REPLACE the stored token: Facebook publishing needs the Page's own
		// token, not the user token OAuth returned. Storing it here keeps the credential
		// in the encrypted column rather than in plaintext `extra`.
		if res.AccessToken != "" {
			ts.AccessToken = res.AccessToken
		}
	}
	for k, v := range ts.Extra { // token_extra fields (e.g. Salesforce instance_url)
		extraMap[k] = v
	}
	// connect_inputs collected before consent (e.g. Google Ads developer token). Applied
	// last so a user-supplied value wins over a hook-derived one of the same name.
	for k, v := range connectInputs {
		if strings.TrimSpace(v) != "" {
			extraMap[k] = v
		}
	}
	extraJSON := ""
	if len(extraMap) > 0 {
		if b, _ := json.Marshal(extraMap); b != nil {
			extraJSON = string(b)
		}
	}

	encAccess, _ := secrets.EncryptWithSystemKey(ts.AccessToken, s.systemKey)
	encRefresh, _ := secrets.EncryptWithSystemKey(ts.RefreshToken, s.systemKey)
	// Non-expiring providers (GitHub, Notion) store an empty expiry so refresh is never attempted.
	expiresAt := ""
	if !prov.NonExpiring() {
		expiresAt = time.Now().Add(time.Duration(ts.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
	}

	if err := s.db.InsertServiceConnection(ctx, db.ServiceConnection{
		ID: uuid.New().String(), WorkspaceID: w.ID, Provider: provider,
		AccountLabel: label, AccountIdentity: identity,
		Scopes:               strings.Join(prov.DefaultScopes, " "),
		EncryptedAccessToken: encAccess, EncryptedRefreshToken: encRefresh,
		ExpiresAt: expiresAt, Status: "ACTIVE", Extra: extraJSON,
	}); err != nil {
		// A duplicate label is no longer a plausible cause here — InsertServiceConnection
		// upserts on (workspace_id, provider, account_label), so reconnecting under the
		// same label refreshes the existing connection instead of erroring.
		return s.redirectWithError(c, "/connections", "Connected, but saving failed: "+err.Error())
	}
	return c.Redirect(http.StatusSeeOther, "/connections?connected="+url.QueryEscape(provider))
}
