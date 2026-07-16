package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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

// ── Page ────────────────────────────────────────────────────────────────────

type serviceProviderView struct {
	Name          string
	Label         string
	SetupURL      string
	SetupSteps    []string
	HasCreds      bool
	RedirectURI   string
	Connections   []db.ServiceConnection
	IsAPIKey      bool
	KeyLabel      string
	KeyHint       string
	KeySetupURL   string
	IsChild       bool
	ParentName    string
	ParentLabel   string
	ConnectInputs []connectors.ConnectInput
}

type servicesPageData struct {
	*pageData
	Providers []serviceProviderView
}

// availableServiceProviders is the set of providers exposed in the UI (grows as
// provider data files are added).
var availableServiceProviders = []string{"google", "github", "notion", "outlook", "jira", "slack", "openai", "google_drive", "google_sheets", "google_docs", "teams", "hubspot", "calendly", "asana", "airtable", "sendgrid", "intercom", "clickup", "monday", "dropbox", "zoom", "shopify", "salesforce", "mailchimp", "zendesk", "stripe", "twilio", "trello"}

// redirectWithError performs a PRG redirect carrying a user-facing error message in
// the query string (showServices renders it into the alert).
func (s *Server) redirectWithError(c echo.Context, path, msg string) error {
	return c.Redirect(http.StatusSeeOther, path+"?error="+url.QueryEscape(msg))
}

