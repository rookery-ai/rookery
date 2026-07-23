package web

import (
	"net/http"
	"strings"
	"testing"
)

// An HTML export inlines vault-relative IMAGES as data: URIs so the downloaded
// document is self-contained (images travel with it) instead of carrying
// dangling relative references. This also pins that goldmark preserves the
// base64 data URI intact in an <img src>. A non-image file-attachment link
// keeps its portable relative path (goldmark blanks a data: href), and external
// URLs are untouched.
func TestAPIKBExportInlinesAssets(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	// Seed an asset file and a note that references it as an image AND a link.
	if err := s.vault.WriteNote(wsID, "assets/pic.png", []byte("\x89PNG\r\n\x1a\nfakepngbytes")); err != nil {
		t.Fatalf("write asset: %v", err)
	}
	if err := s.vault.WriteNote(wsID, "assets/doc.pdf", []byte("%PDF-1.4 fake")); err != nil {
		t.Fatalf("write asset: %v", err)
	}
	rec := doJSON(t, s, http.MethodPost, "/api/v1/kb/new",
		map[string]any{"path": "notes/withassets.md", "is_dir": false}, cookies)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("new note: %d %s", rec.Code, rec.Body.String())
	}
	body := "# Trip\n\n![shot](assets/pic.png)\n\n[report](assets/doc.pdf)\n\n[external](https://example.com/x.png)\n"
	rec = doJSON(t, s, http.MethodPut, "/api/v1/kb/note",
		map[string]string{"path": "notes/withassets.md", "content": body}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("save note: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/v1/kb/export?path=notes/withassets.md&format=html", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("export html: %d %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()
	if !strings.Contains(out, "data:image/png;base64,") {
		t.Errorf("image not inlined as data URI; export:\n%s", out)
	}
	// The image's bare relative path is gone (replaced by the data URI).
	if strings.Contains(out, "assets/pic.png") {
		t.Errorf("dangling relative image reference survived export")
	}
	// A non-image file-attachment link keeps its portable relative path (a
	// data: href would be blanked by goldmark, so we don't inline it).
	if !strings.Contains(out, "assets/doc.pdf") {
		t.Errorf("attachment link should keep its portable relative path")
	}
	// External URLs are left untouched.
	if !strings.Contains(out, "https://example.com/x.png") {
		t.Errorf("external link was rewritten")
	}
}
