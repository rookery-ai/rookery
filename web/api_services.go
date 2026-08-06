package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/ilijad1/rookery/internal/db"
	"github.com/ilijad1/rookery/internal/publicurl"
	"github.com/ilijad1/rookery/internal/secrets"
	"github.com/labstack/echo/v4"
)

// registerServicesAPI registers the JSON service-connection (OAuth/API-key)
// endpoints on the given group (already guarded by requireOwnerAPI +
// requireActiveWorkspaceAPI + requireSetupCompleteAPI). Business logic lives
// in web/handlers_services.go (shared with the template handlers) — this
// file only adapts it to the JSON envelope. The OAuth callback route stays a
// browser-redirect route and is untouched here.
func (s *Server) registerServicesAPI(g *echo.Group) {
	g.GET("/services", s.apiListServices)
	g.GET("/services/:provider/actions", s.apiListProviderActions)
	g.POST("/services/:provider/creds", s.apiSaveProviderCreds)
	g.POST("/services/:provider/connect", s.apiConnectService)
	g.POST("/services/:provider/apikey", s.apiConnectAPIKey)
	g.DELETE("/services/:id", s.apiDeleteServiceConnection)
}

// ── DTOs ─────────────────────────────────────────────────────────────────────

type apiServiceConnection struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Identity string `json:"identity"`
	Status   string `json:"status"`
}

// apiPreflightProblem is a publicurl.Problem flattened for JSON. Severity is a
// string ("hard"/"soft") rather than the Go int so the SPA never depends on our
// enum ordering.
type apiPreflightProblem struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Fix      string `json:"fix"`
}

type apiServiceConnectInput struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Hint     string `json:"hint"`
	Required bool   `json:"required"`
}

// apiOAuthCreds names the OAuth app's two credential fields the way the provider's own
// developer console does, so the connect form stops asking for a "Client ID" the user
// cannot find on the page they are looking at.
//
// A VALUE, not a pointer, so it always serializes as an object rather than null —
// matching the convention connections, connect_inputs and setup_steps already
// follow. TestChildProvidersInheritParentCredLabels (api_services_oauth_labels_test.go)
// asserts this field specifically never serializes as null. Empty fields are the
// SPA's signal to fall back.
type apiOAuthCreds struct {
	IDLabel     string `json:"id_label"`
	IDHint      string `json:"id_hint"`
	SecretLabel string `json:"secret_label"`
	SecretHint  string `json:"secret_hint"`
}

// setupMode names the two shapes the connect wizard's guidance takes. Kept as a
// function rather than an inline conditional so both the handler and its tests
// spell the values one way.
func setupMode(hasCreds bool) string {
	if hasCreds {
		return "update"
	}
	return "create"
}

type apiServiceProvider struct {
	Name       string   `json:"name"`
	Label      string   `json:"label"`
	Category   string   `json:"category"`
	Kind       string   `json:"kind"`
	SetupURL   string   `json:"setup_url"`
	SetupSteps []string `json:"setup_steps"`
	// KeyLabel/KeyHint name the credential the paste form asks for. Providers say
	// very different things here — "AdGuard Home password", "Nextcloud app password",
	// "Todoist API token" — and the wizard used to hardcode "<Provider> API key",
	// which was simply wrong for the ones that do not take an API key at all.
	KeyLabel string `json:"key_label"`
	KeyHint  string `json:"key_hint"`
	// OAuthCreds is the OAuth-path analogue of KeyLabel/KeyHint. Resolved through
	// auth_parent — see the assignment below.
	OAuthCreds apiOAuthCreds `json:"oauth_creds"`
	HasCreds   bool          `json:"has_creds"`
	// AppProvider/AppLabel name the provider that OWNS the OAuth application this
	// one authenticates through — itself, or its auth_parent when aliased. The
	// wizard needs them to say WHICH application to update ("your Google (Gmail)
	// app") rather than naming the service the user just clicked, which does not
	// exist as an app in the provider's console.
	AppProvider string `json:"app_provider"`
	AppLabel    string `json:"app_label"`
	// SetupMode is "create" when no credentials are stored yet and "update" when
	// they are. An aliased child inherits its parent's credentials, so it reports
	// "update" on the very first visit — which is correct: the user must edit the
	// existing application, not create a second one.
	SetupMode     string                   `json:"setup_mode"`
	ConnectInputs []apiServiceConnectInput `json:"connect_inputs"`
	RedirectURI   string                   `json:"redirect_uri"`
	Preflight     []apiPreflightProblem    `json:"preflight"`
	Connections   []apiServiceConnection   `json:"connections"`
	// ActionCount lets the UI show a count and hide the actions entry point at
	// zero without a second fetch. The actions themselves stay OFF this payload:
	// it loads on every visit to the connections page, and 272 actions across
	// 45 providers with their JSON schemas is a real regression on that critical path.
	ActionCount int `json:"action_count"`
}

