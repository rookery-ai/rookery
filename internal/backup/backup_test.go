package backup

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// newTestDB builds a throwaway SQLite file with just enough shape for the
// engine: a schema_migrations table and a workspaces table.
func newTestDB(t *testing.T, dir string) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(dir, "rookery.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{
		`CREATE TABLE schema_migrations (name TEXT PRIMARY KEY, applied_at TEXT)`,
		`INSERT INTO schema_migrations(name) VALUES ('001_initial_schema.up.sql')`,
		`INSERT INTO schema_migrations(name) VALUES ('011_pending_actions.up.sql')`,
		`CREATE TABLE workspaces (id TEXT PRIMARY KEY)`,
		`INSERT INTO workspaces(id) VALUES ('ws1')`,
	}
	for _, s := range stmts {
		if _, err := database.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	t.Cleanup(func() { database.Close() })
	return database, path
}

func TestLatestSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	database, _ := newTestDB(t, dir)
	got, err := LatestSchemaVersion(database)
	if err != nil {
		t.Fatalf("LatestSchemaVersion: %v", err)
	}
	if got != "011_pending_actions.up.sql" {
		t.Fatalf("got %q, want the highest applied migration", got)
	}
}

func TestSnapshotProducesDecryptableArchive(t *testing.T) {
	dataDir := t.TempDir()
	database, dbPath := newTestDB(t, dataDir)

	vaults := filepath.Join(dataDir, "vaults")
	writeFile(t, filepath.Join(vaults, "ws1", "notes", "a.md"), "my note")
	writeFile(t, filepath.Join(vaults, "ws1", ".kb", "links.json"), "{}")
	// Must be excluded — hundreds of MB of regenerable cache in a real install.
	writeFile(t, filepath.Join(dataDir, "claude-homes", "ws1", "creds"), "secret")

	destDir := t.TempDir()
	sysKey := make([]byte, 32)
	for i := range sysKey {
		sysKey[i] = byte(i)
	}

	name, err := Snapshot(context.Background(), Options{
		DB:          database,
		DBPath:      dbPath,
		DataDir:     dataDir,
		SystemKey:   sysKey,
		Passphrase:  "correct horse",
		Destination: NewLocalDestination(destDir),
	})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !IsSnapshotName(name) {
		t.Fatalf("returned name %q is not a snapshot name", name)
	}

	sealed, err := os.ReadFile(filepath.Join(destDir, name))
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	var plain bytes.Buffer
	if err := Decrypt(&plain, bytes.NewReader(sealed), "correct horse"); err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	out := t.TempDir()
	m, err := readArchive(bytes.NewReader(plain.Bytes()), out)
	if err != nil {
		t.Fatalf("readArchive: %v", err)
	}

	if m.SchemaVersion != "011_pending_actions.up.sql" {
		t.Fatalf("schema version = %q", m.SchemaVersion)
	}
	if m.WorkspaceCount != 1 {
		t.Fatalf("workspace count = %d, want 1", m.WorkspaceCount)
	}
	if len(m.SystemKey) != 64 {
		t.Fatalf("system key must be 64 hex chars, got %d", len(m.SystemKey))
	}
	if _, err := os.Stat(filepath.Join(out, "db", "rookery.db")); err != nil {
		t.Fatalf("database missing from snapshot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "vaults", "ws1", ".kb", "links.json")); err != nil {
		t.Fatalf(".kb must be archived: %v", err)
	}
	for _, e := range m.Files {
		if strings.HasPrefix(e.Path, "claude-homes") {
			t.Fatalf("claude-homes must never be archived, found %s", e.Path)
		}
	}
}

// VACUUM INTO yields a consistent copy; copying the live file would be torn.
func TestSnapshotDatabaseIsQueryable(t *testing.T) {
	dataDir := t.TempDir()
	database, dbPath := newTestDB(t, dataDir)
	destDir := t.TempDir()

	name, err := Snapshot(context.Background(), Options{
		DB: database, DBPath: dbPath, DataDir: dataDir,
		SystemKey: make([]byte, 32), Passphrase: "pw",
		Destination: NewLocalDestination(destDir),
	})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	rc, _ := NewLocalDestination(destDir).Get(context.Background(), name)
	sealed, _ := io.ReadAll(rc)
	rc.Close()

	var plain bytes.Buffer
	if err := Decrypt(&plain, bytes.NewReader(sealed), "pw"); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if _, err := readArchive(bytes.NewReader(plain.Bytes()), out); err != nil {
		t.Fatal(err)
	}

	restored, err := sql.Open("sqlite", filepath.Join(out, "db", "rookery.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var n int
	if err := restored.QueryRow(`SELECT COUNT(*) FROM workspaces`).Scan(&n); err != nil {
		t.Fatalf("snapshot database is not queryable: %v", err)
	}
	if n != 1 {
		t.Fatalf("got %d workspaces, want 1", n)
	}
}

func TestSnapshotRequiresPassphrase(t *testing.T) {
	dataDir := t.TempDir()
	database, dbPath := newTestDB(t, dataDir)
	_, err := Snapshot(context.Background(), Options{
		DB: database, DBPath: dbPath, DataDir: dataDir,
		SystemKey: make([]byte, 32), Passphrase: "",
		Destination: NewLocalDestination(t.TempDir()),
	})
	if err == nil {
		t.Fatal("an empty passphrase must be refused, never written unencrypted")
	}
}

func TestSnapshotRequiresSystemKey(t *testing.T) {
	dataDir := t.TempDir()
	database, dbPath := newTestDB(t, dataDir)
	_, err := Snapshot(context.Background(), Options{
		DB: database, DBPath: dbPath, DataDir: dataDir,
		SystemKey: make([]byte, 16), Passphrase: "pw",
		Destination: NewLocalDestination(t.TempDir()),
	})
	if err == nil {
		t.Fatal("a short system key must be refused")
	}
}

// The work dir lives inside DataDir; a second snapshot must not sweep the
// first one's leftovers into its own archive.
func TestSnapshotDoesNotArchiveItsOwnWorkDir(t *testing.T) {
	dataDir := t.TempDir()
	database, dbPath := newTestDB(t, dataDir)
	writeFile(t, filepath.Join(dataDir, "vaults", "ws1", "notes", "a.md"), "note")
	destDir := t.TempDir()

	for i := 0; i < 2; i++ {
		if _, err := Snapshot(context.Background(), Options{
			DB: database, DBPath: dbPath, DataDir: dataDir,
			SystemKey: make([]byte, 32), Passphrase: "pw",
			Destination: NewLocalDestination(destDir),
		}); err != nil {
			t.Fatalf("Snapshot %d: %v", i, err)
		}
	}

	leftovers, _ := filepath.Glob(filepath.Join(dataDir, ".backup-work-*"))
	if len(leftovers) != 0 {
		t.Fatalf("work dirs must be cleaned up: %v", leftovers)
	}
}

// Two runs inside the same second must not resolve to the same name: the
// second would silently overwrite the first.
func TestSnapshotNamesDoNotCollideWithinASecond(t *testing.T) {
	dataDir := t.TempDir()
	database, dbPath := newTestDB(t, dataDir)
	destDir := t.TempDir()
	fixed := time.Date(2026, 7, 29, 3, 0, 0, 0, time.UTC)

	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		name, err := Snapshot(context.Background(), Options{
			DB: database, DBPath: dbPath, DataDir: dataDir,
			SystemKey: make([]byte, 32), Passphrase: "pw",
			Destination: NewLocalDestination(destDir), Now: fixed,
		})
		if err != nil {
			t.Fatalf("Snapshot %d: %v", i, err)
		}
		if seen[name] {
			t.Fatalf("name %q reused — the earlier snapshot was overwritten", name)
		}
		seen[name] = true
	}

	entries, _ := NewLocalDestination(destDir).List(context.Background())
	if len(entries) != 3 {
		t.Fatalf("got %d stored snapshots, want 3", len(entries))
	}
}
