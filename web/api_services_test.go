package web

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestAPIServices_GET_Unauthenticated(t *testing.T) {
	s, _ := newAPITestServer(t)
	rec := doJSON(t, s, http.MethodGet, "/api/v1/services", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAPIServices_GET_Authed_ListsGoogle(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/services", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !contains(body, `"name":"google"`) {
		t.Fatalf("expected response to contain google provider, got: %s", body)
	}
	for _, key := range []string{`"kind"`, `"setup_url"`, `"has_creds"`, `"connect_inputs"`, `"connections"`, `"label"`, `"setup_steps"`} {
		if !contains(body, key) {
			t.Fatalf("expected response to contain field %s, got: %s", key, body)
		}
	}
	if contains(body, `"connections":null`) || contains(body, `"connect_inputs":null`) || contains(body, `"setup_steps":null`) {
		t.Fatalf("array fields must serialize as [] not null: %s", body)
	}
	if contains(body, "client_secret") || contains(body, "encrypted") || contains(body, "access_token") {
		t.Fatalf("response must never leak credential material: %s", body)
	}
}

func TestAPIServices_CONNECT_NoSavedCreds(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/services/google/connect", map[string]any{}, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAPIServices_CONNECT_UnknownProvider(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/services/not-a-real-provider/connect", map[string]any{}, cookies)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), "not_found") {
		t.Fatalf("expected not_found code, got: %s", rec.Body.String())
	}
}

func TestAPIServices_DELETE_UnknownID(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodDelete, "/api/v1/services/not-a-real-id", nil, cookies)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), "not_found") {
		t.Fatalf("expected not_found code, got: %s", rec.Body.String())
	}
}

func TestAPIServices_CredsSaveThenConnect_ReturnsConsentURL(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/services/google/creds", map[string]any{
		"client_id":     "test-client-id",
		"client_secret": "test-client-secret",
	}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 saving creds, got %d: %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("expected ok:true, got: %s", rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodPost, "/api/v1/services/google/connect", map[string]any{
		"label": "my-account",
	}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 connecting, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !contains(body, "accounts.google.com/o/oauth2/v2/auth") {
		t.Fatalf("expected consent URL to contain google's authorize endpoint, got: %s", body)
	}
	if !contains(body, "state=") {
		t.Fatalf("expected consent URL to contain a state param, got: %s", body)
	}
	if contains(body, "test-client-secret") {
		t.Fatalf("response must never leak the client secret, got: %s", body)
	}
}

func TestAPIServices_CREDS_MissingFields(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/services/google/creds", map[string]any{
		"client_id": "only-id",
	}, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAPIServices_APIKEY_UnknownProvider(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/services/not-a-real-provider/apikey", map[string]any{
		"key": "sk-test",
	}, cookies)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAPIServices_APIKEY_OpenAI_HappyPath(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/services/openai/apikey", map[string]any{
		"key": "sk-test-key",
	}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("expected ok:true, got: %s", rec.Body.String())
	}
	if contains(rec.Body.String(), "sk-test-key") {
		t.Fatalf("response must never leak the api key, got: %s", rec.Body.String())
	}
}

