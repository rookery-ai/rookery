package web

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/ilijad1/rookery/internal/backup"
	"github.com/ilijad1/rookery/internal/db"
)

func TestBackupConfigRequiresOwner(t *testing.T) {
	s, _ := newAPITestServer(t)
	rec := doJSON(t, s, http.MethodGet, "/api/v1/backup/config", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// Backup covers every workspace, so it must NOT require one to be entered —
// otherwise an owner could not configure backups before setting up a workspace.
func TestBackupConfigNeedsNoActiveWorkspace(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapLoginAndVerify(t, s)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/backup/config", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestBackupConfigDefaultsAndNeverLeaksSecrets(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapLoginAndVerify(t, s)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/backup/config", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["schedule"] != "daily" {
		t.Fatalf("schedule = %v, want daily", body["schedule"])
	}
	if _, present := body["encrypted_passphrase"]; present {
		t.Fatal("the encrypted passphrase must never be sent to the browser")
	}
	if body["passphrase_set"] != false {
		t.Fatalf("passphrase_set = %v, want false", body["passphrase_set"])
	}
}

func TestBackupSaveConfigStoresPassphraseAndDoesNotEchoIt(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapLoginAndVerify(t, s)

	payload := map[string]any{
		"enabled": true, "destination": "local", "schedule": "weekly",
		"hour": 4, "weekday": 1, "retention": 5,
		"passphrase": "hunter2",
		"local":      map[string]string{"dir": filepath.Join(t.TempDir(), "backups")},
	}
	rec := doJSON(t, s, http.MethodPut, "/api/v1/backup/config", payload, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if contains(rec.Body.String(), "hunter2") {
		t.Fatal("the passphrase must never be echoed back")
	}

	rec = doJSON(t, s, http.MethodGet, "/api/v1/backup/config", nil, cookies)
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["passphrase_set"] != true {
		t.Fatal("passphrase_set must be true after saving one")
	}
	if body["schedule"] != "weekly" {
		t.Fatalf("schedule = %v, want weekly", body["schedule"])
	}
	if body["next_run_at"] == nil {
		t.Fatal("saving must arm the schedule")
	}
}

// Saving an unrelated field must not wipe the stored credential.
func TestBackupSaveConfigKeepsExistingPassphrase(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapLoginAndVerify(t, s)
	dir := filepath.Join(t.TempDir(), "backups")

	first := map[string]any{
		"enabled": true, "destination": "local", "schedule": "daily",
		"hour": 3, "retention": 7, "passphrase": "hunter2",
		"local": map[string]string{"dir": dir},
	}
	if rec := doJSON(t, s, http.MethodPut, "/api/v1/backup/config", first, cookies); rec.Code != http.StatusOK {
		t.Fatalf("first save: %d %s", rec.Code, rec.Body.String())
	}

	second := map[string]any{
		"enabled": true, "destination": "local", "schedule": "daily",
		"hour": 5, "retention": 9, // no passphrase field
		"local": map[string]string{"dir": dir},
	}
	rec := doJSON(t, s, http.MethodPut, "/api/v1/backup/config", second, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("second save: %d %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["passphrase_set"] != true {
		t.Fatal("an omitted passphrase must leave the stored one intact")
	}
}

func TestBackupSaveConfigRejectsEnabledWithoutPassphrase(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapLoginAndVerify(t, s)

	payload := map[string]any{
		"enabled": true, "destination": "local", "schedule": "daily",
		"hour": 3, "retention": 7,
		"local": map[string]string{"dir": filepath.Join(t.TempDir(), "backups")},
	}
	rec := doJSON(t, s, http.MethodPut, "/api/v1/backup/config", payload, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 — a snapshot is never written unencrypted", rec.Code)
	}
}

func TestBackupRestoreRequiresConfirmation(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapLoginAndVerify(t, s)

	payload := map[string]string{
		"name": "rookery-20260729-030000.rkb", "passphrase": "pw", "confirm": "nope",
	}
	rec := doJSON(t, s, http.MethodPost, "/api/v1/backup/restore", payload, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 without the RESTORE confirmation", rec.Code)
	}
	if !contains(rec.Body.String(), "RESTORE") {
		t.Fatalf("the error must say what to type, got %s", rec.Body.String())
	}
}

func TestBackupDeleteRejectsForeignName(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapLoginAndVerify(t, s)

	rec := doJSON(t, s, http.MethodDelete, "/api/v1/backup/snapshots/important-tax-return.pdf", nil, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a non-snapshot name", rec.Code)
	}
}

func TestBackupDownloadRejectsForeignName(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapLoginAndVerify(t, s)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/backup/snapshots/secrets.env/download", nil, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a non-snapshot name", rec.Code)
	}
}

// A full round trip through the API: configure, run, list, verify.
func TestBackupRunListAndVerifyRoundTrip(t *testing.T) {
	s, database := newAPITestServer(t)
	cookies := bootstrapLoginAndVerify(t, s)
	s.WithBackupScheduler(newTestBackupScheduler(t, s, database))

	dir := filepath.Join(t.TempDir(), "backups")
	payload := map[string]any{
		"enabled": true, "destination": "local", "schedule": "daily",
		"hour": 3, "retention": 7, "passphrase": "backup-pass",
		"local": map[string]string{"dir": dir},
	}
	if rec := doJSON(t, s, http.MethodPut, "/api/v1/backup/config", payload, cookies); rec.Code != http.StatusOK {
		t.Fatalf("save config: %d %s", rec.Code, rec.Body.String())
	}

	rec := doJSON(t, s, http.MethodPost, "/api/v1/backup/run", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("run: %d %s", rec.Code, rec.Body.String())
	}
	var run map[string]string
	json.Unmarshal(rec.Body.Bytes(), &run)
	if run["name"] == "" {
		t.Fatal("run must return the snapshot name")
	}

	rec = doJSON(t, s, http.MethodGet, "/api/v1/backup/snapshots", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var entries []map[string]any
	json.Unmarshal(rec.Body.Bytes(), &entries)
	if len(entries) != 1 || entries[0]["name"] != run["name"] {
		t.Fatalf("list = %+v, want the snapshot just written", entries)
	}

	rec = doJSON(t, s, http.MethodPost, "/api/v1/backup/verify",
		map[string]string{"name": run["name"], "passphrase": "backup-pass"}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("verify: %d %s", rec.Code, rec.Body.String())
	}

	// And a wrong passphrase must be a clean 400, not a 500.
	rec = doJSON(t, s, http.MethodPost, "/api/v1/backup/verify",
		map[string]string{"name": run["name"], "passphrase": "wrong"}, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("verify with wrong passphrase: %d, want 400", rec.Code)
	}
}

// newTestBackupScheduler wires a real scheduler onto the test server's own
// database and data dir, so the run/list/verify path is exercised end to end
// rather than mocked.
func newTestBackupScheduler(t *testing.T, s *Server, database *db.DB) *backup.Scheduler {
	t.Helper()
	return backup.NewScheduler(database, database.DB, s.cfg.Data.Dir, s.systemKey)
}
