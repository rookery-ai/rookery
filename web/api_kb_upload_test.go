package web

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// uploadRequest builds a multipart POST to /api/v1/kb/upload with the given
// file field (and optional dir field), matching the field names the SPA's
// upload button/drop handler post.
func uploadRequest(t *testing.T, filename, content, dir string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if dir != "" {
		if err := mw.WriteField("dir", dir); err != nil {
			t.Fatalf("WriteField: %v", err)
		}
	}
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		t.Fatalf("write content: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/kb/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestAPIKBUploadConvertsAndFiles(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	req := uploadRequest(t, "sales.csv", "a,b\n1,2\n", "")
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	s.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		NotePath     string   `json:"note_path"`
		OriginalPath string   `json:"original_path"`
		Kind         string   `json:"kind"`
		Extractor    string   `json:"extractor"`
		Warnings     []string `json:"warnings"`
	}
	decodeJSON(t, rec, &out)
	if !strings.HasSuffix(out.NotePath, ".md") || out.Kind != "csv" {
		t.Errorf("unexpected response: %+v", out)
	}
	if out.OriginalPath == "" {
		t.Errorf("expected original_path to be set, got %+v", out)
	}
	if out.Warnings == nil {
		t.Errorf("expected warnings to serialize as [] not null, got %+v", out)
	}

	// The converted note itself must actually be readable back out of the KB.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/kb/note?path="+out.NotePath, nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("read imported note: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAPIKBUploadRejectsOversized(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	big := strings.Repeat("a,b\n", maxUploadBytes/4+10)
	req := uploadRequest(t, "big.csv", big, "")
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	s.echo.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413: %s", rec.Code, rec.Body.String())
	}
}

func TestAPIKBUploadRequiresWorkspace(t *testing.T) {
	s, _ := newAPITestServer(t)

	req := uploadRequest(t, "x.csv", "a,b\n", "") // no session cookies at all
	rec := httptest.NewRecorder()
	s.echo.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want an auth failure", rec.Code)
	}
}

func TestAPIKBUploadUnsupportedIsUnprocessable(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	req := uploadRequest(t, "x.bin", "\x00\x01\x02", "")
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	s.echo.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
}

// TestAPIKBUploadRespectsDestDir confirms the optional "dir" field is honored
// (files under the caller-chosen folder, not always notes/).
func TestAPIKBUploadRespectsDestDir(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	req := uploadRequest(t, "report.csv", "x,y\n1,2\n", "notes/imports")
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	s.echo.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		NotePath string `json:"note_path"`
	}
	decodeJSON(t, rec, &out)
	if !strings.HasPrefix(out.NotePath, "notes/imports/") {
		t.Errorf("note_path = %q, want it under notes/imports/", out.NotePath)
	}
}

// TestAPIKBUploadRefusesSystemDir pins that a caller cannot use the dir field
// to land an import inside a system-managed area (vault.ImportFile's own
// guard — exercised here through the HTTP door, mapped to 422 like any other
// unconvertible-request case).
func TestAPIKBUploadRefusesSystemDir(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	req := uploadRequest(t, "sneaky.csv", "a,b\n1,2\n", "agents")
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	s.echo.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
}