type apiServicesListResponse struct {
	Providers []apiServiceProvider `json:"providers"`
}

// apiConnectorAction is one curated action a provider exposes. Deliberately a
// SUBSET of connectors.Action: Request (method/URL/query/body templates) and
// ResponseExtract are internal plumbing — noise to a reader and a needless
// widening of what the API discloses about how requests are built.
type apiConnectorAction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Mutating    bool            `json:"mutating"`
	PublicWrite bool            `json:"public_write"`
	Params      json.RawMessage `json:"params"`
}

type apiProviderActionsResponse struct {
	Actions []apiConnectorAction `json:"actions"`
}

type apiSaveProviderCredsRequest struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

type apiConnectServiceRequest struct {
	Label string `json:"label"`
	// Inputs carries the provider's connect_inputs on the OAuth path. They ride the
	// signed state through the provider and are stored in extra at callback — Google
	// Ads needs a developer token that cannot be discovered from any API.
	Inputs map[string]string `json:"inputs"`
}

type apiConnectServiceResponse struct {
	RedirectURL string `json:"redirect_url"`
}

type apiConnectAPIKeyRequest struct {
	Key    string            `json:"key"`
	Label  string            `json:"label"`
	Inputs map[string]string `json:"inputs"`
}

// ── Handlers ────────────────────────────────────────────────────────────────

