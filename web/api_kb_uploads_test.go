package web

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type kbTreeNode struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	IsDir       bool   `json:"is_dir"`
}

// kbTree returns the nodes the tree endpoint reports for a folder.
func kbTree(t *testing.T, s *Server, cookies []*http.Cookie, path string) []kbTreeNode {
	t.Helper()
	rec := doJSON(t, s, http.MethodGet, "/api/v1/kb/tree?path="+path, nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /kb/tree?path=%s = %d: %s", path, rec.Code, rec.Body.String())
	}
	var body struct {
		Nodes []kbTreeNode `json:"nodes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode tree: %v", err)
	}
	return body.Nodes
}

func hasNode(nodes []kbTreeNode, name string) bool {
	for _, n := range nodes {
		if n.Name == name {
			return true
		}
	}
	return false
}

func nodeDisplay(nodes []kbTreeNode, name string) string {
	for _, n := range nodes {
		if n.Name == name {
			return n.DisplayName
		}
	}
	return ""
}

func hasString(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

// The legacy assets/ folder confused users and nothing writes to it any more.
// It is hidden rather than deleted: existing notes still reference their images
// through it, and those resolve via /kb/raw (vault.Resolve), not the tree.
func TestKBTreeHidesLegacyAssetsAndLabelsUploads(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	for _, rel := range []string{"assets/old.png", "uploads/report.pdf", "notes/a.md"} {
		if err := s.vault.WriteNote(wsID, rel, []byte("x")); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	nodes := kbTree(t, s, cookies, "")

	if hasNode(nodes, "assets") {
		t.Errorf("the legacy assets/ folder must be hidden from the tree, got %+v", nodes)
	}
	if !hasNode(nodes, "uploads") {
		t.Errorf("uploads/ must be visible, got %+v", nodes)
	}
	if got := nodeDisplay(nodes, "uploads"); got != "Uploads" {
		t.Errorf(`uploads must display as "Uploads", got %q`, got)
	}
}

// The hide is root-level ONLY. Skills keep their own skills/<name>/assets/
// directory, and a blanket name match would hide those too.
func TestKBTreeKeepsNestedSkillAssets(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	if err := s.vault.WriteNote(wsID, "skills/pdf/assets/logo.png", []byte("x")); err != nil {
		t.Fatalf("write nested asset: %v", err)
	}

	if nodes := kbTree(t, s, cookies, "skills/pdf"); !hasNode(nodes, "assets") {
		t.Errorf("a skill's own assets/ dir must stay visible, got %+v", nodes)
	}
}

// Hidden in the tree but still offered by the folder picker would leave the
// folder half-hidden and still selectable as a move/create destination.
func TestKBFoldersHidesLegacyAssets(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	for _, rel := range []string{"assets/old.png", "uploads/r.pdf"} {
		if err := s.vault.WriteNote(wsID, rel, []byte("x")); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	rec := doJSON(t, s, http.MethodGet, "/api/v1/kb/folders", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /kb/folders = %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Folders []string `json:"folders"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if hasString(body.Folders, "assets") {
		t.Errorf("assets must not be offered as a destination, got %v", body.Folders)
	}
	if !hasString(body.Folders, "uploads") {
		t.Errorf("uploads must be offered as a destination, got %v", body.Folders)
	}
}

// Editor images are uploads too and belong in the same folder as every other
// ingest door, rather than a second folder of their own.
func TestKBAssetUploadLandsInUploads(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "diagram.png")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write([]byte("\x89PNG\r\n\x1a\nfake")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/kb/asset", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	s.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /kb/asset = %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(body.Path, "uploads/") {
		t.Errorf("an editor upload must land in uploads/, got %q", body.Path)
	}

	// Asserting the returned string alone would not catch the move breaking
	// image display: the editor renders an image by handing this path to
	// /kb/raw. Both that endpoint and the export inliner's regex are
	// path-agnostic (they resolve any scheme-less vault path rather than
	// matching an "assets/" prefix), and this proves it for the new location.
	raw := doJSON(t, s, http.MethodGet,
		"/api/v1/kb/raw?path="+url.QueryEscape(body.Path), nil, cookies)
	if raw.Code != http.StatusOK {
		t.Fatalf("GET /kb/raw for %q = %d: %s", body.Path, raw.Code, raw.Body.String())
	}
	if got := raw.Body.String(); !strings.Contains(got, "PNG") {
		t.Errorf("raw fetch returned the wrong bytes for %q", body.Path)
	}
}
