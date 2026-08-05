package web

import (
	"net/http"
	"testing"
)

// walkToStep5 drives a fresh workspace through steps 1-4 so the wizard sits on
// the chat-app step with needs_setup still true — the state every assertion
// below depends on.
func walkToStep5(t *testing.T, s *Server, cookies []*http.Cookie) {
	t.Helper()
	steps := []map[string]any{
		{"step": 1, "name": "wizard-ws"},
		{"step": 2, "master_password": "wizard-pw-1", "confirm": "wizard-pw-1"},
		{"step": 3, "skip": true},
		{"step": 4, "skip": true},
	}
	for _, body := range steps {
		rec := doJSON(t, s, http.MethodPost, "/api/v1/setup", body, cookies)
		if rec.Code != http.StatusOK {
			t.Fatalf("setup %v: %d %s", body["step"], rec.Code, rec.Body.String())
		}
	}
}

// TestAPISetupPlatforms_ReachableDuringSetup is the regression guard for the
// reason onboarding could never run the test-and-link steps: every
// /api/v1/connectors route sits behind requireSetupCompleteAPI and 403s while
// the wizard is running. This endpoint exists so the wizard has a reachable
// source, and the guard exempts it only because it matches by PREFIX — a
// change to exact-match would silently take onboarding back to a saved-but-
// unlinked chat app, which is precisely the bug this work removes.
func TestAPISetupPlatforms_ReachableDuringSetup(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = freshUnsetupWorkspace(t, s, cookies, "wizard-ws")
	walkToStep5(t, s, cookies)

	// The endpoint this one substitutes for must genuinely be blocked here,
	// otherwise the new route proves nothing.
	rec := doJSON(t, s, http.MethodGet, "/api/v1/connectors", nil, cookies)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected /connectors to be 403 during setup, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/v1/setup/platforms", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /setup/platforms during setup, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"platforms"`, `"telegram"`, `"discord"`, `"slack"`, `"bot_online"`, `"linked"`} {
		if !contains(body, want) {
			t.Fatalf("expected %s in payload: %s", want, body)
		}
	}
	if contains(body, "encrypted") || contains(body, `token":"`) {
		t.Fatalf("payload must never leak credential values: %s", body)
	}
}

// TestAPISetupTestPlatform_UnknownPlatform confirms the setup-scoped test
// endpoint validates its platform the same way apiTestConnector does, rather
// than reaching testConnectorIdentity with an unregistered name.
func TestAPISetupTestPlatform_UnknownPlatform(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = freshUnsetupWorkspace(t, s, cookies, "wizard-ws")
	walkToStep5(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/setup/platforms/not-a-real-platform/test", nil, cookies)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestAPISetupTestPlatform_NotConnected pins the shape the wizard's test phase
// reads: a platform with no saved credentials answers 200 with ok:false, NOT
// an HTTP error. The wizard renders the message inline and offers Retry, so an
// error status would surface as a crash banner instead.
func TestAPISetupTestPlatform_NotConnected(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = freshUnsetupWorkspace(t, s, cookies, "wizard-ws")
	walkToStep5(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/setup/platforms/discord/test", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"ok":false`) {
		t.Fatalf("expected ok:false for an unconnected platform: %s", rec.Body.String())
	}
}
