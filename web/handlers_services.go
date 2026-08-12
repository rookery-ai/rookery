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
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/rookery-ai/rookery/internal/connectors"
	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/publicurl"
	"github.com/rookery-ai/rookery/internal/secrets"
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

// oauthAppName resolves a provider to the provider that OWNS the OAuth
// application it authenticates through: the `auth_parent` when the provider is
// aliased (google_calendar → google, teams → outlook), else the provider itself.
//
// The redirect URI is a property of the OAuth APPLICATION, not of the service
// being connected. Building it from the child name meant every aliased provider
// sent a URI its app had never registered — Google has …/callback/google
// registered, so connecting Calendar sent …/callback/google_calendar and was
// rejected with redirect_uri_mismatch at the CONSENT screen, before any code was
// issued and therefore before explainOAuthError could ever run.
//
// An unknown provider resolves to itself: callers validate the name separately,
// and returning "" here would produce a silently truncated URI.
func (s *Server) oauthAppName(provider string) string {
	if op, ok := s.connectors.OAuthProvider(provider); ok && op.Name != "" {
		return op.Name
	}
	return provider
}

// callbackURL is the redirect URI the workspace registers with the provider.
// Scoped to the OAuth application (see oauthAppName), so one registered URI
// covers a parent and every child that reuses its app.
func (s *Server) callbackURL(c echo.Context, provider string) string {
	return s.publicBaseURL(c) + "/dashboard/connectors/services/callback/" + s.oauthAppName(provider)
}

// publicBaseURL is the instance's externally-reachable base URL: the configured
// setting, else ROOKERY_PUBLIC_URL, else what this request suggests.
//
// Detection is only the fallback. It is why the redirect URI used to change with
// however the operator happened to reach the page, which is the defect the
// configured setting exists to remove.
func (s *Server) publicBaseURL(c echo.Context) string {
	base, _ := s.resolvePublicURL(c)
	return base
}

// resolvePublicURL also reports where the value came from, for the settings UI.
func (s *Server) resolvePublicURL(c echo.Context) (string, publicurl.Source) {
	return publicurl.Resolve(s.db.GetSystemSetting, detectBaseURL(c))
}

// detectBaseURL infers a base URL from the current request. Note this reads the
// Host header directly and does NOT consult X-Forwarded-Host, so any reverse
// proxy that rewrites Host must have the instance URL configured explicitly.
func detectBaseURL(c echo.Context) string {
	return c.Scheme() + "://" + c.Request().Host
}

