package web

import (
	"net/http"
	"testing"
)

func TestAPISearchKeysConfiguredStateAndDelete(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	// Nothing configured yet.
	rec := doJSON(t, s, http.MethodGet, "/api/v1/search-keys", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rec.Code, rec.Body.String())
	}
	if contains(rec.Body.String(), "true") {
		t.Fatalf("expected both unconfigured: %s", rec.Body.String())
	}

	// Set the brave key.
	rec = doJSON(t, s, http.MethodPut, "/api/v1/search-keys",
		map[string]string{"provider": "brave", "key": "sekrit-brave-key"}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rec.Code, rec.Body.String())
	}

	// Now brave reports configured; the value is never returned anywhere.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/search-keys", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("get after put: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !contains(body, `"brave":true`) || !contains(body, `"tavily":false`) {
		t.Fatalf("expected brave configured, tavily not: %s", body)
	}
	if contains(body, "sekrit-brave-key") {
		t.Fatalf("GET must never leak the key value: %s", body)
	}

	// Also confirm the raw secret list never surfaces the value (belt-and-braces —
	// search keys are stored via the ordinary secrets service).
	rec = doJSON(t, s, http.MethodGet, "/api/v1/secrets", nil, cookies)
	if contains(rec.Body.String(), "sekrit-brave-key") {
		t.Fatalf("secrets list must never leak the key value: %s", rec.Body.String())
	}

	// Delete clears it.
	rec = doJSON(t, s, http.MethodDelete, "/api/v1/search-keys/brave", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodGet, "/api/v1/search-keys", nil, cookies)
	if !contains(rec.Body.String(), `"brave":false`) {
		t.Fatalf("expected brave unconfigured after delete: %s", rec.Body.String())
	}

	// Delete is idempotent — deleting an already-unconfigured provider still 200s.
	rec = doJSON(t, s, http.MethodDelete, "/api/v1/search-keys/brave", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("idempotent delete: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAPISearchKeysInvalidProviderRejected(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPut, "/api/v1/search-keys",
		map[string]string{"provider": "bing", "key": "whatever"}, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown provider: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodDelete, "/api/v1/search-keys/bing", nil, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown provider on delete: %d %s", rec.Code, rec.Body.String())
	}
}
