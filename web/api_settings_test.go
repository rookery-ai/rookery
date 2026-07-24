package web

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ilijad1/simple-agents/internal/secrets"
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

// TestAPISettingsCoderProviderSwitchPrefersMatchingSecret is the SP5 final
// review fix: a workspace configured as coder=api/openai (coder_api_key_secret
// = CODER_KEY_OPENAI) that also has a CODER_KEY_OPENROUTER secret on record
// (e.g. saved directly via the secrets API) must, when switched to
// provider=openrouter with no pasted key, pick up CODER_KEY_OPENROUTER — not
// silently keep the stale CODER_KEY_OPENAI reference from the old provider.
func TestAPISettingsCoderProviderSwitchPrefersMatchingSecret(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	// Seed the openai key and configure the coder as openai first, so
	// coder_api_key_secret = CODER_KEY_OPENAI is on record.
	rec := doJSON(t, s, http.MethodPost, "/api/v1/secrets",
		map[string]string{"name": "CODER_KEY_OPENAI", "value": "sk-openai-existing"}, cookies)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed openai secret: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodPut, "/api/v1/settings/coder", map[string]any{
		"kind":     "api",
		"provider": "openai",
		"model":    "gpt-x",
	}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("put coder (openai): %d %s", rec.Code, rec.Body.String())
	}

	// Now seed an openrouter key too, and switch the provider without pasting a key.
	rec = doJSON(t, s, http.MethodPost, "/api/v1/secrets",
		map[string]string{"name": "CODER_KEY_OPENROUTER", "value": "sk-or-existing"}, cookies)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed openrouter secret: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodPut, "/api/v1/settings/coder", map[string]any{
		"kind":     "api",
		"provider": "openrouter",
		"model":    "y",
	}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("put coder (openrouter): %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/v1/settings", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("get settings: %d %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"api_key_secret":"CODER_KEY_OPENROUTER"`) {
		t.Fatalf("expected coder.api_key_secret to switch to CODER_KEY_OPENROUTER, not stay stale: %s", rec.Body.String())
	}
}