// apiListServices mirrors showServices's view data as JSON. GET
// /api/v1/services → {"providers":[...]}. Never includes client_secret, api
// keys, or tokens — ids/labels/identities/status only.
func (s *Server) apiListServices(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	ctx := c.Request().Context()
	all, _ := s.db.ListServiceConnections(ctx, w.ID)

	providers := s.serviceProviders()
	// Resolved ONCE: there are 45 providers and callbackURL resolves again
	// internally, so resolving per-iteration would mean ~90 GetSystemSetting
	// reads on every page load.
	base, _ := s.resolvePublicURL(c)
	out := make([]apiServiceProvider, 0, len(providers))
	for _, provider := range providers {
		conns := make([]apiServiceConnection, 0)
		for _, cn := range all {
			if cn.Provider != provider {
				continue
			}
			conns = append(conns, apiServiceConnection{
				ID: cn.ID, Label: cn.AccountLabel, Identity: cn.AccountIdentity, Status: cn.Status,
			})
		}

		label, setupURL, setupSteps, kind := provider, "", []string{}, "oauth"
		keyLabel, keyHint := "", ""
		// A provider with no declared category groups under "Other" rather than
		// dropping out of the page entirely.
		category := "Other"
		connectInputs := make([]apiServiceConnectInput, 0)
		if p, ok := s.connectors.ProviderByName(provider); ok {
			if p.Label != "" {
				label = p.Label
			}
			if p.Category != "" {
				category = p.Category
			}
			setupURL, setupSteps = p.SetupURL, p.SetupSteps
			if setupSteps == nil {
				setupSteps = []string{}
			}
			// session_exchange (Bluesky) is an OAuth-less paste-a-credential flow like
			// api_key, so it renders the same connect form. They differ only in what
			// happens to the stored value at request time, which the UI does not care about.
			keyLabel, keyHint = p.Auth.KeyLabel, p.Auth.KeyHint
			switch {
			case p.IsKeyless():
				// A third kind, not a variant of api_key: the wizard must render no
				// credential field at all, and there is no redirect URI or preflight
				// because nothing ever leaves the browser.
				kind = "keyless"
				if p.Auth.SetupURL != "" {
					setupURL = p.Auth.SetupURL
				}
			case p.PastesCredential():
				kind = "api_key"
				if p.Auth.SetupURL != "" {
					setupURL = p.Auth.SetupURL
				}
			}
			for _, ci := range p.ConnectInputs {
				connectInputs = append(connectInputs, apiServiceConnectInput{
					Key: ci.Key, Label: ci.Label, Hint: ci.Hint, Required: ci.Required,
				})
			}
		}

		credsProvider, appLabel := provider, label
		oauthCreds := apiOAuthCreds{}
		if op, ok := s.connectors.OAuthProvider(provider); ok {
			if op.Name != provider {
				credsProvider = op.Name
				appLabel = op.Label
				if appLabel == "" {
					appLabel = op.Name
				}
			}
			// Read the labels off the RESOLVED provider, never off p: a child
			// (teams → outlook, google_calendar → google) has no OAuth app of its own,
			// and the fields it is being asked for belong to the parent's app.
			oauthCreds = apiOAuthCreds{
				IDLabel:     op.OAuthCreds.IDLabel,
				IDHint:      op.OAuthCreds.IDHint,
				SecretLabel: op.OAuthCreds.SecretLabel,
				SecretHint:  op.OAuthCreds.SecretHint,
			}
		}
		cfgForCreds, _ := s.db.GetServiceProviderConfig(ctx, w.ID, credsProvider)

		// Only OAuth providers have a redirect URI; an api_key provider never
		// leaves the browser, so emitting one would be a false instruction.
		redirectURI, preflight := "", []apiPreflightProblem{}
		if kind == "oauth" {
			// Scoped to the OAuth APPLICATION, not the service: an aliased child
			// (google_calendar → google) authenticates through the parent's app, so
			// the parent's URI is the one that must be registered. See oauthAppName.
			redirectURI = base + "/dashboard/connectors/services/callback/" + credsProvider
			preflight = toAPIPreflight(publicurl.Check(base, s.connectors.RedirectPolicy(provider)))
		}

		out = append(out, apiServiceProvider{
			Name:     provider,
			Label:    label,
			Category: category,
			Kind:     kind,
			SetupURL: setupURL,
			// SetupSteps ship with {{redirect_uri}} UNSUBSTITUTED on purpose. The
			// wizard splits on the token and renders the URI as copyable code;
			// substituting here would embed a bare URL that Linkify turns into a
			// link, and clicking our own callback route without a state parameter
			// only ever produces "Invalid or expired authorization request".
			SetupSteps:    setupSteps,
			KeyLabel:      keyLabel,
			KeyHint:       keyHint,
			OAuthCreds:    oauthCreds,
			RedirectURI:   redirectURI,
			Preflight:     preflight,
			HasCreds:      cfgForCreds != nil,
			AppProvider:   credsProvider,
			AppLabel:      appLabel,
			SetupMode:     setupMode(cfgForCreds != nil),
			ConnectInputs: connectInputs,
			Connections:   conns,
			ActionCount:   len(s.connectors.Actions(provider)),
		})
	}

	return c.JSON(http.StatusOK, apiServicesListResponse{Providers: out})
}