// TestAPIServices_APIKEY_ReconnectSameLabelPreservesID is the SP5 final
// review fix exercised end-to-end through the HTTP API: connecting the same
// provider+label twice (a reconnect) must succeed both times — not fail the
// (workspace_id, provider, account_label) UNIQUE constraint — and the
// connection's id must stay the same across the reconnect, since
// agent_connections bindings reference it by id.
func TestAPIServices_APIKEY_ReconnectSameLabelPreservesID(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	connect := func(key string) apiServicesListResponse {
		rec := doJSON(t, s, http.MethodPost, "/api/v1/services/openai/apikey", map[string]any{
			"key":   key,
			"label": "work",
		}, cookies)
		if rec.Code != http.StatusOK {
			t.Fatalf("connect: expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		rec = doJSON(t, s, http.MethodGet, "/api/v1/services", nil, cookies)
		if rec.Code != http.StatusOK {
			t.Fatalf("list: expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var out apiServicesListResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v: %s", err, rec.Body.String())
		}
		return out
	}

	findOpenAIConn := func(resp apiServicesListResponse) *apiServiceConnection {
		for _, p := range resp.Providers {
			if p.Name != "openai" {
				continue
			}
			if len(p.Connections) == 0 {
				return nil
			}
			return &p.Connections[0]
		}
		return nil
	}

	first := findOpenAIConn(connect("sk-first-key"))
	if first == nil {
		t.Fatal("expected one openai connection after first connect")
	}

	second := findOpenAIConn(connect("sk-second-key"))
	if second == nil {
		t.Fatal("expected one openai connection after reconnect")
	}
	if second.ID != first.ID {
		t.Fatalf("reconnect must keep the same connection id: first=%q second=%q", first.ID, second.ID)
	}
	if second.Status != "ACTIVE" {
		t.Fatalf("reconnect must bring status back to ACTIVE: %q", second.Status)
	}

	// Still exactly one openai connection, not two rows.
	for _, p := range connect("sk-second-key").Providers {
		if p.Name == "openai" && len(p.Connections) != 1 {
			t.Fatalf("expected exactly 1 openai connection after reconnects, got %d", len(p.Connections))
		}
	}
}

func TestAPIServices_ACTIONS_Unauthenticated(t *testing.T) {
	s, _ := newAPITestServer(t)
	rec := doJSON(t, s, http.MethodGet, "/api/v1/services/github/actions", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAPIServices_ACTIONS_UnknownProvider(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/services/not-a-real-provider/actions", nil, cookies)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), "not_found") {
		t.Fatalf("expected not_found code, got: %s", rec.Body.String())
	}
}

func TestAPIServices_ACTIONS_ListsGithubActions(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/services/github/actions", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !contains(body, `"github_search_issues"`) {
		t.Fatalf("expected github_search_issues in response, got: %s", body)
	}
	for _, key := range []string{`"description"`, `"mutating"`, `"public_write"`, `"params"`} {
		if !contains(body, key) {
			t.Fatalf("expected response to contain field %s, got: %s", key, body)
		}
	}
	if contains(body, `"actions":null`) {
		t.Fatalf("actions must serialize as [] not null: %s", body)
	}

	// Decode and verify the params contract itself, not just that the substring
	// "params" appears somewhere in the body (which would pass even if it only
	// showed up inside a description string). The whole Go-to-TypeScript seam
	// (ConnectorActionParams in web/ui/src/lib/connections.ts) assumes every
	// action's params is a JSON Schema object with properties+required — this
	// is the one place that assumption is checked against the real payload.
	var decoded apiProviderActionsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decoding actions response: %v: %s", err, body)
	}
	var searchIssues *apiConnectorAction
	for i := range decoded.Actions {
		if decoded.Actions[i].Name == "github_search_issues" {
			searchIssues = &decoded.Actions[i]
			break
		}
	}
	if searchIssues == nil {
		t.Fatalf("github_search_issues not found among decoded actions: %s", body)
	}
	var params struct {
		Type       string          `json:"type"`
		Properties json.RawMessage `json:"properties"`
		Required   []string        `json:"required"`
	}
	if err := json.Unmarshal(searchIssues.Params, &params); err != nil {
		t.Fatalf("decoding github_search_issues params: %v: %s", err, string(searchIssues.Params))
	}
	if params.Type != "object" {
		t.Fatalf("expected params.type == object, got %q", params.Type)
	}
	var props map[string]json.RawMessage
	if err := json.Unmarshal(params.Properties, &props); err != nil {
		t.Fatalf("params.properties did not decode as an object: %v: %s", err, string(searchIssues.Params))
	}
	if _, ok := props["query"]; !ok {
		t.Fatalf("expected params.properties.query, got: %s", string(searchIssues.Params))
	}
	if len(params.Required) == 0 {
		t.Fatalf("expected a non-empty params.required, got: %s", string(searchIssues.Params))
	}
}

// The action manifests are the only place request templates live. Leaking them
// through this endpoint would disclose how every request is built, for no reader
// benefit — so sweep EVERY provider rather than trusting one spot check.
func TestAPIServices_ACTIONS_NeverLeaksRequestPlumbing(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	listRec := doJSON(t, s, http.MethodGet, "/api/v1/services", nil, cookies)
	var list struct {
		Providers []struct {
			Name        string `json:"name"`
			ActionCount int    `json:"action_count"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decoding services list: %v", err)
	}
	if len(list.Providers) == 0 {
		t.Fatal("expected at least one provider")
	}

	for _, p := range list.Providers {
		rec := doJSON(t, s, http.MethodGet, "/api/v1/services/"+p.Name+"/actions", nil, cookies)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d: %s", p.Name, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		for _, banned := range []string{`"request":`, `"response_extract":`, `"body_builder":`} {
			if contains(body, banned) {
				t.Fatalf("%s: response leaked request plumbing %s: %s", p.Name, banned, body)
			}
		}
		if contains(body, `"params":null`) {
			t.Fatalf("%s: params must normalize to {} not null: %s", p.Name, body)
		}
	}
}

// action_count exists so the UI can render a count and hide the entry button at
// zero without a second fetch. If it drifts from the real list the button lies.
func TestAPIServices_ActionCountMatchesActionsEndpoint(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	listRec := doJSON(t, s, http.MethodGet, "/api/v1/services", nil, cookies)
	var list struct {
		Providers []struct {
			Name        string `json:"name"`
			ActionCount int    `json:"action_count"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decoding services list: %v", err)
	}

	for _, p := range list.Providers {
		rec := doJSON(t, s, http.MethodGet, "/api/v1/services/"+p.Name+"/actions", nil, cookies)
		var got struct {
			Actions []struct {
				Name string `json:"name"`
			} `json:"actions"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("%s: decoding actions: %v", p.Name, err)
		}
		if p.ActionCount != len(got.Actions) {
			t.Fatalf("%s: action_count=%d but endpoint returned %d actions",
				p.Name, p.ActionCount, len(got.Actions))
		}
	}
}
