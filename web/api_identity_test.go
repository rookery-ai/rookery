package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ilijad1/rookery/internal/auth"
	"github.com/ilijad1/rookery/internal/config"
	"github.com/ilijad1/rookery/internal/db"
	"github.com/ilijad1/rookery/internal/memory"
	"github.com/ilijad1/rookery/internal/secrets"
)

// newIdentityTestServer is newAPITestServer with a real memory store attached —
// the default harness passes nil, which makes every identity seed a silent
// no-op. Returns the data dir too, since the assertions read the seeded files
// straight off disk.
func newIdentityTestServer(t *testing.T) (*Server, *db.DB, string) {
	t.Helper()
	t.Setenv("ROOKERY_SYSTEM_KEY", strings.Repeat("ab", 32))
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"), "../migrations")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	cfg := &config.Config{}
	cfg.Data.Dir = dir
	memStore := memory.New(filepath.Join(dir, "vaults"))
	s, err := NewServer(cfg, database, nil, nil, nil, filepath.Join(dir, "homes"), nil, nil, nil, memStore)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return s, database, dir
}

func readMemoryFile(t *testing.T, dataDir, wsID, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dataDir, "vaults", wsID, "memory", name))
	if err != nil {
		t.Fatalf("read memory/%s: %v", name, err)
	}
	return string(b)
}

// Setup step 7 is the single point every wizard path passes through — steps 1
// and 4 are both skippable — so it is where identity must be seeded. Before
// this, everything the wizard collected went into the settings table and
// memory/ stayed empty, so a freshly set-up workspace told the LLM nothing
// about itself.
func TestSetupCompletionSeedsIdentityFiles(t *testing.T) {
	s, database, dataDir := newIdentityTestServer(t)
	cookies := bootstrapAndLogin(t, s)

	w, err := auth.CreateWorkspace(database, "Personal", "keeping my research in one place")
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	// Enter the workspace, but leave needs_setup=1 so /api/v1/setup accepts it.
	encPw, err := secrets.EncryptMasterPassword("master-pw-1", s.systemKey)
	if err != nil {
		t.Fatal(err)
	}
	salt, err := auth.GenerateSecretsSalt()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(
		`UPDATE workspaces SET encrypted_master_password=?, secrets_salt=? WHERE id=?`,
		encPw, salt, w.ID); err != nil {
		t.Fatal(err)
	}
	rec := doJSON(t, s, http.MethodPost, "/api/v1/workspaces/"+w.ID+"/enter",
		map[string]string{"master_password": "master-pw-1"}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("enter: %d %s", rec.Code, rec.Body.String())
	}
	cookies = rec.Result().Cookies()

	for k, v := range map[string]string{
		"display_name":     "Peer",
		"profile_language": "English",
		"profile_tone":     "concise",
	} {
		if err := database.SetSetting(w.ID, k, v); err != nil {
			t.Fatal(err)
		}
	}

	rec = doJSON(t, s, http.MethodPost, "/api/v1/setup", map[string]any{"step": 7}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("step 7 = %d, body %s", rec.Code, rec.Body.String())
	}

	about := readMemoryFile(t, dataDir, w.ID, memory.AboutFile)
	if !strings.Contains(about, "keeping my research in one place") {
		t.Errorf("ABOUT.md missing the workspace about text:\n%s", about)
	}
	if !strings.Contains(about, "Peer") {
		t.Errorf("ABOUT.md missing the display name:\n%s", about)
	}
	style := readMemoryFile(t, dataDir, w.ID, memory.StyleFile)
	if !strings.Contains(style, "English") {
		t.Errorf("STYLE.md missing the language:\n%s", style)
	}
	if !strings.Contains(style, "brief") {
		t.Errorf("STYLE.md must expand the tone label into guidance:\n%s", style)
	}
}

func TestCreateWorkspaceSeedsIdentityFiles(t *testing.T) {
	s, _, dataDir := newIdentityTestServer(t)
	cookies := bootstrapAndLogin(t, s)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/workspaces",
		map[string]string{"name": "Research", "about": "papers and reading notes"}, cookies)
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("create = %d, body %s", rec.Code, rec.Body.String())
	}

	wss, err := s.db.ListWorkspaces()
	if err != nil || len(wss) == 0 {
		t.Fatalf("ListWorkspaces: %v (%d)", err, len(wss))
	}
	about := readMemoryFile(t, dataDir, wss[0].ID, memory.AboutFile)
	if !strings.Contains(about, "papers and reading notes") {
		t.Errorf("ABOUT.md missing the about text:\n%s", about)
	}
}

// Renaming a workspace must not blank workspaces.about — it is the seed source
// the startup backfill reads for installs whose memory files are still empty.
func TestSaveWorkspaceMetaPreservesAbout(t *testing.T) {
	s, database, _ := newIdentityTestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	if err := database.UpdateWorkspaceMeta(wsID, "Personal", "the original about text"); err != nil {
		t.Fatal(err)
	}

	rec := doJSON(t, s, http.MethodPut, "/api/v1/settings/workspace", map[string]any{
		"name": "Renamed", "about": "a client trying to overwrite it",
	}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d, body %s", rec.Code, rec.Body.String())
	}

	got, err := database.GetWorkspaceByID(wsID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Renamed" {
		t.Errorf("name not saved, got %q", got.Name)
	}
	if got.About != "the original about text" {
		t.Errorf("About was overwritten: %q", got.About)
	}
}

// The five prose keys are no longer read by anything, so the endpoint must stop
// writing them — otherwise Settings still looks like the place to change facts
// whose source of truth is now memory/.
func TestSaveProfileIgnoresProseFields(t *testing.T) {
	s, database, _ := newIdentityTestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	// Pre-existing prose values, as an upgraded install has: they are the seed
	// the startup backfill reads, so the save must neither write them nor WIPE
	// them. profile.Save writes every field unconditionally, so a handler that
	// built a two-field Profile would blank all five here.
	existing := map[string]string{
		"profile_tone": "formal", "profile_language": "Macedonian",
		"profile_notes": "runs a consultancy", "profile_email": "peer@example.com",
		"profile_location": "Skopje",
	}
	for k, v := range existing {
		if err := database.SetSetting(wsID, k, v); err != nil {
			t.Fatal(err)
		}
	}

	rec := doJSON(t, s, http.MethodPut, "/api/v1/settings/profile", map[string]any{
		"display_name": "Peer", "timezone": "Europe/Skopje",
		"tone": "casual", "language": "German", "notes": "ignored",
		"email": "x@y.z", "location": "Nowhere",
	}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d, body %s", rec.Code, rec.Body.String())
	}

	for key, want := range existing {
		got, err := database.GetSetting(wsID, key)
		if err != nil {
			t.Errorf("%s: %v", key, err)
			continue
		}
		if got != want {
			t.Errorf("%s = %q, want the pre-existing %q — the request must neither "+
				"overwrite it nor blank it", key, got, want)
		}
	}
	if v, _ := database.GetSetting(wsID, "display_name"); v != "Peer" {
		t.Errorf("display_name = %q, want Peer", v)
	}
	if v, _ := database.GetSetting(wsID, "profile_timezone"); v != "Europe/Skopje" {
		t.Errorf("profile_timezone = %q, want Europe/Skopje", v)
	}
}
