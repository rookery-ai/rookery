package web

import (
	"net/http"
	"testing"
)

func TestAPIMiddlewareUnauthenticated(t *testing.T) {
	s, _ := newAPITestServer(t)
	rec := doJSON(t, s, http.MethodGet, "/api/v1/auth/session", nil, nil)
	// /auth/session is public: 200 {"authenticated":false}
	if rec.Code != 200 {
		t.Fatalf("session: got %d", rec.Code)
	}
	if got := rec.Body.String(); !contains(got, `"authenticated":false`) {
		t.Fatalf("session body: %s", got)
	}

	// A dash-group route without a session → 401 envelope.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/agents", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("agents unauthenticated: got %d want 401", rec.Code)
	}
	if got := rec.Body.String(); !contains(got, `"code":"not_authenticated"`) {
		t.Fatalf("envelope: %s", got)
	}
}

func TestAPIMiddlewareNoWorkspace(t *testing.T) {
	t.Skip("needs Task 2 login endpoint")
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	rec := doJSON(t, s, http.MethodGet, "/api/v1/agents", nil, cookies)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("got %d want 403", rec.Code)
	}
	if got := rec.Body.String(); !contains(got, `"code":"no_workspace"`) {
		t.Fatalf("envelope: %s", got)
	}
}
