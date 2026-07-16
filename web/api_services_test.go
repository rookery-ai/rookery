package web

import (
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
