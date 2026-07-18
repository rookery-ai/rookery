package web

import (
	"net/http"
	"testing"
)

func TestAPIKBRoundTrip(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/kb/new",
		map[string]any{"path": "notes/hello.md", "is_dir": false}, cookies)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("new: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodPut, "/api/v1/kb/note",
		map[string]string{"path": "notes/hello.md", "content": "# Hello\n\nworld [[other]]"}, cookies)
	if rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodGet, "/api/v1/kb/note?path=notes/hello.md", nil, cookies)
	if rec.Code != 200 || !contains(rec.Body.String(), "world") {
		t.Fatalf("read: %d %s", rec.Code, rec.Body.String())
	}
	// Path escape → 400.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/kb/note?path=../../etc/passwd", nil, cookies)
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
		t.Fatalf("escape must be rejected: %d", rec.Code)
	}
	// Tree shows the note.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/kb/tree?path=notes", nil, cookies)
	if rec.Code != 200 || !contains(rec.Body.String(), "hello") {
		t.Fatalf("tree: %d %s", rec.Code, rec.Body.String())
	}
}

// TestAPIKBNoteBacklinksEmptyArrayNotNull is a regression test for a
// user-reported SPA crash: `TypeError: Cannot read properties of null
// (reading 'length')` on clicking any KB note. vault.Backlinks returns
// `var out []string` (nil, not `[]string{}`) when a note has no incoming
// [[wikilinks]] — apiGetKBNote used to assign that nil straight over the
// pre-initialized `[]string{}` default, so a note with no backlinks
// serialized "backlinks":null. The frontend does `data.backlinks.length`
// unconditionally, so null throws.
func TestAPIKBNoteBacklinksEmptyArrayNotNull(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/kb/new",
		map[string]any{"path": "notes/lonely.md", "is_dir": false}, cookies)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("new: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodPut, "/api/v1/kb/note",
		map[string]string{"path": "notes/lonely.md", "content": "# Lonely\n\nno links here"}, cookies)
	if rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/v1/kb/note?path=notes/lonely.md", nil, cookies)
	if rec.Code != 200 {
		t.Fatalf("read: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if contains(body, `"backlinks":null`) {
		t.Fatalf("backlinks serialized as null, not []: %s", body)
	}
	if !contains(body, `"backlinks":[]`) {
		t.Fatalf("backlinks missing or not an empty array: %s", body)
	}
}

func TestAPIKBSearch(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/kb/new",
		map[string]any{"path": "notes/search-target.md", "is_dir": false}, cookies)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("new: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodPut, "/api/v1/kb/note",
		map[string]string{"path": "notes/search-target.md", "content": "this note mentions zzflibbertigibbetzz somewhere"}, cookies)
	if rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/v1/kb/search?q=zzflibbertigibbetzz", nil, cookies)
	if rec.Code != 200 {
		t.Fatalf("search: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !contains(body, "search-target.md") || !contains(body, "zzflibbertigibbetzz") {
		t.Fatalf("search hit missing path/snippet: %s", body)
	}

	// Empty query → 400 empty_query.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/kb/search?q=", nil, cookies)
	if rec.Code != http.StatusBadRequest || !contains(rec.Body.String(), "empty_query") {
		t.Fatalf("empty query: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAPIKBDeleteAndRename(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/kb/new",
		map[string]any{"path": "notes/movable.md", "is_dir": false}, cookies)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("new: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodPost, "/api/v1/kb/rename",
		map[string]string{"from": "notes/movable.md", "to": "notes/moved.md"}, cookies)
	if rec.Code != 200 {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodGet, "/api/v1/kb/note?path=notes/moved.md", nil, cookies)
	if rec.Code != 200 {
		t.Fatalf("read moved: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodDelete, "/api/v1/kb/note?path=notes/moved.md", nil, cookies)
	if rec.Code != 200 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodGet, "/api/v1/kb/note?path=notes/moved.md", nil, cookies)
	if rec.Code == 200 {
		t.Fatalf("expected note to be gone after delete, got 200")
	}
}

func TestAPIKBResolve(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/kb/new",
		map[string]any{"path": "notes/other-note.md", "is_dir": false}, cookies)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("new: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodPost, "/api/v1/kb/new",
		map[string]any{"path": "notes/hello.md", "is_dir": false}, cookies)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("new: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodPut, "/api/v1/kb/note",
		map[string]string{"path": "notes/hello.md", "content": "See [[other-note]].\n"}, cookies)
	if rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}

	// Resolve by bare name.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/kb/resolve?link=other-note", nil, cookies)
	if rec.Code != 200 || !contains(rec.Body.String(), "notes/other-note.md") {
		t.Fatalf("resolve by name: %d %s", rec.Code, rec.Body.String())
	}

	// Resolve by full path form.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/kb/resolve?link=notes/other-note", nil, cookies)
	if rec.Code != 200 || !contains(rec.Body.String(), "notes/other-note.md") {
		t.Fatalf("resolve by path: %d %s", rec.Code, rec.Body.String())
	}

	// Unknown target -> 404 not_found.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/kb/resolve?link=nonexistent-note", nil, cookies)
	if rec.Code != http.StatusNotFound || !contains(rec.Body.String(), "not_found") {
		t.Fatalf("unknown resolve: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAPIKBRaw(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/kb/new",
		map[string]any{"path": "notes/raw.md", "is_dir": false}, cookies)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("new: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodGet, "/api/v1/kb/raw?path=notes/raw.md", nil, cookies)
	if rec.Code != 200 {
		t.Fatalf("raw: %d %s", rec.Code, rec.Body.String())
	}
}
