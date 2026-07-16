package web

import (
	"net/http"
	"testing"

	"github.com/ilijad1/simple-agents/internal/db"
)

func TestAPIConnectors_GET_Unauthenticated(t *testing.T) {
	s, _ := newAPITestServer(t)
	rec := doJSON(t, s, http.MethodGet, "/api/v1/connectors", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAPIConnectors_GET_Authed_ListsTelegram(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/connectors", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !contains(body, `"telegram"`) {
		t.Fatalf("expected response to contain telegram platform, got: %s", body)
	}
	if contains(body, "encrypted") || contains(body, "token\":\"") {
		t.Fatalf("response must never leak credential values: %s", body)
	}
}

// The registered telegram CredSpec validator (wired in NewServer, see
// web/server.go:123-126) hits the real Telegram API, so it can't fail
// deterministically offline. Slack's validator, by contrast, rejects a
// missing app_token synchronously with no network call — use that for a
// deterministic 400 on invalid credentials.
func TestAPIConnectors_POST_InvalidCredentials(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/connectors", map[string]any{
		"platform": "slack",
		"values":   map[string]string{"token": "xoxb-garbage"},
	}, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), "invalid_credentials") {
		t.Fatalf("expected invalid_credentials code, got: %s", rec.Body.String())
	}
}

func TestAPIConnectors_POST_UnknownPlatform(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/connectors", map[string]any{
		"platform": "not-a-real-platform",
		"values":   map[string]string{"token": "x"},
	}, cookies)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAPIConnectors_DELETE_UnknownPlatform(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodDelete, "/api/v1/connectors/not-a-real-platform", nil, cookies)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAPIConnectors_TEST_UnknownPlatform(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/connectors/not-a-real-platform/test", nil, cookies)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAPIConnectors_DELETE_Success(t *testing.T) {
	s, database := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	if err := database.UpsertPlatformConnection(&db.PlatformConnection{
		ID:          "conn-1",
		WorkspaceID: wsID,
		Platform:    "slack",
		Active:      true,
	}); err != nil {
		t.Fatalf("seed connection: %v", err)
	}

	rec := doJSON(t, s, http.MethodDelete, "/api/v1/connectors/slack", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("expected ok:true, got: %s", rec.Body.String())
	}
	if _, err := database.GetPlatformConnection(wsID, "slack"); err == nil {
		t.Fatalf("expected connection to be deleted")
	}
}

func TestAPIConnectors_TEST_NotConnected(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	// slack is a registered platform but nothing has been connected yet —
	// testConnectorIdentity should fail with "connector not found", surfaced
	// as ok:false, not an HTTP error.
	rec := doJSON(t, s, http.MethodPost, "/api/v1/connectors/slack/test", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"ok":false`) {
		t.Fatalf("expected ok:false, got: %s", rec.Body.String())
	}
}
