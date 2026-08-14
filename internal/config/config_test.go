package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCoderModeDefaultsToFull(t *testing.T) {
	os.Unsetenv("ROOKERY_CODER_MODE")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Coder.Mode != ModeFull {
		t.Errorf("Mode = %q, want %q", cfg.Coder.Mode, ModeFull)
	}
}

func TestCoderModeSlimFromEnv(t *testing.T) {
	t.Setenv("ROOKERY_CODER_MODE", "slim")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Coder.Mode != ModeSlim {
		t.Errorf("Mode = %q, want %q", cfg.Coder.Mode, ModeSlim)
	}
}

// A typo must fail at startup, not silently fall back to full. A slim image
// whose env var was misspelled would otherwise advertise CLI coders it does
// not contain.
func TestCoderModeRejectsUnknownValue(t *testing.T) {
	t.Setenv("ROOKERY_CODER_MODE", "minimal")
	if _, err := Load(""); err == nil {
		t.Fatal("Load accepted ROOKERY_CODER_MODE=minimal, want an error")
	}
}

// writeConfig writes a config.yaml into a temp dir and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A yaml-configured data dir must carry the database with it.
//
// It did not, and the failure was invisible: `data.dir` relocated the vaults,
// claude-homes, backups and BOTH keys while the database stayed at the default
// path. The relocated dir then got a freshly generated system.key, so every
// stored master password, OAuth token and bot token in that database — all
// encrypted under the OLD key — became undecryptable. The server still booted,
// /healthz still answered ok, and every scheduled agent and connector was dead.
// Only applyEnv ever recomputed the path, so ROOKERY_DATA_DIR was whole and the
// config field it mirrors was not.
func TestYAMLDataDirCarriesTheDatabase(t *testing.T) {
	os.Unsetenv("ROOKERY_DATA_DIR")
	dir := t.TempDir()
	cfg, err := Load(writeConfig(t, "data:\n  dir: "+dir+"\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := filepath.Join(dir, dbFileName); cfg.Database.Path != want {
		t.Errorf("Database.Path = %q, want %q", cfg.Database.Path, want)
	}
}

// An explicit database.path is a deliberate choice and outranks the derivation:
// a user who puts the database on a different disk from the vaults must keep it
// there.
func TestExplicitDatabasePathWinsOverTheDataDir(t *testing.T) {
	os.Unsetenv("ROOKERY_DATA_DIR")
	dir, dbPath := t.TempDir(), filepath.Join(t.TempDir(), "elsewhere.db")
	cfg, err := Load(writeConfig(t, "data:\n  dir: "+dir+"\ndatabase:\n  path: "+dbPath+"\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Database.Path != dbPath {
		t.Errorf("Database.Path = %q, want the explicit %q", cfg.Database.Path, dbPath)
	}
	if cfg.Data.Dir != dir {
		t.Errorf("Data.Dir = %q, want %q", cfg.Data.Dir, dir)
	}
}

// database.path alone must not disturb the data dir.
func TestDatabasePathAloneLeavesTheDataDirDefault(t *testing.T) {
	os.Unsetenv("ROOKERY_DATA_DIR")
	dbPath := filepath.Join(t.TempDir(), "only.db")
	cfg, err := Load(writeConfig(t, "database:\n  path: "+dbPath+"\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Database.Path != dbPath {
		t.Errorf("Database.Path = %q, want %q", cfg.Database.Path, dbPath)
	}
	if home, err := os.UserHomeDir(); err == nil {
		if want := filepath.Join(home, ".rookery"); cfg.Data.Dir != want {
			t.Errorf("Data.Dir = %q, want the default %q", cfg.Data.Dir, want)
		}
	}
}

// Env beats file, including an explicit database.path. ROOKERY_DATA_DIR is
// documented as relocating the database too, and env-over-file is the ordinary
// precedence — this bugfix does not change it.
func TestDataDirEnvOverridesAnExplicitYAMLDatabasePath(t *testing.T) {
	envDir := t.TempDir()
	t.Setenv("ROOKERY_DATA_DIR", envDir)
	yamlDir, dbPath := t.TempDir(), filepath.Join(t.TempDir(), "elsewhere.db")
	cfg, err := Load(writeConfig(t, "data:\n  dir: "+yamlDir+"\ndatabase:\n  path: "+dbPath+"\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Data.Dir != envDir {
		t.Errorf("Data.Dir = %q, want the env %q", cfg.Data.Dir, envDir)
	}
	if want := filepath.Join(envDir, dbFileName); cfg.Database.Path != want {
		t.Errorf("Database.Path = %q, want %q", cfg.Database.Path, want)
	}
}

// No config file at all: both stay on the default dir, together.
func TestDefaultsKeepTheDatabaseInsideTheDataDir(t *testing.T) {
	os.Unsetenv("ROOKERY_DATA_DIR")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := filepath.Join(cfg.Data.Dir, dbFileName); cfg.Database.Path != want {
		t.Errorf("Database.Path = %q, want %q", cfg.Database.Path, want)
	}
}

// An install that relocated via yaml BEFORE the fix has its database at the old
// default. Deriving the new path silently would point at nothing, SQLite would
// create an empty database, and the data would look gone — the same green-but-
// empty failure this fix exists to remove. So say so.
//
// A warning rather than a hard error: failing to start would also block a
// legitimate fresh install that happens to have an unrelated ~/.rookery.
func TestLoadReportsADatabaseLeftAtTheOldDefault(t *testing.T) {
	os.Unsetenv("ROOKERY_DATA_DIR")
	home := t.TempDir()
	t.Setenv("HOME", home)
	legacy := filepath.Join(home, ".rookery")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyDB := filepath.Join(legacy, dbFileName)
	if err := os.WriteFile(legacyDB, []byte("not empty"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	cfg, err := Load(writeConfig(t, "data:\n  dir: "+dir+"\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Warnings) == 0 {
		t.Fatal("relocating past an existing database must warn, got none")
	}
	joined := strings.Join(cfg.Warnings, "\n")
	for _, want := range []string{legacyDB, cfg.Database.Path} {
		if !strings.Contains(joined, want) {
			t.Errorf("warning must name %q, got:\n%s", want, joined)
		}
	}
}

// The same relocation with nothing left behind is an ordinary fresh install.
func TestLoadIsQuietWhenNoLegacyDatabaseExists(t *testing.T) {
	os.Unsetenv("ROOKERY_DATA_DIR")
	t.Setenv("HOME", t.TempDir())
	cfg, err := Load(writeConfig(t, "data:\n  dir: "+t.TempDir()+"\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("want no warnings, got %v", cfg.Warnings)
	}
}

func TestBackupConfigIsGone(t *testing.T) {
	// The inert backup config was replaced by owner-level settings stored in
	// the database. A second, unread config surface next to the real one is
	// exactly what this project's no-fake-settings rule forbids.
	raw := []byte("backup:\n  enabled: true\n  target: git\n")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("an unknown key must be ignored, not fatal: %v", err)
	}
	if reflect.ValueOf(*cfg).FieldByName("Backup").IsValid() {
		t.Fatal("Config.Backup must no longer exist")
	}
}
