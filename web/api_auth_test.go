package web

import (
	"net/http"
	"testing"
)

func TestAPILoginLogoutSession(t *testing.T) {
	s, _ := newAPITestServer(t)
	// Bad creds → 401 envelope.
	rec := doJSON(t, s, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "nobody", "password": "wrong"}, nil)
	if rec.Code != http.StatusUnauthorized || !contains(rec.Body.String(), `"code":"invalid_credentials"`) {
		t.Fatalf("bad creds: %d %s", rec.Code, rec.Body.String())
	}
	// Good creds → cookie; session reflects owner.
	cookies := bootstrapAndLogin(t, s)
	rec = doJSON(t, s, http.MethodGet, "/api/v1/auth/session", nil, cookies)
	body := rec.Body.String()
	if rec.Code != 200 || !contains(body, `"authenticated":true`) || !contains(body, `"username":"admin"`) {
		t.Fatalf("session: %d %s", rec.Code, body)
	}
	// Logout kills the session.
	rec = doJSON(t, s, http.MethodPost, "/api/v1/auth/logout", nil, cookies)
	if rec.Code != 200 {
		t.Fatalf("logout: %d", rec.Code)
	}
}

// The SPA renders per-message chat timestamps in the workspace profile's
// timezone. The session payload is the carrier: it is already fetched once and
// cached by the SPA, unlike /api/v1/settings which re-probes the filesystem for
// installed coders on every call.
func TestAPISessionCarriesWorkspaceTimezone(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	// No timezone configured yet → present but empty, never absent.
	rec := doJSON(t, s, http.MethodGet, "/api/v1/auth/session", nil, cookies)
	if rec.Code != 200 || !contains(rec.Body.String(), `"timezone":""`) {
		t.Fatalf("default session timezone: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodPut, "/api/v1/settings/profile",
		map[string]string{"timezone": "Europe/Skopje"}, cookies)
	if rec.Code != 200 {
		t.Fatalf("save profile: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/v1/auth/session", nil, cookies)
	if !contains(rec.Body.String(), `"timezone":"Europe/Skopje"`) {
		t.Fatalf("session timezone after save: %s", rec.Body.String())
	}
}
