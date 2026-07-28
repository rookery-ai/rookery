package config

import (
	"os"
	"testing"
)

func TestCoderModeDefaultsToFull(t *testing.T) {
	os.Unsetenv("SA_CODER_MODE")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Coder.Mode != ModeFull {
		t.Errorf("Mode = %q, want %q", cfg.Coder.Mode, ModeFull)
	}
}

func TestCoderModeSlimFromEnv(t *testing.T) {
	t.Setenv("SA_CODER_MODE", "slim")
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
	t.Setenv("SA_CODER_MODE", "minimal")
	if _, err := Load(""); err == nil {
		t.Fatal("Load accepted SA_CODER_MODE=minimal, want an error")
	}
}