func (s *Server) showServices(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	ctx := c.Request().Context()
	all, _ := s.db.ListServiceConnections(ctx, w.ID)

	var views []serviceProviderView
	for _, provider := range availableServiceProviders {
		var conns []db.ServiceConnection
		for _, cn := range all {
			if cn.Provider == provider {
				conns = append(conns, cn)
			}
		}
		label, setupURL, setupSteps := provider, "", []string{}
		isAPIKey, keyLabel, keyHint, keySetupURL := false, "", "", ""
		var connectInputs []connectors.ConnectInput
		if p, ok := s.connectors.ProviderByName(provider); ok {
			if p.Label != "" {
				label = p.Label
			}
			setupURL, setupSteps = p.SetupURL, p.SetupSteps
			if p.IsAPIKey() {
				isAPIKey = true
				keyLabel, keyHint, keySetupURL = p.Auth.KeyLabel, p.Auth.KeyHint, p.Auth.SetupURL
			}
			connectInputs = p.ConnectInputs
		}
		isChild, parentName, parentLabel := false, "", ""
		credsProvider := provider
		if op, ok := s.connectors.OAuthProvider(provider); ok && op.Name != provider {
			isChild, parentName, parentLabel = true, op.Name, op.Label
			credsProvider = op.Name
		}
		cfgForCreds, _ := s.db.GetServiceProviderConfig(ctx, w.ID, credsProvider)
		views = append(views, serviceProviderView{
			Name:          provider,
			Label:         label,
			SetupURL:      setupURL,
			SetupSteps:    setupSteps,
			HasCreds:      cfgForCreds != nil,
			RedirectURI:   s.callbackURL(c, provider),
			Connections:   conns,
			IsAPIKey:      isAPIKey,
			KeyLabel:      keyLabel,
			KeyHint:       keyHint,
			KeySetupURL:   keySetupURL,
			IsChild:       isChild,
			ParentName:    parentName,
			ParentLabel:   parentLabel,
			ConnectInputs: connectInputs,
		})
	}
	p := s.page(c, "Service Connections")
	if e := c.QueryParam("error"); e != "" {
		p.Error = e
	}
	return c.Render(http.StatusOK, "dashboard/services.html", &servicesPageData{
		pageData:  p,
		Providers: views,
	})
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

// ── Handlers ────────────────────────────────────────────────────────────────

func (s *Server) handleSaveProviderCreds(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	provider := c.Param("provider")
	clientID := strings.TrimSpace(c.FormValue("client_id"))
	clientSecret := strings.TrimSpace(c.FormValue("client_secret"))
	if clientID == "" || clientSecret == "" {
		return s.redirectWithError(c, "/dashboard/connectors/services", "Client ID and secret are required.")
	}
	encID, err := secrets.EncryptWithSystemKey(clientID, s.systemKey)
	if err != nil {
		return s.redirectWithError(c, "/dashboard/connectors/services", "Failed to store credentials.")
	}
	encSec, err := secrets.EncryptWithSystemKey(clientSecret, s.systemKey)
	if err != nil {
		return s.redirectWithError(c, "/dashboard/connectors/services", "Failed to store credentials.")
	}
	if err := s.db.UpsertServiceProviderConfig(c.Request().Context(), db.ServiceProviderConfig{
		ID: uuid.New().String(), WorkspaceID: w.ID, Provider: provider,
		EncryptedClientID: encID, EncryptedClientSecret: encSec,
	}); err != nil {
		return s.redirectWithError(c, "/dashboard/connectors/services", "Failed to save credentials.")
	}
	return c.Redirect(http.StatusSeeOther, "/dashboard/connectors/services")
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
func (s *Server) buildConsentURL(c echo.Context, w *db.Workspace, provider, label string) (string, error) {
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
	payload := strings.Join([]string{w.ID, provider, label, nonce}, "~")
	state := signState(s.systemKey, payload, time.Now())
	return oauth.ConsentURL(clientID, s.callbackURL(c, provider), state, child.DefaultScopes), nil
}

func (s *Server) handleConnectService(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	provider := c.Param("provider")
	label := strings.TrimSpace(c.FormValue("account_label"))
	if label == "" {
		label = "account"
	}
	url, err := s.buildConsentURL(c, w, provider, label)
	if err != nil {
		return s.redirectWithError(c, "/dashboard/connectors/services", err.Error())
	}
	return c.Redirect(http.StatusSeeOther, url)
}

// handleConnectAPIKey stores a static API-key connection for an api_key provider. No OAuth
// app, no redirect: the pasted key is encrypted into service_connections directly.
func (s *Server) handleConnectAPIKey(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	provider := c.Param("provider")
	prov, ok := s.connectors.ProviderByName(provider)
	if !ok || !prov.IsAPIKey() {
		return s.redirectWithError(c, "/dashboard/connectors/services", "Unknown or non-API-key provider.")
	}
	apiKey := strings.TrimSpace(c.FormValue("api_key"))
	if apiKey == "" {
		return s.redirectWithError(c, "/dashboard/connectors/services", "API key is required.")
	}
	label := strings.TrimSpace(c.FormValue("account_label"))
	if label == "" {
		label = "default"
	}

	extra := map[string]string{}
	for _, ci := range prov.ConnectInputs {
		v := strings.TrimSpace(c.FormValue(ci.Key))
		if ci.Required && v == "" {
			return s.redirectWithError(c, "/dashboard/connectors/services", ci.Label+" is required.")
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
		if b, _ := json.Marshal(extra); b != nil {
			extraJSON = string(b)
		}
	}

	enc, err := secrets.EncryptWithSystemKey(apiKey, s.systemKey)
	if err != nil {
		return s.redirectWithError(c, "/dashboard/connectors/services", "Failed to store the API key.")
	}
	if err := s.db.InsertServiceConnection(c.Request().Context(), db.ServiceConnection{
		ID: uuid.New().String(), WorkspaceID: w.ID, Provider: provider,
		AccountLabel: label, AccountIdentity: label,
		EncryptedAccessToken: enc, Status: "ACTIVE", Extra: extraJSON,
	}); err != nil {
		return s.redirectWithError(c, "/dashboard/connectors/services", "Failed to save the connection: "+err.Error())
	}
	return c.Redirect(http.StatusSeeOther, "/dashboard/connectors/services")
}

func (s *Server) handleOAuthCallback(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	provider := c.Param("provider")
	ctx := c.Request().Context()

	if errParam := c.QueryParam("error"); errParam != "" {
		return s.redirectWithError(c, "/dashboard/connectors/services", "Authorization was denied: "+errParam)
	}
	code := c.QueryParam("code")
	payload, ok := verifyState(s.systemKey, c.QueryParam("state"), time.Now())
	if !ok || code == "" {
		return s.redirectWithError(c, "/dashboard/connectors/services", "Invalid or expired authorization request; try again.")
	}
	parts := strings.Split(payload, "~")
	if len(parts) != 4 || parts[0] != w.ID || parts[1] != provider {
		return s.redirectWithError(c, "/dashboard/connectors/services", "Authorization did not match this workspace; try again.")
	}
	label := parts[2]

	prov, ok := s.connectors.ProviderByName(provider)
	if !ok {
		return s.redirectWithError(c, "/dashboard/connectors/services", "Unknown provider.")
	}
	// authProv is the OAuth parent when this provider is aliased (e.g. google_drive → google),
	// else the provider itself. It governs endpoints, token settings, and the app-credentials
	// lookup key; `prov` (the child) still governs scopes/post_connect/expiry below.
	authProv, ok := s.connectors.OAuthProvider(provider)
	if !ok {
		return s.redirectWithError(c, "/dashboard/connectors/services", "Unknown provider.")
	}
	cfg, _ := s.db.GetServiceProviderConfig(ctx, w.ID, authProv.Name)
	if cfg == nil {
		return s.redirectWithError(c, "/dashboard/connectors/services", "Missing OAuth app credentials.")
	}
	clientID, _ := secrets.DecryptWithSystemKey(cfg.EncryptedClientID, s.systemKey)
	clientSecret, _ := secrets.DecryptWithSystemKey(cfg.EncryptedClientSecret, s.systemKey)

	oauth := connectors.OAuthClient{}
	ts, err := oauth.ExchangeCode(ctx, authProv, clientID, clientSecret, code, s.callbackURL(c, provider))
	if err != nil {
		return s.redirectWithError(c, "/dashboard/connectors/services", "Token exchange failed: "+err.Error())
	}
	identity, _ := oauth.FetchIdentity(ctx, authProv, ts.AccessToken)

	// Post-connect resolution (e.g. Jira cloud id) + token_extra fields (e.g. Salesforce
	// instance_url) → merged into extra, exposed to request templates as {{conn.<key>}}.
	extraMap := map[string]string{}
	if prov.PostConnect != "" {
		if vals, perr := connectors.RunPostConnect(ctx, prov.PostConnect, nil, ts.AccessToken); perr != nil {
			return s.redirectWithError(c, "/dashboard/connectors/services", "Connected, but setup failed: "+perr.Error())
		} else {
			for k, v := range vals {
				extraMap[k] = v
			}
		}
	}
	for k, v := range ts.Extra { // token_extra fields (e.g. Salesforce instance_url)
		extraMap[k] = v
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
		return s.redirectWithError(c, "/dashboard/connectors/services",
			"Connected, but saving failed (a connection labeled '"+label+"' may already exist): "+err.Error())
	}
	return c.Redirect(http.StatusSeeOther, "/dashboard/connectors/services")
}

func (s *Server) handleDeleteServiceConnection(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	id := c.Param("id")
	ctx := c.Request().Context()
	// Ownership check: the connection must belong to the active workspace.
	conn, err := s.db.GetServiceConnection(ctx, id)
	if err != nil || conn == nil || conn.WorkspaceID != w.ID {
		return s.redirectWithError(c, "/dashboard/connectors/services", "Connection not found.")
	}
	if err := s.db.DeleteServiceConnection(ctx, id); err != nil {
		return s.redirectWithError(c, "/dashboard/connectors/services", "Failed to delete connection.")
	}
	return c.Redirect(http.StatusSeeOther, "/dashboard/connectors/services")
}
