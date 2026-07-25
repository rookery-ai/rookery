package web

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestAPIWorkspaceLifecycle(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)

	// Create → 201, becomes active (needs_setup=true).
	rec := doJSON(t, s, http.MethodPost, "/api/v1/workspaces",
		map[string]string{"name": "ws-a", "about": "first"}, cookies)
	if rec.Code != http.StatusCreated || !contains(rec.Body.String(), `"name":"ws-a"`) {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	cookies = append(cookies, rec.Result().Cookies()...)

	// Duplicate name → 409.
	rec = doJSON(t, s, http.MethodPost, "/api/v1/workspaces",
		map[string]string{"name": "ws-a"}, cookies)
	if rec.Code != http.StatusConflict {
		t.Fatalf("dup: %d", rec.Code)
	}

	// Leave → session no longer has an active workspace.
	rec = doJSON(t, s, http.MethodPost, "/api/v1/workspaces/leave", nil, cookies)
	if rec.Code != 200 {
		t.Fatalf("leave: %d", rec.Code)
	}
}

func TestAPIEnterWorkspaceWrongPassword(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	// Re-enter with a wrong password → 401 wrong_master_password.
	rec := doJSON(t, s, http.MethodPost, "/api/v1/workspaces/"+wsID+"/enter",
		map[string]string{"master_password": "nope"}, cookies)
	if rec.Code != http.StatusUnauthorized || !contains(rec.Body.String(), "wrong_master_password") {
		t.Fatalf("enter wrong pw: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAPIEnterWorkspaceNeedsSetup(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)

	// Create → 201, becomes active with needs_setup=true (no master password yet).
	rec := doJSON(t, s, http.MethodPost, "/api/v1/workspaces",
		map[string]string{"name": "ws-needs-setup"}, cookies)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	cookies = append(cookies, rec.Result().Cookies()...)
	var created apiWorkspace
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	wsID := created.ID

	// Leave, then re-enter with NO master_password. Since the workspace still
	// needs setup, the enter handler must accept an empty body and report
	// needs_setup=true rather than requiring/verifying a password.
	rec = doJSON(t, s, http.MethodPost, "/api/v1/workspaces/leave", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("leave: %d %s", rec.Code, rec.Body.String())
	}
	cookies = append(cookies, rec.Result().Cookies()...)

	rec = doJSON(t, s, http.MethodPost, "/api/v1/workspaces/"+wsID+"/enter", nil, cookies)
	if rec.Code != http.StatusOK || !contains(rec.Body.String(), `"needs_setup":true`) {
		t.Fatalf("enter needs-setup: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAPIWorkspaceDeleteActiveClearsSession(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodDelete, "/api/v1/workspaces/"+wsID, nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete active: %d %s", rec.Code, rec.Body.String())
	}
	cookies = append(cookies, rec.Result().Cookies()...)

	rec = doJSON(t, s, http.MethodGet, "/api/v1/auth/session", nil, cookies)
	if rec.Code != http.StatusOK || !contains(rec.Body.String(), `"workspace":null`) {
		t.Fatalf("session after deleting active workspace: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAPIWorkspaceDeleteInactiveKeepsSession(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsA := createAndEnterWorkspace(t, s, cookies)

	// Create workspace B — creation sets it active (knocking A out momentarily).
	rec := doJSON(t, s, http.MethodPost, "/api/v1/workspaces",
		map[string]string{"name": "ws-b"}, cookies)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create ws-b: %d %s", rec.Code, rec.Body.String())
	}
	cookies = append(cookies, rec.Result().Cookies()...)
	var created apiWorkspace
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode ws-b response: %v", err)
	}
	wsB := created.ID

	// Re-enter workspace A (master password set by createAndEnterWorkspace).
	rec = doJSON(t, s, http.MethodPost, "/api/v1/workspaces/"+wsA+"/enter",
		map[string]string{"master_password": "master-pw-1"}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-enter A: %d %s", rec.Code, rec.Body.String())
	}
	cookies = append(cookies, rec.Result().Cookies()...)

	// Delete B (not the active workspace) → session must still show A.
	rec = doJSON(t, s, http.MethodDelete, "/api/v1/workspaces/"+wsB, nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete inactive B: %d %s", rec.Code, rec.Body.String())
	}
	cookies = append(cookies, rec.Result().Cookies()...)

	rec = doJSON(t, s, http.MethodGet, "/api/v1/auth/session", nil, cookies)
	if rec.Code != http.StatusOK || !contains(rec.Body.String(), `"id":"`+wsA+`"`) {
		t.Fatalf("session after deleting inactive workspace: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAPIWorkspaceList(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	rec := doJSON(t, s, http.MethodGet, "/api/v1/workspaces", nil, cookies)
	if rec.Code != http.StatusOK || !contains(rec.Body.String(), `"workspaces":`) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAPIWorkspaceDelete(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodDelete, "/api/v1/workspaces/"+wsID, nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}

	// Active workspace was deleted → session should have no active workspace,
	// so entering another with a wrong password shouldn't 500. Verify the
	// workspace is really gone via the list.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/workspaces", nil, cookies)
	if contains(rec.Body.String(), wsID) {
		t.Fatalf("expected workspace gone from list: %s", rec.Body.String())
	}
}

func TestAPIAdminOverviewAuditSettings(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	_, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/admin/overview", nil, cookies)
	if rec.Code != http.StatusOK || !contains(rec.Body.String(), `"workspace_count"`) {
		t.Fatalf("overview: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/v1/admin/audit", nil, cookies)
	if rec.Code != http.StatusOK || !contains(rec.Body.String(), `"logs":`) {
		t.Fatalf("audit: %d %s", rec.Code, rec.Body.String())
	}

	// ?limit= is honored (there's at least 1 audit entry by now: create_workspace).
	rec = doJSON(t, s, http.MethodGet, "/api/v1/admin/audit?limit=1", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit limit: %d %s", rec.Code, rec.Body.String())
	}

	// Admin settings are read-only runtime status now. The writable
	// claude_bin / coder_timeout / agent_timeout / memory_mb fields were
	// removed along with the PUT: they persisted into system_settings and
	// nothing ever read them back.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/admin/settings", nil, cookies)
	if rec.Code != http.StatusOK || !contains(rec.Body.String(), `"landlock_ready"`) {
		t.Fatalf("settings get: %d %s", rec.Code, rec.Body.String())
	}
	if contains(rec.Body.String(), `"claude_bin"`) {
		t.Fatalf("settings get still exposes claude_bin: %s", rec.Body.String())
	}

	// The PUT is gone entirely — Echo answers an unregistered method with 405.
	rec = doJSON(t, s, http.MethodPut, "/api/v1/admin/settings",
		map[string]string{"claude_bin": "/usr/bin/claude"}, cookies)
	if rec.Code == http.StatusOK {
		t.Fatalf("settings put should no longer be routed, got 200: %s", rec.Body.String())
	}
}
