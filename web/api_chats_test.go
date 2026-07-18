package web

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestAPIChatsCRUD(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	// Create with no name → default "Chat <workspace-local time>".
	rec := doJSON(t, s, http.MethodPost, "/api/v1/chats", map[string]string{}, cookies)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"name":"Chat `) {
		t.Fatalf("expected default name, got: %s", rec.Body.String())
	}
	if !contains(rec.Body.String(), `"platform":"web"`) || !contains(rec.Body.String(), `"active":true`) {
		t.Fatalf("expected platform web + active true, got: %s", rec.Body.String())
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("missing id in create response: %s", rec.Body.String())
	}

	// Create with an explicit name.
	rec = doJSON(t, s, http.MethodPost, "/api/v1/chats", map[string]string{"name": "My Chat"}, cookies)
	if rec.Code != http.StatusCreated || !contains(rec.Body.String(), `"name":"My Chat"`) {
		t.Fatalf("create named: %d %s", rec.Code, rec.Body.String())
	}

	// List shows both.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/chats", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), created.ID) || !contains(rec.Body.String(), "My Chat") {
		t.Fatalf("list missing created chats: %s", rec.Body.String())
	}

	// Detail: chat + empty messages array (not null).
	rec = doJSON(t, s, http.MethodGet, "/api/v1/chats/"+created.ID, nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("detail: %d %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"messages":[]`) {
		t.Fatalf("expected empty messages array, got: %s", rec.Body.String())
	}
	if !contains(rec.Body.String(), `"chat":{`) || !contains(rec.Body.String(), created.ID) {
		t.Fatalf("expected chat object in detail, got: %s", rec.Body.String())
	}

	// Stop.
	rec = doJSON(t, s, http.MethodPost, "/api/v1/chats/"+created.ID+"/stop", nil, cookies)
	if rec.Code != http.StatusOK || !contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("stop: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodGet, "/api/v1/chats/"+created.ID, nil, cookies)
	if !contains(rec.Body.String(), `"active":false`) {
		t.Fatalf("expected inactive after stop: %s", rec.Body.String())
	}

	// Resume.
	rec = doJSON(t, s, http.MethodPost, "/api/v1/chats/"+created.ID+"/resume", nil, cookies)
	if rec.Code != http.StatusOK || !contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("resume: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodGet, "/api/v1/chats/"+created.ID, nil, cookies)
	if !contains(rec.Body.String(), `"active":true`) {
		t.Fatalf("expected active after resume: %s", rec.Body.String())
	}

	// Delete.
	rec = doJSON(t, s, http.MethodDelete, "/api/v1/chats/"+created.ID, nil, cookies)
	if rec.Code != http.StatusOK || !contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}

	// Detail now 404.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/chats/"+created.ID, nil, cookies)
	if rec.Code != http.StatusNotFound || !contains(rec.Body.String(), "not_found") {
		t.Fatalf("expected 404 not_found after delete: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAPIChatsForeignID404(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/chats/does-not-exist", nil, cookies)
	if rec.Code != http.StatusNotFound || !contains(rec.Body.String(), "not_found") {
		t.Fatalf("get foreign: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodDelete, "/api/v1/chats/does-not-exist", nil, cookies)
	if rec.Code != http.StatusNotFound || !contains(rec.Body.String(), "not_found") {
		t.Fatalf("delete foreign: %d %s", rec.Code, rec.Body.String())
	}
}
