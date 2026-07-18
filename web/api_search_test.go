package web

import (
	"net/http"
	"testing"
)

func TestAPIGlobalSearch(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	// Seed: one note, one agent, one chat.
	if err := s.vault.WriteNote(wsID, "notes/ohrid-trip.md", []byte("lake apartments in Ohrid")); err != nil {
		t.Fatalf("write note: %v", err)
	}
	seedAgent(t, s, wsID) // name "Digest"
	rec := doJSON(t, s, http.MethodGet, "/api/v1/search?q=ohrid", nil, cookies)
	if rec.Code != 200 || !contains(rec.Body.String(), "ohrid-trip") {
		t.Fatalf("notes hit: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodGet, "/api/v1/search?q=digest", nil, cookies)
	if rec.Code != 200 || !contains(rec.Body.String(), `"kind":"agents"`) {
		t.Fatalf("agents hit: %d %s", rec.Code, rec.Body.String())
	}
	// Empty query → 400.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/search?q=", nil, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty q: %d", rec.Code)
	}
}
