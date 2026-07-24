package web

import (
	"net/http"
	"testing"
)

// A chat's title is user-editable via PATCH /api/v1/chats/:id.
func TestAPIRenameChat(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/chats", map[string]any{"name": "Original"}, cookies)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create chat: %d %s", rec.Code, rec.Body.String())
	}
	var created apiChat
	decodeJSON(t, rec, &created)

	// Rename succeeds and persists.
	rec = doJSON(t, s, http.MethodPatch, "/api/v1/chats/"+created.ID,
		map[string]any{"name": "  Renamed  "}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body.String())
	}
	var renamed apiChat
	decodeJSON(t, rec, &renamed)
	if renamed.Name != "Renamed" {
		t.Fatalf("want trimmed name %q, got %q", "Renamed", renamed.Name)
	}
	rec = doJSON(t, s, http.MethodGet, "/api/v1/chats/"+created.ID, nil, cookies)
	var got struct {
		Chat apiChat `json:"chat"`
	}
	decodeJSON(t, rec, &got)
	if got.Chat.Name != "Renamed" {
		t.Fatalf("name did not persist, got %q", got.Chat.Name)
	}

	// Empty name is rejected.
	rec = doJSON(t, s, http.MethodPatch, "/api/v1/chats/"+created.ID,
		map[string]any{"name": "   "}, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty name: want 400, got %d %s", rec.Code, rec.Body.String())
	}

	// Unknown chat is 404.
	rec = doJSON(t, s, http.MethodPatch, "/api/v1/chats/does-not-exist",
		map[string]any{"name": "x"}, cookies)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown chat: want 404, got %d %s", rec.Code, rec.Body.String())
	}
}
