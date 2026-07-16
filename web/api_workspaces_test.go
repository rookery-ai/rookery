package web

import (
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

func TestAPIWorkspacePermissions(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	// Initial permissions: all ungranted.
	rec := doJSON(t, s, http.MethodGet, "/api/v1/workspaces/"+wsID+"/permissions", nil, cookies)
	if rec.Code != http.StatusOK || !contains(rec.Body.String(), `"name":"bash"`) {
		t.Fatalf("permissions get: %d %s", rec.Code, rec.Body.String())
	}

	// Grant bash.
	rec = doJSON(t, s, http.MethodPut, "/api/v1/workspaces/"+wsID+"/permissions",
		map[string]any{"grant": []string{"bash"}}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("permissions grant: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/v1/workspaces/"+wsID+"/permissions", nil, cookies)
	if !contains(rec.Body.String(), `"name":"bash","granted":true`) {
		t.Fatalf("permissions not granted: %s", rec.Body.String())
	}

	// Invalid permission name → 400.
	rec = doJSON(t, s, http.MethodPut, "/api/v1/workspaces/"+wsID+"/permissions",
		map[string]any{"grant": []string{"not-a-real-permission"}}, cookies)
	if rec.Code != http.StatusBadRequest || !contains(rec.Body.String(), "invalid_permission") {
		t.Fatalf("permissions invalid: %d %s", rec.Code, rec.Body.String())
	}

	// Revoke bash.
	rec = doJSON(t, s, http.MethodPut, "/api/v1/workspaces/"+wsID+"/permissions",
		map[string]any{"revoke": []string{"bash"}}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("permissions revoke: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodGet, "/api/v1/workspaces/"+wsID+"/permissions", nil, cookies)
	if !contains(rec.Body.String(), `"name":"bash","granted":false`) {
		t.Fatalf("permission not revoked: %s", rec.Body.String())
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

	rec = doJSON(t, s, http.MethodGet, "/api/v1/admin/settings", nil, cookies)
	if rec.Code != http.StatusOK || !contains(rec.Body.String(), `"claude_bin"`) {
		t.Fatalf("settings get: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodPut, "/api/v1/admin/settings",
		map[string]string{"claude_bin": "/usr/bin/claude", "coder_timeout": "150"}, cookies)
	if rec.Code != http.StatusOK || !contains(rec.Body.String(), `"claude_bin":"/usr/bin/claude"`) {
		t.Fatalf("settings put: %d %s", rec.Code, rec.Body.String())
	}
}
