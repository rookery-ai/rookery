package config

import (
	"os"
	"path/filepath"
	"reflect"
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