// TestAPISetupCoderStep3PrefersProviderMatchedSecret is the setup-wizard
// counterpart of TestAPISettingsCoderProviderSwitchPrefersMatchingSecret:
// apiSetupCoder (step 3) must apply the same provider-matched
// CODER_KEY_<PROVIDER> precedence saveWorkspaceCoderCore already has —
// a workspace with a pre-saved key for a DIFFERENT provider than the one
// currently being configured (w.CoderAPIKeySecret = CODER_KEY_OPENAI, stale
// from an earlier step-3 post in this same wizard run) must, when step 3 is
// posted AGAIN for provider=openrouter with NO pasted key but a matching
// CODER_KEY_OPENROUTER secret already on record, pick up CODER_KEY_OPENROUTER
// — not silently keep the stale CODER_KEY_OPENAI reference.
func TestAPISetupCoderStep3PrefersProviderMatchedSecret(t *testing.T) {
	s, database := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := freshUnsetupWorkspace(t, s, cookies, "wizard-precedence-ws")

	rec := doJSON(t, s, http.MethodPost, "/api/v1/setup", map[string]any{
		"step": 2, "master_password": "wizard-pw-1", "confirm": "wizard-pw-1",
	}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("step2: %d %s", rec.Code, rec.Body.String())
	}

	// First pass: configure as openai with a pasted key — leaves
	// w.CoderAPIKeySecret = CODER_KEY_OPENAI on record.
	rec = doJSON(t, s, http.MethodPost, "/api/v1/setup", map[string]any{
		"step": 3, "coder_kind": "api", "coder_provider": "openai", "coder_model": "gpt-x",
		"coder_api_key": "sk-openai-test",
	}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("step3 (openai): %d %s", rec.Code, rec.Body.String())
	}

	// Seed a CODER_KEY_OPENROUTER secret directly (the wizard can't reach
	// /api/v1/secrets while needs_setup is still true — see apiSetupCoder's
	// doc comment), mirroring "saved directly via the secrets API earlier".
	w, err := database.GetWorkspaceByID(wsID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	masterPw, err := secrets.DecryptMasterPassword(w.EncryptedMasterPassword, s.systemKey)
	if err != nil {
		t.Fatalf("decrypt master pw: %v", err)
	}
	if err := secrets.New(database, wsID, masterPw, w.SecretsSalt).Set(context.Background(), "CODER_KEY_OPENROUTER", "sk-or-existing"); err != nil {
		t.Fatalf("seed openrouter secret: %v", err)
	}

	// Second pass: switch to openrouter with NO pasted key.
	rec = doJSON(t, s, http.MethodPost, "/api/v1/setup", map[string]any{
		"step": 3, "coder_kind": "api", "coder_provider": "openrouter", "coder_model": "glm-5.2",
	}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("step3 (openrouter, no pasted key): %d %s", rec.Code, rec.Body.String())
	}

	w, err = database.GetWorkspaceByID(wsID)
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	if w.CoderAPIKeySecret != "CODER_KEY_OPENROUTER" {
		t.Fatalf("expected coder_api_key_secret to switch to CODER_KEY_OPENROUTER, got %q", w.CoderAPIKeySecret)
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

// TestAPISetupPostBlockedAfterCompletion is the SP5 final review fix: a
// workspace whose setup is already complete (needs_setup=0, the normal state
// for any live session) must reject POST /api/v1/setup — otherwise a step-2
// resubmit can rotate the master password without the caller proving they
// know the current one. GET stays open (harmless, used for step recompute).
func TestAPISetupPostBlockedAfterCompletion(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/setup", map[string]any{
		"step":            2,
		"master_password": "sneaky-new-password",
		"confirm":         "sneaky-new-password",
	}, cookies)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 once setup is complete, got %d: %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), "setup_complete") {
		t.Fatalf("expected setup_complete error code: %s", rec.Body.String())
	}

	// GET must still work (harmless, used for step recompute in the wizard).
	rec = doJSON(t, s, http.MethodGet, "/api/v1/setup", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET setup should still succeed after completion: %d %s", rec.Code, rec.Body.String())
	}
}

// freshUnsetupWorkspace creates a brand-new workspace via the API (auto-
// activated, needs_setup still true — no master password / coder / profile /
// connector yet) and returns updated cookies + its id.
func freshUnsetupWorkspace(t *testing.T, s *Server, cookies []*http.Cookie, name string) ([]*http.Cookie, string) {
	t.Helper()
	rec := doJSON(t, s, http.MethodPost, "/api/v1/workspaces", map[string]string{"name": name}, cookies)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create workspace: %d %s", rec.Code, rec.Body.String())
	}
	var ws struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ws); err != nil {
		t.Fatalf("decode workspace: %v", err)
	}
	return rec.Result().Cookies(), ws.ID
}

