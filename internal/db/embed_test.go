package db_test

import (
	"path/filepath"
	"testing"

	"github.com/ilijad1/rookery/internal/db"
)

// The packaged failure reproduced: a process whose working directory has no
// migrations/ anywhere above it, which is what a systemd user unit
// (WorkingDirectory unset, so CWD is $HOME) and any tar.gz user get.
func TestOpenMigratesWithNoMigrationsDirOnDisk(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	database, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	// One table from the initial schema and one from the newest migration, so
	// the assertion covers the whole ordered run rather than just the first file.
	for _, table := range []string{"workspaces", "pending_actions"} {
		var name string
		err := database.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q missing after migrate: %v", table, err)
		}
	}
}

// Opening the same database twice must be a no-op the second time. If the
// embedded names ever stopped matching the names already recorded in
// schema_migrations, every migration would re-apply and CREATE TABLE would fail.
func TestOpenIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	first, err := db.Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	var applied int
	if err := first.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatalf("count: %v", err)
	}
	first.Close()

	second, err := db.Open(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	t.Cleanup(func() { second.Close() })

	var again int
	if err := second.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&again); err != nil {
		t.Fatalf("recount: %v", err)
	}
	if again != applied {
		t.Errorf("re-open changed applied count: %d then %d", applied, again)
	}
	if applied == 0 {
		t.Error("no migrations were applied at all")
	}
}
