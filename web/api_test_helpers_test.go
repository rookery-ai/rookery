package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ilijad1/rookery/internal/auth"
	"github.com/ilijad1/rookery/internal/config"
	"github.com/ilijad1/rookery/internal/db"
	"github.com/ilijad1/rookery/internal/secrets"
	"github.com/labstack/echo/v4"
)

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }

// newAPITestServer builds a Server with a temp DB and no gateway/runner/flows.
func newAPITestServer(t *testing.T) (*Server, *db.DB) {
	t.Helper()
	t.Setenv("SA_SYSTEM_KEY", strings.Repeat("ab", 32)) // 64 hex chars
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"), "../migrations")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	cfg := &config.Config{}
	cfg.Data.Dir = dir
	s, err := NewServer(cfg, database, nil, nil, nil, filepath.Join(dir, "homes"), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return s, database
}

// doJSON performs a request against the echo instance and returns the recorder.
func doJSON(t *testing.T, s *Server, method, path string, body any, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	s.echo.ServeHTTP(rec, req)
	return rec
}

// bootstrapAndLogin creates the owner (admin/password123) and logs in via the API.
func bootstrapAndLogin(t *testing.T, s *Server) []*http.Cookie {
	t.Helper()
	if _, err := auth.BootstrapOwner(s.db, "admin", "password123"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	rec := doJSON(t, s, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "admin", "password": "password123"}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: %d %s", rec.Code, rec.Body.String())
	}
	res := rec.Result()
	return res.Cookies()
}

// createAndEnterWorkspace makes a workspace, marks setup complete with a known
// master password ("master-pw-1"), and enters it. Returns updated cookies + ws id.
func createAndEnterWorkspace(t *testing.T, s *Server, cookies []*http.Cookie) ([]*http.Cookie, string) {
	t.Helper()
	w, err := auth.CreateWorkspace(s.db, "ws1", "test workspace")
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	// Complete "setup" directly in the DB: store encrypted master pw + salt,
	// which also clears needs_setup — mirrors what the setup wizard does (see
	// web/handlers_setup.go:100-131). UpdateWorkspaceSetup (the production
	// helper this used to call) was dead outside this one test call site and
	// was removed — inline its SQL here directly.
	encPw, err := secrets.EncryptMasterPassword("master-pw-1", s.systemKey)
	if err != nil {
		t.Fatalf("encrypt master pw: %v", err)
	}
	salt, err := auth.GenerateSecretsSalt()
	if err != nil {
		t.Fatalf("generate salt: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE workspaces SET encrypted_master_password=?, secrets_salt=?, needs_setup=0, updated_at=datetime('now') WHERE id=?`,
		encPw, salt, w.ID); err != nil {
		t.Fatalf("workspace setup: %v", err)
	}
	rec := doJSON(t, s, http.MethodPost, "/api/v1/workspaces/"+w.ID+"/enter",
		map[string]string{"master_password": "master-pw-1"}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("enter workspace: %d %s", rec.Code, rec.Body.String())
	}
	// Session cookie is rewritten on enter — merge the fresh cookie.
	return rec.Result().Cookies(), w.ID
}
