package web

import (
	"net/http"
	"testing"
)

// mergeCookies keeps the freshest session cookie. The session is rewritten on
// lock/unlock, so a test that kept using the pre-lock cookie would be asserting
// against a stale session rather than the one the server just issued.
func mergeCookies(old, fresh []*http.Cookie) []*http.Cookie {
	if len(fresh) == 0 {
		return old
	}
	return fresh
}

// The lock is a SERVER flag, not a client overlay. These tests pin the three
// properties that distinguish the two: the API actually refuses work while
// locked, the entered workspace survives the lock, and only the real master
// password lifts it.

func TestLockRefusesGuardedRoutesUntilUnlocked(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	// Sanity: a workspace-scoped route works before locking.
	if rec := doJSON(t, s, http.MethodGet, "/api/v1/agents", nil, cookies); rec.Code != http.StatusOK {
		t.Fatalf("agents before lock: %d %s", rec.Code, rec.Body.String())
	}

	rec := doJSON(t, s, http.MethodPost, "/api/v1/auth/lock", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("lock: %d %s", rec.Code, rec.Body.String())
	}
	cookies = mergeCookies(cookies, rec.Result().Cookies())

	// Workspace-scoped and owner-scoped routes are both refused with 423.
	for _, path := range []string{"/api/v1/agents", "/api/v1/workspaces", "/api/v1/admin/audit"} {
		rec = doJSON(t, s, http.MethodGet, path, nil, cookies)
		if rec.Code != http.StatusLocked {
			t.Errorf("%s while locked: want 423, got %d %s", path, rec.Code, rec.Body.String())
		}
	}

	// The escape hatches stay reachable, or the UI could never be unlocked.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/auth/session", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("session while locked: %d %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"locked":true`) {
		t.Errorf("session should report locked:true, got %s", rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodPost, "/api/v1/auth/unlock",
		map[string]string{"master_password": "master-pw-1"}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("unlock: %d %s", rec.Code, rec.Body.String())
	}
	cookies = mergeCookies(cookies, rec.Result().Cookies())

	if rec = doJSON(t, s, http.MethodGet, "/api/v1/agents", nil, cookies); rec.Code != http.StatusOK {
		t.Fatalf("agents after unlock: %d %s", rec.Code, rec.Body.String())
	}
}

func TestLockKeepsTheEnteredWorkspace(t *testing.T) {
	// The whole point of lock-vs-logout: locking must not cost the workspace,
	// otherwise it is just a slower "leave workspace".
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/auth/lock", nil, cookies)
	cookies = mergeCookies(cookies, rec.Result().Cookies())

	rec = doJSON(t, s, http.MethodGet, "/api/v1/auth/session", nil, cookies)
	if !contains(rec.Body.String(), wsID) {
		t.Errorf("locked session lost the active workspace %s: %s", wsID, rec.Body.String())
	}
}

func TestUnlockRejectsTheWrongMasterPassword(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/auth/lock", nil, cookies)
	cookies = mergeCookies(cookies, rec.Result().Cookies())

	rec = doJSON(t, s, http.MethodPost, "/api/v1/auth/unlock",
		map[string]string{"master_password": "not-the-password"}, cookies)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: want 401, got %d %s", rec.Code, rec.Body.String())
	}
	cookies = mergeCookies(cookies, rec.Result().Cookies())

	// Still locked after a failed attempt.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/agents", nil, cookies)
	if rec.Code != http.StatusLocked {
		t.Errorf("a failed unlock must leave the session locked, got %d", rec.Code)
	}
}

func TestSessionReportsUnlockedByDefault(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/auth/session", nil, cookies)
	if !contains(rec.Body.String(), `"locked":false`) {
		t.Errorf("a fresh session must report locked:false, got %s", rec.Body.String())
	}
}
