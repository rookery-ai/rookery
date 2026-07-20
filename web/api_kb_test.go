package web

import (
	"encoding/json"
	"net/http"
	"strings"
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

// TestAPIKBNoteKindMarkdown is a regression guard: a .md file must keep
// returning kind:"markdown" (with rendered HTML) unchanged by the kind
// discriminator added for code/binary files.
func TestAPIKBNoteKindMarkdown(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/kb/new",
		map[string]any{"path": "notes/kind.md", "is_dir": false}, cookies)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("new: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodPut, "/api/v1/kb/note",
		map[string]string{"path": "notes/kind.md", "content": "# Hello\n"}, cookies)
	if rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/v1/kb/note?path=notes/kind.md", nil, cookies)
	if rec.Code != 200 {
		t.Fatalf("read: %d %s", rec.Code, rec.Body.String())
	}
	var resp apiKBNoteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Kind != "markdown" {
		t.Fatalf("kind = %q, want markdown", resp.Kind)
	}
	if resp.HTML == "" {
		t.Fatalf("expected rendered HTML for a markdown note")
	}
}

// TestAPIKBNoteKindCode covers a non-markdown, valid-UTF-8, under-cap file —
// the exact shape an agent's tools/*.py is written as. It must open
// read-only as kind:"code" with its content intact, not be treated as
// binary just because the extension isn't allowlisted (content sniffing,
// not an extension allowlist — see spec §7).
func TestAPIKBNoteKindCode(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPut, "/api/v1/kb/note",
		map[string]string{"path": "agents/demo/tools/script.py", "content": "print('hello')\n"}, cookies)
	if rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/v1/kb/note?path=agents/demo/tools/script.py", nil, cookies)
	if rec.Code != 200 {
		t.Fatalf("read: %d %s", rec.Code, rec.Body.String())
	}
	var resp apiKBNoteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Kind != "code" {
		t.Fatalf("kind = %q, want code", resp.Kind)
	}
	if !contains(resp.Content, "print('hello')") {
		t.Fatalf("content missing script body: %s", resp.Content)
	}
	if resp.HTML != "" {
		t.Fatalf("expected no rendered HTML for a code file, got %q", resp.HTML)
	}
}

// TestAPIKBNoteKindBinaryInvalidUTF8 writes raw invalid-UTF-8 bytes directly
// to the vault (bypassing the JSON note-save path, which can't carry
// arbitrary bytes) and asserts the note endpoint classifies it as
// kind:"binary" with content omitted.
func TestAPIKBNoteKindBinaryInvalidUTF8(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	invalid := []byte{0x50, 0x4b, 0x03, 0x04, 0xff, 0xfe, 0x00, 0x01} // not valid UTF-8
	if err := s.vault.WriteNote(wsID, "agents/demo/data.bin", invalid); err != nil {
		t.Fatalf("write raw: %v", err)
	}

	rec := doJSON(t, s, http.MethodGet, "/api/v1/kb/note?path=agents/demo/data.bin", nil, cookies)
	if rec.Code != 200 {
		t.Fatalf("read: %d %s", rec.Code, rec.Body.String())
	}
	var resp apiKBNoteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Kind != "binary" {
		t.Fatalf("kind = %q, want binary", resp.Kind)
	}
	if resp.Content != "" {
		t.Fatalf("expected empty content for a binary file, got %d bytes", len(resp.Content))
	}
}

