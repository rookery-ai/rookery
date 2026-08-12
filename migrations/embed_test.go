package migrations_test

import (
	"io/fs"
	"os"
	"testing"

	"github.com/rookery-ai/rookery/migrations"
)

// The embedded set must equal what is on disk. A migration added to the
// directory but not reachable through FS would apply in development, where the
// files exist, and silently vanish from every shipped artifact — which is the
// exact failure this package exists to prevent.
func TestEmbedHoldsEverySQLFileOnDisk(t *testing.T) {
	onDisk, err := fs.Glob(os.DirFS("."), "*.sql")
	if err != nil {
		t.Fatalf("glob disk: %v", err)
	}
	if len(onDisk) == 0 {
		t.Fatal("no .sql files on disk; test is not running in the migrations directory")
	}

	embedded, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		t.Fatalf("glob embed: %v", err)
	}

	if len(embedded) != len(onDisk) {
		t.Fatalf("embedded %d files, disk has %d\nembedded: %v\ndisk: %v",
			len(embedded), len(onDisk), embedded, onDisk)
	}
	for i := range onDisk {
		if embedded[i] != onDisk[i] {
			t.Errorf("index %d: embedded %q, disk %q", i, embedded[i], onDisk[i])
		}
	}
}

// Down files are never executed today. They are embedded anyway so that wiring
// a down runner later cannot silently find them missing.
func TestEmbedIncludesDownMigrations(t *testing.T) {
	down, err := fs.Glob(migrations.FS, "*.down.sql")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(down) == 0 {
		t.Fatal("no .down.sql files embedded; the embed pattern is too narrow")
	}
}

// A file present in the listing but empty would apply as a no-op migration and
// record itself as applied, which is unrecoverable without hand-editing
// schema_migrations.
func TestEmbeddedMigrationsAreNonEmpty(t *testing.T) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		data, err := fs.ReadFile(migrations.FS, e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if len(data) == 0 {
			t.Errorf("%s is empty", e.Name())
		}
	}
}
