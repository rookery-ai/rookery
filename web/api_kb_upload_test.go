package web

import (
	"bytes"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ilijad1/rookery/internal/convert"
	"github.com/ilijad1/rookery/internal/vault"
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
	var out apiErrBody
	decodeJSON(t, rec, &out)
	if out.Error.Code != "unsupported_format" {
		t.Errorf("error code = %q, want unsupported_format", out.Error.Code)
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
	// The status code alone doesn't distinguish this from an unsupported
	// format — both are 422 — so pin the error CODE too: a rejected
	// destination is "invalid_destination", never "unsupported_format" (the
	// file itself converts fine; it's the folder that's the problem).
	var out apiErrBody
	decodeJSON(t, rec, &out)
	if out.Error.Code != "invalid_destination" {
		t.Errorf("error code = %q, want invalid_destination", out.Error.Code)
	}
}

// TestUploadErrStatusMapping exercises uploadErrStatus directly rather than
// through HTTP: simulating a genuine disk fault (permission denied, disk
// full) through the real upload path is awkward without touching a real
// filesystem in a way that risks the operator's data, so this is the
// documented substitute for that HTTP-level case — see task-14-report.md.
// The three sentinel branches ARE also exercised over real HTTP above
// (TestAPIKBUploadUnsupportedIsUnprocessable, TestAPIKBUploadRefusesSystemDir);
// this test adds the one branch that isn't reachable that way (plain,
// unwrapped errors → 500) and pins the exact status/code/message contract for
// all four in one place.
func TestUploadErrStatusMapping(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "unsupported format",
			err:        fmt.Errorf("wrap: %w", convert.ErrUnsupportedFormat),
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "unsupported_format",
		},
		{
			name:       "system-managed destination",
			err:        fmt.Errorf("wrap: %w", vault.ErrSystemDir),
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "invalid_destination",
		},
		{
			name:       "destination escapes the vault",
			err:        fmt.Errorf("wrap: %w", vault.ErrEscapes),
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "invalid_destination",
		},
		{
			// A genuine server fault (disk I/O during preserve-original or
			// write-note) is a bare error, not one of ImportFile's typed
			// sentinels — Finding 2's whole point is that this must NOT
			// collapse into the same 422 the format/destination cases get.
			name:       "generic/unwrapped error (disk fault stand-in)",
			err:        errors.New("write note: open /vaults/x/notes/y.md: permission denied"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "internal_error",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, code, msg := uploadErrStatus(tc.err)
			if status != tc.wantStatus {
				t.Errorf("status = %d, want %d", status, tc.wantStatus)
			}
			if code != tc.wantCode {
				t.Errorf("code = %q, want %q", code, tc.wantCode)
			}
			if tc.wantStatus == http.StatusInternalServerError {
				// The 500 branch's client message must never leak the raw
				// error (which can carry a filesystem path) — that's the
				// entire reason this branch exists as distinct from the others.
				if strings.Contains(msg, "permission denied") || strings.Contains(msg, "/vaults/") {
					t.Errorf("client message leaks the raw error: %q", msg)
				}
			} else if msg == "" {
				t.Error("client message must not be empty for a request-property error")
			}
		})
	}
}