// TestAPIKBNoteKindBinaryOversize writes a file over the 1 MB inline cap
// whose bytes ARE valid UTF-8 — it must still classify as binary. This is
// the case that distinguishes "content sniffing" from a naive utf8.Valid-
// only check: a 1 MB+ text file is not inlined either.
func TestAPIKBNoteKindBinaryOversize(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	big := []byte(strings.Repeat("a", (1<<20)+10)) // > 1 MiB, valid UTF-8
	if err := s.vault.WriteNote(wsID, "agents/demo/big.txt", big); err != nil {
		t.Fatalf("write raw: %v", err)
	}

	rec := doJSON(t, s, http.MethodGet, "/api/v1/kb/note?path=agents/demo/big.txt", nil, cookies)
	if rec.Code != 200 {
		t.Fatalf("read: %d %s", rec.Code, rec.Body.String())
	}
	var resp apiKBNoteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Kind != "binary" {
		t.Fatalf("kind = %q, want binary (oversize)", resp.Kind)
	}
	if resp.Content != "" {
		t.Fatalf("expected empty content for an oversize file, got %d bytes", len(resp.Content))
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

// TestAPISaveKBNoteBlockedWhileAgentRunning is spec §4.3's core case: the
// runner writes agents/<id>/state.md at the end of every run, so a save to
// that same path while a run is in flight must be refused (409 agent_running)
// rather than racing the runner's write. A save to any OTHER note is
// unaffected — the guard is scoped to exactly agents/<id>/state.md.
func TestAPISaveKBNoteBlockedWhileAgentRunning(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a := seedAgent(t, s, wsID)
	s.runs[a.ID] = &agentRunState{} // in-flight, mirrors TestAPIRunAgentAlreadyRunning

	path := "agents/" + a.ID + "/state.md"
	rec := doJSON(t, s, http.MethodPut, "/api/v1/kb/note",
		map[string]string{"path": path, "content": "# State\n"}, cookies)
	if rec.Code != http.StatusConflict || !contains(rec.Body.String(), "agent_running") {
		t.Fatalf("expected 409 agent_running, got %d %s", rec.Code, rec.Body.String())
	}

	// A different note is unaffected.
	rec = doJSON(t, s, http.MethodPut, "/api/v1/kb/note",
		map[string]string{"path": "notes/free.md", "content": "hi"}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("unrelated note should save: %d", rec.Code)
	}

	// A different file inside the SAME running agent's own dir (e.g. a note
	// or a tool script) is not blocked — only state.md is guarded.
	rec = doJSON(t, s, http.MethodPut, "/api/v1/kb/note",
		map[string]string{"path": "agents/" + a.ID + "/notes/scratch.md", "content": "hi"}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("other file in the running agent's dir should save: %d %s", rec.Code, rec.Body.String())
	}
}

// TestAPISaveKBNoteIdleAgentStateSaves is the negative control: the same
// path, for an agent that is NOT running, saves normally.
func TestAPISaveKBNoteIdleAgentStateSaves(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a := seedAgent(t, s, wsID)

	path := "agents/" + a.ID + "/state.md"
	rec := doJSON(t, s, http.MethodPut, "/api/v1/kb/note",
		map[string]string{"path": path, "content": "# State\n"}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("idle agent's state.md should save: %d %s", rec.Code, rec.Body.String())
	}
}

// TestAPISaveKBNoteBlockedWhileAgentRunningPathVariants checks the path
// match survives the kinds of drift a real client/browser can introduce —
// a leading slash, a "./" prefix, and a trailing slash — without being
// fooled into matching something it shouldn't.
func TestAPISaveKBNoteBlockedWhileAgentRunningPathVariants(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a := seedAgent(t, s, wsID)
	s.runs[a.ID] = &agentRunState{}

	variants := []string{
		"/agents/" + a.ID + "/state.md",
		"agents/" + a.ID + "/state.md/",
		"./agents/" + a.ID + "/state.md",
	}
	for _, p := range variants {
		rec := doJSON(t, s, http.MethodPut, "/api/v1/kb/note",
			map[string]string{"path": p, "content": "# State\n"}, cookies)
		if rec.Code != http.StatusConflict || !contains(rec.Body.String(), "agent_running") {
			t.Fatalf("path %q: expected 409 agent_running, got %d %s", p, rec.Code, rec.Body.String())
		}
	}
}

// TestAPISaveKBNoteBlockedWhileAgentRunningDotlessPath pins the guard against
// an extension bypass. A dotless basename gets ".md" appended by the handler,
// so a PUT to agents/<id>/state lands on the very state.md the guard exists to
// protect. Checking the raw input instead of the finalized path let any direct
// API caller write a running agent's state and get a 200 back — the guard is
// specified to live in the backend precisely because the frontend cannot be
// trusted to send the well-behaved form.
func TestAPISaveKBNoteBlockedWhileAgentRunningDotlessPath(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a := seedAgent(t, s, wsID)
	s.runs[a.ID] = &agentRunState{}

	rec := doJSON(t, s, http.MethodPut, "/api/v1/kb/note",
		map[string]string{"path": "agents/" + a.ID + "/state", "content": "{\"pwned\":true}\n"}, cookies)
	if rec.Code != http.StatusConflict || !contains(rec.Body.String(), "agent_running") {
		t.Fatalf("dotless state path bypassed the guard: got %d %s", rec.Code, rec.Body.String())
	}
}
