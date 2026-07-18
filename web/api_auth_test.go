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