// apiListProviderActions lists the curated actions a provider exposes. GET
// /api/v1/services/:provider/actions → {"actions":[...]}; unknown provider → 404.
// Read-only over embedded manifest data: no DB access and nothing
// workspace-scoped, so an unconnected provider lists its actions too — "what can
// this do for me" is the strongest reason to connect in the first place.
func (s *Server) apiListProviderActions(c echo.Context) error {
	provider := c.Param("provider")
	if _, ok := s.connectors.ProviderByName(provider); !ok {
		return jsonErr(c, http.StatusNotFound, "not_found", "unknown provider: "+provider)
	}

	acts := s.connectors.Actions(provider)
	out := make([]apiConnectorAction, 0, len(acts))
	for _, a := range acts {
		// A manifest with no params: block compiles to the literal bytes `null`,
		// not to empty — normalize so the client can always read .properties.
		params := a.Params
		if len(params) == 0 || string(params) == "null" {
			params = json.RawMessage(`{}`)
		}
		out = append(out, apiConnectorAction{
			Name:        a.Name,
			Description: a.Description,
			Mutating:    a.Mutating,
			PublicWrite: a.PublicWrite,
			Params:      params,
		})
	}
	return c.JSON(http.StatusOK, apiProviderActionsResponse{Actions: out})
}

// apiSaveProviderCreds ports handleSaveProviderCreds. POST
// /api/v1/services/:provider/creds {client_id,client_secret} → 200 {"ok":true}.
func (s *Server) apiSaveProviderCreds(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	provider := c.Param("provider")

	var req apiSaveProviderCredsRequest
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	clientID := strings.TrimSpace(req.ClientID)
	clientSecret := strings.TrimSpace(req.ClientSecret)
	if clientID == "" || clientSecret == "" {
		return jsonErr(c, http.StatusBadRequest, "missing_field", "client_id and client_secret are required")
	}

	encID, err := secrets.EncryptWithSystemKey(clientID, s.systemKey)
	if err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "failed to store credentials")
	}
	encSec, err := secrets.EncryptWithSystemKey(clientSecret, s.systemKey)
	if err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "failed to store credentials")
	}
	if err := s.db.UpsertServiceProviderConfig(c.Request().Context(), db.ServiceProviderConfig{
		ID: uuid.New().String(), WorkspaceID: w.ID, Provider: provider,
		EncryptedClientID: encID, EncryptedClientSecret: encSec,
	}); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "failed to save credentials")
	}
	s.audit.Log(w.ID, "save_provider_creds", "provider:"+provider, "", c.RealIP())
	return c.JSON(http.StatusOK, apiOKResponse{OK: true})
}

