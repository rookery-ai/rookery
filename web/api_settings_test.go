package web

import (
	"net/http"
	"testing"
)

// TestAPISettingsGetNeverLeaksSecretValues seeds a secret then checks the
// settings payload names it (secret_names) but never includes its value.
func TestAPISettingsGetNeverLeaksSecretValues(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/secrets",
		map[string]string{"name": "MY_SECRET", "value": "super-sekrit-value"}, cookies)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed secret: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/v1/settings", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("get settings: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !contains(body, "detected_coders") {
		t.Fatalf("expected detected_coders in response: %s", body)
	}
	if !contains(body, "MY_SECRET") {
		t.Fatalf("expected secret name MY_SECRET in response: %s", body)
	}
	if contains(body, "super-sekrit-value") {
		t.Fatalf("response leaked a secret VALUE: %s", body)
	}
}

// TestAPISettingsProfileRoundTrips verifies PUT profile persists and GET
// reflects the new display_name.
func TestAPISettingsProfileRoundTrips(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPut, "/api/v1/settings/profile", map[string]string{
		"display_name": "Ilija D.",
		"email":        "ilija@example.com",
		"timezone":     "Europe/Skopje",
	}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("put profile: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/v1/settings", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("get settings: %d %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"Ilija D."`) {
		t.Fatalf("expected display_name to round-trip: %s", rec.Body.String())
	}
}

// TestAPISettingsWorkspaceEmptyNameRejected checks the missing_field 400.
func TestAPISettingsWorkspaceEmptyNameRejected(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPut, "/api/v1/settings/workspace",
		map[string]string{"name": "", "about": "whatever"}, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty name: %d %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), "missing_field") {
		t.Fatalf("expected missing_field code: %s", rec.Body.String())
	}
}

// TestAPISettingsWorkspaceRoundTrips verifies a valid PUT persists name/about.
func TestAPISettingsWorkspaceRoundTrips(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPut, "/api/v1/settings/workspace",
		map[string]string{"name": "renamed-ws", "about": "a test workspace"}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("put workspace: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodGet, "/api/v1/settings", nil, cookies)
	if !contains(rec.Body.String(), "renamed-ws") {
		t.Fatalf("expected workspace name to round-trip: %s", rec.Body.String())
	}
}

// TestAPISettingsMasterPasswordWrongCurrent verifies the 401 wrong_master_password path.
func TestAPISettingsMasterPasswordWrongCurrent(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	// Seed a secret so the "wrong current" check has something to verify against.
	rec := doJSON(t, s, http.MethodPost, "/api/v1/secrets",
		map[string]string{"name": "SOME_KEY", "value": "v"}, cookies)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed secret: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodPut, "/api/v1/settings/master-password", map[string]string{
		"current":      "totally-wrong",
		"new_password": "new-master-pw-2",
		"confirm":      "new-master-pw-2",
	}, cookies)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong current password: %d %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), "wrong_master_password") {
		t.Fatalf("expected wrong_master_password code: %s", rec.Body.String())
	}
}

// TestAPISettingsMasterPasswordHappyPath changes the master password and
// verifies re-entering the workspace requires the NEW password.
func TestAPISettingsMasterPasswordHappyPath(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPut, "/api/v1/settings/master-password", map[string]string{
		"current":      "master-pw-1",
		"new_password": "new-master-pw-2",
		"confirm":      "new-master-pw-2",
	}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("change master password: %d %s", rec.Code, rec.Body.String())
	}

	// Leave and re-enter with the OLD password → should fail.
	doJSON(t, s, http.MethodPost, "/api/v1/workspaces/leave", nil, cookies)
	rec = doJSON(t, s, http.MethodPost, "/api/v1/workspaces/"+wsID+"/enter",
		map[string]string{"master_password": "master-pw-1"}, cookies)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected old password to now fail: %d %s", rec.Code, rec.Body.String())
	}

	// Re-enter with the NEW password → should succeed.
	rec = doJSON(t, s, http.MethodPost, "/api/v1/workspaces/"+wsID+"/enter",
		map[string]string{"master_password": "new-master-pw-2"}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected new password to succeed: %d %s", rec.Code, rec.Body.String())
	}
}

// TestAPISettingsCoderReusesExistingSecretForProvider covers the SP4 carry-over
// fallback: a workspace that already has a CODER_KEY_<PROVIDER> secret (e.g.
// saved directly via the secrets API, bypassing the coder form) can switch its
// coder to that provider without re-pasting the key — saveWorkspaceCoderCore
// must find and reuse the existing secret by its reserved name instead of
// erroring "API key is required for this provider".
func TestAPISettingsCoderReusesExistingSecretForProvider(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/secrets",
		map[string]string{"name": "CODER_KEY_OPENROUTER", "value": "sk-or-existing"}, cookies)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed secret: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodPut, "/api/v1/settings/coder", map[string]any{
		"kind":     "api",
		"provider": "openrouter",
		"model":    "x",
	}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("put coder: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/v1/settings", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("get settings: %d %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"api_key_secret":"CODER_KEY_OPENROUTER"`) {
		t.Fatalf("expected coder.api_key_secret to reuse CODER_KEY_OPENROUTER: %s", rec.Body.String())
	}
}

// TestAPISettingsCoderNoKeyNoExistingSecretErrors is the negative counterpart:
// no pasted key, no prior coder_api_key_secret, and no matching CODER_KEY_*
// secret already saved — the handler must still reject the save (the fallback
// only reuses a secret that actually exists).
func TestAPISettingsCoderNoKeyNoExistingSecretErrors(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPut, "/api/v1/settings/coder", map[string]any{
		"kind":     "api",
		"provider": "openrouter",
		"model":    "x",
	}, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 with no key and no existing secret: %d %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), "API key is required") {
		t.Fatalf("expected the missing-key message: %s", rec.Body.String())
	}
}

// TestAPISetupGetAndCoderTestRegistered is a light smoke test that the setup
// GET and coder/test endpoints are wired and respond (not exercising the
// coder subprocess itself — see Task 10's warning about the host's real
// `claude` CLI).
func TestAPISetupGetRegistered(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/setup", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("get setup: %d %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"step"`) {
		t.Fatalf("expected step field: %s", rec.Body.String())
	}
}