// redirectURIFromState reads the pinned redirect URI out of a split state
// payload, tolerating the 4- and 5-field shapes issued before it was pinned.
func redirectURIFromState(parts []string) string {
	if len(parts) < 6 {
		return ""
	}
	return parts[5]
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

// duplicateConnectionMsg reports a user-facing refusal when this connect would
// collide with an existing connection, or "" when it is safe to proceed.
//
// Three cases, deliberately distinguished:
//
//   - same label, same identity — a reconnect. Allowed: the upsert refreshes the
//     tokens in place and preserves the row id (and its agent bindings).
//   - same label, DIFFERENT identity — refused. This is the silent-overwrite case.
//   - different label, same identity — refused. A second row for one account would
//     produce two tool-name variants pointing at the same mailbox, and the user
//     almost certainly meant to pick a different account at the consent screen.
//
// An empty identity means the provider exposes no userinfo endpoint, so identity
// comparison is impossible; only the label rule can be enforced, and a same-label
// reconnect stays allowed rather than being refused on a value we cannot read.
func (s *Server) duplicateConnectionMsg(ctx context.Context, workspaceID, provider, label, identity string) string {
	conns, err := s.db.ListServiceConnections(ctx, workspaceID)
	if err != nil {
		// Fail OPEN: refusing a legitimate connect because a read failed is worse
		// than the collision this guards against, which the upsert has always had.
		slog.Warn("oauth callback: duplicate check failed; allowing connect",
			"provider", provider, "err", err)
		return ""
	}
	for _, cn := range conns {
		if cn.Provider != provider {
			continue
		}
		if msg := duplicateDecision(cn.AccountLabel, cn.AccountIdentity, label, identity); msg != "" {
			return msg
		}
	}
	return ""
}

// duplicateDecision is the pure core of duplicateConnectionMsg: given one
// existing connection and the incoming one, report the refusal message, or ""
// when this pairing is fine. Split out so the decision table is testable without
// a database, which is where the interesting cases live.
func duplicateDecision(existingLabel, existingIdentity, incomingLabel, incomingIdentity string) string {
	switch {
	case existingLabel == incomingLabel:
		// A reconnect. Allowed — the upsert refreshes tokens in place and keeps the
		// row id, so agent bindings survive. Only refuse when we can PROVE the
		// accounts differ; an unknown identity on either side is not proof.
		if incomingIdentity == "" || existingIdentity == "" || existingIdentity == incomingIdentity {
			return ""
		}
		return "The name \"" + incomingLabel + "\" is already used by " + existingIdentity +
			". Choose a different name for this account."
	case incomingIdentity != "" && existingIdentity == incomingIdentity:
		return "You have already connected " + incomingIdentity + " as \"" + existingLabel +
			"\". Reconnect under that name to refresh it, or pick a different account at the sign-in screen."
	}
	return ""
}

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
	// Mastodon-style providers template their OAuth endpoints over a connect_input
	// (the instance host), so resolve before building the consent URL.
	oauth = oauth.WithConnVars(inputs)
	if strings.Contains(oauth.AuthorizeURL, "{{") || oauth.AuthorizeURL == "" {
		return "", &consentURLError{"missing_creds",
			"This provider needs its connection details filled in before connecting."}
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
	redirectURI := s.callbackURL(c, provider)
	// The URI is pinned into the signed state so the token exchange uses the
	// SAME string the consent request did. Recomputing it at callback time was a
	// real failure mode: any difference produces redirect_uri_mismatch AFTER the
	// user has already granted consent, which reads as a provider fault.
	payload := strings.Join([]string{w.ID, provider, label, nonce, encoded, redirectURI}, "~")
	state := signState(s.systemKey, payload, time.Now())
	return oauth.ConsentURL(clientID, redirectURI, state, child.DefaultScopes), nil
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
		// A keyless connection has no account behind it, so FetchIdentity cannot run
		// and "default" says nothing. Use the provider's own label — it is what the
		// connections page and ToolDefs' multi-account slug both read.
		if prov.IsKeyless() && prov.Label != "" {
			label = prov.Label
		} else {
			label = "default"
		}
	}

	extra := map[string]string{}
	for _, ci := range prov.ConnectInputs {
		v := strings.TrimSpace(inputs[ci.Key])
		if ci.Required && v == "" {
			return nil, ci.Label + " is required.", nil
		}
		if v == "" {
			continue
		}
		// A declared normalizer canonicalizes the pasted value before it is stored,
		// so every action template concatenating onto it sees one shape.
		if ci.Normalize == "base_url" {
			norm, nerr := connectors.NormalizeBaseURL(v)
			if nerr != nil {
				return nil, ci.Label + ": " + nerr.Error(), nil
			}
			v = norm
		}
		extra[ci.Key] = v
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

	// A keyless provider has no credential to store. Encrypting the empty string
	// would still yield ciphertext, leaving a secret-shaped value in the row that
	// decrypts to nothing — misleading to read and pointless to keep.
	enc := ""
	if !prov.IsKeyless() {
		var encErr error
		enc, encErr = secrets.EncryptWithSystemKey(apiKey, s.systemKey)
		if encErr != nil {
			return nil, "", errors.New("Failed to store the API key.")
		}
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
	if len(parts) < 4 || len(parts) > 6 || parts[0] != w.ID {
		return s.redirectWithError(c, "/connections", "Authorization did not match this workspace; try again.")
	}
	// The state carries the CHILD provider (google_calendar); the path now carries
	// the OAuth application that authenticated it (google). Accept either shape:
	// the new one, and the legacy child-in-path form for states already in flight
	// across a deploy, bounded by the 10-minute state TTL. Anything else is a state
	// issued for a different provider and must not be honoured.
	if provider != parts[1] && provider != s.oauthAppName(parts[1]) {
		return s.redirectWithError(c, "/connections", "Authorization did not match this workspace; try again.")
	}
	// Everything downstream — scopes, post_connect, the stored row's provider, the
	// success redirect — belongs to the CHILD, not to the app that authenticated it.
	provider = parts[1]
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
	// Same resolution as the consent URL: a per-instance provider's token and userinfo
	// endpoints are templates, and the values came back to us inside the signed state.
	authProv = authProv.WithConnVars(connectInputs)
	cfg, _ := s.db.GetServiceProviderConfig(ctx, w.ID, authProv.Name)
	if cfg == nil {
		return s.redirectWithError(c, "/connections", "Missing OAuth app credentials.")
	}
	clientID, _ := secrets.DecryptWithSystemKey(cfg.EncryptedClientID, s.systemKey)
	clientSecret, _ := secrets.DecryptWithSystemKey(cfg.EncryptedClientSecret, s.systemKey)

	// Use the URI pinned at consent time, unconditionally. It is by definition
	// the correct string: it is the one the provider itself saw and validated
	// when it issued this code, so the exchange must present the same one.
	//
	// Do NOT reject the callback when it diverges from what we would compute now.
	// The user has already granted consent at that point, and if the cause is
	// systematic — the operator changed the instance URL mid-flow, or a transient
	// GetSystemSetting error made Resolve fall through to detection — then
	// "start again" reproduces the divergence. That is a loop, not a recovery.
	// Log it and proceed.
	redirectURI := redirectURIFromState(parts)
	if redirectURI == "" {
		// A pre-pinning state, still inside the 10-minute TTL.
		redirectURI = s.callbackURL(c, provider)
	} else if current := s.callbackURL(c, provider); current != redirectURI {
		slog.Warn("oauth callback: instance URL changed mid-flow; using the pinned URI",
			"provider", provider, "pinned", redirectURI, "current", current)
	}

	oauth := connectors.OAuthClient{}
	ts, err := oauth.ExchangeCode(ctx, authProv, clientID, clientSecret, code, redirectURI)
	if err != nil {
		label := authProv.Label
		if label == "" {
			label = provider
		}
		return s.redirectWithError(c, "/connections", explainOAuthError(label, redirectURI, err))
	}
	identity, _ := oauth.FetchIdentity(ctx, authProv, ts.AccessToken)

	// Multi-account safety. InsertServiceConnection upserts on
	// (workspace_id, provider, account_label), so connecting a DIFFERENT account
	// under a label already in use silently overwrote the first one's tokens — no
	// error, and every agent bound to it quietly started acting as the new
	// account. Adding the consent account-chooser makes that easier to trigger,
	// not harder, so the collision is refused explicitly here.
	if msg := s.duplicateConnectionMsg(ctx, w.ID, provider, label, identity); msg != "" {
		return s.redirectWithError(c, "/connections", msg)
	}

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