// apiConnectService ports handleConnectService, returning the consent URL as
// JSON instead of 302-redirecting. POST /api/v1/services/:provider/connect
// {label?} → 200 {"redirect_url":"..."}; unknown provider → 404; missing
// saved OAuth creds → 400.
func (s *Server) apiConnectService(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	provider := c.Param("provider")

	var req apiConnectServiceRequest
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	label := strings.TrimSpace(req.Label)
	if label == "" {
		label = "account"
	}

	// Required connect_inputs are validated here rather than at callback: a user who
	// completes consent only to be told a field was missing has to redo the whole flow.
	if prov, ok := s.connectors.ProviderByName(provider); ok {
		for _, ci := range prov.ConnectInputs {
			if ci.Required && strings.TrimSpace(req.Inputs[ci.Key]) == "" {
				return jsonErr(c, http.StatusBadRequest, "missing_field", ci.Label+" is required.")
			}
		}
	}

	redirectURL, err := s.buildConsentURL(c, w, provider, label, req.Inputs)
	if err != nil {
		var cerr *consentURLError
		if errors.As(err, &cerr) {
			switch cerr.Code {
			case "unknown_provider":
				return jsonErr(c, http.StatusNotFound, "not_found", cerr.Msg)
			case "missing_creds":
				return jsonErr(c, http.StatusBadRequest, "missing_creds", cerr.Msg)
			default:
				return jsonErr(c, http.StatusInternalServerError, "internal", cerr.Msg)
			}
		}
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	return c.JSON(http.StatusOK, apiConnectServiceResponse{RedirectURL: redirectURL})
}

// apiConnectAPIKey ports handleConnectAPIKey. POST
// /api/v1/services/:provider/apikey {key, inputs:{...}} → 200 {"ok":true};
// unknown/non-api-key provider → 404; missing key/required input → 400.
func (s *Server) apiConnectAPIKey(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	provider := c.Param("provider")

	// session_exchange providers connect through this same paste-a-credential endpoint;
	// gating on IsAPIKey alone would make Bluesky unconnectable despite its form rendering.
	prov, ok := s.connectors.ProviderByName(provider)
	if !ok || !(prov.PastesCredential() || prov.IsKeyless()) {
		return jsonErr(c, http.StatusNotFound, "not_found", "unknown or non-API-key provider: "+provider)
	}

	var req apiConnectAPIKeyRequest
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	apiKey := strings.TrimSpace(req.Key)
	// A keyless provider has nothing to paste. Every other kind still must.
	if apiKey == "" && !prov.IsKeyless() {
		return jsonErr(c, http.StatusBadRequest, "missing_field", "key is required")
	}
	if prov.IsKeyless() {
		// Two keyless connections to one provider would produce two identical tool
		// sets that ToolDefs slugs by label — harmless but useless, and confusing on
		// the page. Reject rather than relying on the user not to create it.
		existing, lerr := s.db.ListServiceConnections(c.Request().Context(), w.ID)
		if lerr != nil {
			return jsonErr(c, http.StatusInternalServerError, "internal", lerr.Error())
		}
		for _, e := range existing {
			if e.Provider == provider {
				return jsonErr(c, http.StatusBadRequest, "already_connected",
					prov.Label+" needs no credential, so one connection is all there is — it is already connected.")
			}
		}
	}
	label := strings.TrimSpace(req.Label)

	_, userErrMsg, err := s.connectAPIKeyCore(c.Request().Context(), w, prov, provider, apiKey, label, req.Inputs)
	if userErrMsg != "" {
		return jsonErr(c, http.StatusBadRequest, "missing_field", userErrMsg)
	}
	if err != nil {
		// DB-write/encryption failures are treated as internal errors here, same
		// as every sibling handler (previously this specific path returned 400
		// save_failed — an inconsistency fixed as part of this extraction).
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	s.audit.Log(w.ID, "connect_service_apikey", "provider:"+provider, "", c.RealIP())
	return c.JSON(http.StatusOK, apiOKResponse{OK: true})
}

// apiDeleteServiceConnection ports handleDeleteServiceConnection. DELETE
// /api/v1/services/:id → 200 {"ok":true}; unknown/foreign id → 404.
func (s *Server) apiDeleteServiceConnection(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	id := c.Param("id")
	ctx := c.Request().Context()

	conn, err := s.db.GetServiceConnection(ctx, id)
	if err != nil || conn == nil || conn.WorkspaceID != w.ID {
		return jsonErr(c, http.StatusNotFound, "not_found", "connection not found")
	}
	if err := s.db.DeleteServiceConnection(ctx, id); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "failed to delete connection")
	}
	s.audit.Log(w.ID, "disconnect_service", "conn:"+id, "", c.RealIP())
	return c.JSON(http.StatusOK, apiOKResponse{OK: true})
}

func toAPIPreflight(ps []publicurl.Problem) []apiPreflightProblem {
	out := make([]apiPreflightProblem, 0, len(ps))
	for _, p := range ps {
		sev := "soft"
		if p.Severity == publicurl.SeverityHard {
			sev = "hard"
		}
		out = append(out, apiPreflightProblem{sev, p.Code, p.Message, p.Fix})
	}
	return out
}