// TestAPISetupStep5ReturnsConnectorPlatforms walks a fresh workspace through
// steps 1-4 and confirms the step-5 GET response carries the CredSpec-driven
// platform catalog (telegram/discord/slack) — the SPA wizard's chat-app
// picker has no other reachable source for this data while needs_setup is
// still true (GET /api/v1/connectors is itself blocked by
// requireSetupCompleteAPI until setup finishes).
func TestAPISetupStep5ReturnsConnectorPlatforms(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = freshUnsetupWorkspace(t, s, cookies, "wizard-ws")

	rec := doJSON(t, s, http.MethodPost, "/api/v1/setup", map[string]any{"step": 1, "name": "wizard-ws"}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("step1: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodPost, "/api/v1/setup", map[string]any{
		"step": 2, "master_password": "wizard-pw-1", "confirm": "wizard-pw-1",
	}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("step2: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodPost, "/api/v1/setup", map[string]any{"step": 3, "skip": true}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("step3 skip: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodPost, "/api/v1/setup", map[string]any{"step": 4, "skip": true}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("step4 skip: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/v1/setup", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("get setup: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !contains(body, `"step":5`) {
		t.Fatalf("expected to land on step 5: %s", body)
	}
	if !contains(body, `"platforms"`) || !contains(body, `"telegram"`) || !contains(body, `"discord"`) || !contains(body, `"slack"`) {
		t.Fatalf("expected connector platform catalog in step-5 response: %s", body)
	}

	// An empty/missing token is a silent no-op per apiSetupConnector (mirrors
	// the template handler) — confirms the step doesn't error out, without
	// making a real network call to validate a fake bot token.
	rec = doJSON(t, s, http.MethodPost, "/api/v1/setup", map[string]any{"step": 5, "skip": true}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("step5 skip: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodGet, "/api/v1/setup", nil, cookies)
	if rec.Code != http.StatusOK || !contains(rec.Body.String(), `"step":7`) {
		t.Fatalf("expected skip to land on Done (step 7): %d %s", rec.Code, rec.Body.String())
	}
}

// setupToStep3WithSecret drives a fresh workspace through steps 1-3, ending
// with a CODER_KEY_OPENROUTER secret written under master password
// "wizard-pw-1" — the shared starting point for both re-post guard tests
// below (a wizard "Back to step 2, then resubmit" scenario).
func setupToStep3WithSecret(t *testing.T, s *Server, cookies []*http.Cookie, wsName string) ([]*http.Cookie, string) {
	t.Helper()
	cookies, wsID := freshUnsetupWorkspace(t, s, cookies, wsName)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/setup", map[string]any{
		"step": 2, "master_password": "wizard-pw-1", "confirm": "wizard-pw-1",
	}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("step2: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodPost, "/api/v1/setup", map[string]any{
		"step": 3, "coder_kind": "api", "coder_provider": "openrouter", "coder_model": "glm-5.2",
		"coder_api_key": "sk-or-test",
	}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("step3: %d %s", rec.Code, rec.Body.String())
	}
	return cookies, wsID
}

// TestAPISetupMasterPasswordSamePasswordResubmitIsNoOp covers the common
// Back-then-Next case: the user goes Back to step 2 and resubmits the SAME
// password they already set. Must not regenerate the salt (which would
// orphan the step-3 secret) and must leave the secret decryptable under that
// same password.
func TestAPISetupMasterPasswordSamePasswordResubmitIsNoOp(t *testing.T) {
	s, database := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := setupToStep3WithSecret(t, s, cookies, "guard-ws-same")

	before, err := database.GetWorkspaceByID(wsID)
	if err != nil {
		t.Fatalf("load workspace: %v", err)
	}
	if before.SecretsSalt == "" {
		t.Fatalf("expected a salt to already be set")
	}

	rec := doJSON(t, s, http.MethodPost, "/api/v1/setup", map[string]any{
		"step": 2, "master_password": "wizard-pw-1", "confirm": "wizard-pw-1",
	}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("step2 same-password re-post: %d %s", rec.Code, rec.Body.String())
	}

	after, err := database.GetWorkspaceByID(wsID)
	if err != nil {
		t.Fatalf("reload workspace: %v", err)
	}
	if after.SecretsSalt != before.SecretsSalt {
		t.Fatalf("salt changed on a same-password resubmit: before=%q after=%q", before.SecretsSalt, after.SecretsSalt)
	}
	if after.EncryptedMasterPassword != before.EncryptedMasterPassword {
		t.Fatalf("encrypted master password changed on a same-password resubmit")
	}

	secretStore := secrets.New(database, wsID, "wizard-pw-1", after.SecretsSalt)
	val, err := secretStore.Get(context.Background(), "CODER_KEY_OPENROUTER")
	if err != nil || val != "sk-or-test" {
		t.Fatalf("expected secret to remain decryptable with the same password: val=%q err=%v", val, err)
	}
}

// TestAPISetupMasterPasswordDifferentPasswordResubmitReEncrypts covers a
// genuine password change mid-wizard (Back to step 2, resubmit a DIFFERENT
// password): the salt stays the same, but every existing secret must be
// re-encrypted under the NEW password (and become undecryptable under the
// old one), and the stored encrypted master password must be updated.
func TestAPISetupMasterPasswordDifferentPasswordResubmitReEncrypts(t *testing.T) {
	s, database := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := setupToStep3WithSecret(t, s, cookies, "guard-ws-diff")

	before, err := database.GetWorkspaceByID(wsID)
	if err != nil {
		t.Fatalf("load workspace: %v", err)
	}

	rec := doJSON(t, s, http.MethodPost, "/api/v1/setup", map[string]any{
		"step": 2, "master_password": "different-pw-2", "confirm": "different-pw-2",
	}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("step2 different-password re-post: %d %s", rec.Code, rec.Body.String())
	}

	after, err := database.GetWorkspaceByID(wsID)
	if err != nil {
		t.Fatalf("reload workspace: %v", err)
	}
	if after.SecretsSalt != before.SecretsSalt {
		t.Fatalf("salt should stay the same on a re-encrypt (only the derived key changes): before=%q after=%q",
			before.SecretsSalt, after.SecretsSalt)
	}
	if after.EncryptedMasterPassword == before.EncryptedMasterPassword {
		t.Fatalf("expected the stored encrypted master password to be updated after a password change")
	}

	// Old password must no longer decrypt the secret...
	oldStore := secrets.New(database, wsID, "wizard-pw-1", after.SecretsSalt)
	if _, err := oldStore.Get(context.Background(), "CODER_KEY_OPENROUTER"); err == nil {
		t.Fatalf("expected the secret to NOT be decryptable under the old password after re-encryption")
	}
	// ...but the NEW password must.
	newStore := secrets.New(database, wsID, "different-pw-2", after.SecretsSalt)
	val, err := newStore.Get(context.Background(), "CODER_KEY_OPENROUTER")
	if err != nil || val != "sk-or-test" {
		t.Fatalf("expected secret to be decryptable under the NEW password: val=%q err=%v", val, err)
	}
}

// TestAPIWorkspaceIconRoundTripsAndValidates covers the whole contract of the
// workspace-image endpoint in one pass: a known slug persists and shows up in
// the session payload the SPA renders from, an UNKNOWN slug is rejected rather
// than stored, and "" is accepted as the legitimate "no image" state.
//
// The rejection is the load-bearing half. The stored value is echoed into every
// session response and rendered by the SPA, so it is untrusted input, not a
// preference — and storing an unknown slug would "save" a setting that then
// silently falls back to the monogram forever, which is harder to diagnose than
// an error at the point of the mistake.
func TestAPIWorkspaceIconRoundTripsAndValidates(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPut, "/api/v1/settings/workspace/icon",
		map[string]string{"icon": "aurora"}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("set icon: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/v1/auth/session", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("session: %d %s", rec.Code, rec.Body.String())
	}
	var sess struct {
		Workspace struct {
			Icon string `json:"icon"`
		} `json:"workspace"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sess); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	if sess.Workspace.Icon != "aurora" {
		t.Fatalf("session icon = %q, want %q", sess.Workspace.Icon, "aurora")
	}

	rec = doJSON(t, s, http.MethodPut, "/api/v1/settings/workspace/icon",
		map[string]string{"icon": "../../etc/passwd"}, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown icon: got %d, want 400: %s", rec.Code, rec.Body.String())
	}

	// The rejected value must not have overwritten the good one.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/auth/session", nil, cookies)
	if err := json.Unmarshal(rec.Body.Bytes(), &sess); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	if sess.Workspace.Icon != "aurora" {
		t.Fatalf("icon changed after a rejected write: %q", sess.Workspace.Icon)
	}

	// "" clears it — the same state a workspace is created in.
	rec = doJSON(t, s, http.MethodPut, "/api/v1/settings/workspace/icon",
		map[string]string{"icon": ""}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear icon: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodGet, "/api/v1/auth/session", nil, cookies)
	if err := json.Unmarshal(rec.Body.Bytes(), &sess); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	if sess.Workspace.Icon != "" {
		t.Fatalf("icon = %q after clearing, want empty", sess.Workspace.Icon)
	}
}
