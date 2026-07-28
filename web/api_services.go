package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/secrets"
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

type apiServiceConnectInput struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Hint     string `json:"hint"`
	Required bool   `json:"required"`
}

type apiServiceProvider struct {
	Name          string                   `json:"name"`
	Label         string                   `json:"label"`
	Category      string                   `json:"category"`
	Kind          string                   `json:"kind"`
	SetupURL      string                   `json:"setup_url"`
	SetupSteps    []string                 `json:"setup_steps"`
	HasCreds      bool                     `json:"has_creds"`
	ConnectInputs []apiServiceConnectInput `json:"connect_inputs"`
	Connections   []apiServiceConnection   `json:"connections"`
	// ActionCount lets the UI show a count and hide the actions entry point at
	// zero without a second fetch. The actions themselves stay OFF this payload:
	// it loads on every visit to the connections page, and ~214 actions with
	// their JSON schemas is a real regression on that critical path.
	ActionCount   int                      `json:"action_count"`
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
			if p.PastesCredential() {
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

		credsProvider := provider
		if op, ok := s.connectors.OAuthProvider(provider); ok && op.Name != provider {
			credsProvider = op.Name
		}
		cfgForCreds, _ := s.db.GetServiceProviderConfig(ctx, w.ID, credsProvider)

		out = append(out, apiServiceProvider{
			Name:          provider,
			Label:         label,
			Category:      category,
			Kind:          kind,
			SetupURL:      setupURL,
			SetupSteps:    setupSteps,
			HasCreds:      cfgForCreds != nil,
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
	if !ok || !prov.PastesCredential() {
		return jsonErr(c, http.StatusNotFound, "not_found", "unknown or non-API-key provider: "+provider)
	}

	var req apiConnectAPIKeyRequest
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	apiKey := strings.TrimSpace(req.Key)
	if apiKey == "" {
		return jsonErr(c, http.StatusBadRequest, "missing_field", "key is required")
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
