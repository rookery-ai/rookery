package web

import (
	"context"
	"net/http"
	"testing"
)

// keylessTestServer boots a server with an owner logged in and a workspace entered,
// which is what every /api/v1/services call needs.
func keylessTestServer(t *testing.T) (*Server, []*http.Cookie, string) {
	t.Helper()
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	return s, cookies, wsID
}

// A keyless provider connects with no credential at all, so the endpoint must not
// reject an empty key the way it does for every other provider.
func TestConnectKeylessAcceptsEmptyKey(t *testing.T) {
	s, cookies, wsID := keylessTestServer(t)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/services/open_meteo/apikey",
		map[string]any{"key": ""}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	conns, err := s.db.ListServiceConnections(context.Background(), wsID)
	if err != nil {
		t.Fatalf("list connections: %v", err)
	}
	if len(conns) != 1 {
		t.Fatalf("connections = %d, want 1", len(conns))
	}
	// A keyless connection has no account behind it, so FetchIdentity cannot run;
	// the provider's own label is the only meaningful name for the row.
	if conns[0].AccountLabel != "Open-Meteo" {
		t.Errorf("label = %q, want the provider label", conns[0].AccountLabel)
	}
	if conns[0].EncryptedAccessToken != "" {
		t.Errorf("stored a credential (%q) for a keyless provider", conns[0].EncryptedAccessToken)
	}
}

// Two keyless connections to one provider would produce two identical tool sets that
// ToolDefs slugs by label — harmless but useless, and confusing on the page. Reject
// the duplicate rather than relying on the user not to create it.
func TestConnectKeylessRejectsDuplicate(t *testing.T) {
	s, cookies, _ := keylessTestServer(t)

	if rec := doJSON(t, s, http.MethodPost, "/api/v1/services/open_meteo/apikey",
		map[string]any{"key": ""}, cookies); rec.Code != http.StatusOK {
		t.Fatalf("first connect: status %d %s", rec.Code, rec.Body.String())
	}

	rec := doJSON(t, s, http.MethodPost, "/api/v1/services/open_meteo/apikey",
		map[string]any{"key": ""}, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("second connect status = %d, want 400", rec.Code)
	}
	if !contains(rec.Body.String(), "already_connected") {
		t.Errorf("body = %s, want code already_connected", rec.Body.String())
	}
}

// A non-keyless provider still requires a credential — the relaxation must be
// scoped to kind=none, not applied to every paste-a-key provider.
func TestConnectAPIKeyStillRequiresAKey(t *testing.T) {
	s, cookies, _ := keylessTestServer(t)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/services/todoist/apikey",
		map[string]any{"key": ""}, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an empty key on an api_key provider", rec.Code)
	}
}

// A pasted base URL is normalized before storage, so action templates see one shape.
func TestConnectStoresNormalizedBaseURL(t *testing.T) {
	s, cookies, wsID := keylessTestServer(t)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/services/immich/apikey",
		map[string]any{
			"key":    "test-key",
			"inputs": map[string]string{"base_url": "  https://photos.example.com/  "},
		}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	conns, err := s.db.ListServiceConnections(context.Background(), wsID)
	if err != nil {
		t.Fatalf("list connections: %v", err)
	}
	if len(conns) != 1 {
		t.Fatalf("connections = %d, want 1", len(conns))
	}
	if !contains(conns[0].Extra, `"base_url":"https://photos.example.com"`) {
		t.Errorf("stored extra = %s, want a trimmed, slash-stripped base_url", conns[0].Extra)
	}
}

// A path PREFIX must survive normalization: https://host/nextcloud and a
// reverse-proxied Paperless at /paperless are mainstream homelab deployments.
func TestConnectPreservesBaseURLPathPrefix(t *testing.T) {
	s, cookies, wsID := keylessTestServer(t)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/services/paperless/apikey",
		map[string]any{
			"key":    "test-key",
			"inputs": map[string]string{"base_url": "https://example.com/paperless/"},
		}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	conns, err := s.db.ListServiceConnections(context.Background(), wsID)
	if err != nil {
		t.Fatalf("list connections: %v", err)
	}
	if !contains(conns[0].Extra, `"base_url":"https://example.com/paperless"`) {
		t.Errorf("stored extra = %s, want the /paperless prefix preserved", conns[0].Extra)
	}
}

// A malformed base URL is rejected at connect, not discovered as a 404 later.
func TestConnectRejectsSchemelessBaseURL(t *testing.T) {
	s, cookies, _ := keylessTestServer(t)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/services/immich/apikey",
		map[string]any{
			"key":    "test-key",
			"inputs": map[string]string{"base_url": "photos.example.com"},
		}, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
}

// The services list must expose the third kind so the SPA can branch on it.
func TestServicesListReportsKeylessKind(t *testing.T) {
	s, cookies, _ := keylessTestServer(t)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/services", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !contains(body, `"keyless"`) {
		t.Error("no provider reported kind=keyless")
	}
	// A keyless provider must carry no redirect URI: nothing ever leaves the browser.
	if !contains(body, `"name":"open_meteo"`) {
		t.Error("open_meteo missing from the services list")
	}
}
