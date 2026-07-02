package coder

import (
	"testing"
	"time"

	"github.com/ilijad1/simple-agents/internal/db"
)

func TestForWorkspaceUsesInlinedConfig(t *testing.T) {
	w := &db.Workspace{
		ID:               "ws1",
		CoderKind:        "local",
		CoderBin:         "opencode",
		CoderTimeoutS:    90,
		CoderBackendType: "generic",
	}
	c := ForWorkspace(w, "/homes", "/data", "claude", 20*time.Minute, false)
	if c.bin != "opencode" {
		t.Fatalf("bin = %q, want opencode", c.bin)
	}
	if c.timeout != 90*time.Second {
		t.Fatalf("timeout = %v, want 90s", c.timeout)
	}
	if c.BackendType() != "generic" {
		t.Fatalf("backend = %q, want generic", c.BackendType())
	}
}

func TestForWorkspaceFallsBackToDefaults(t *testing.T) {
	w := &db.Workspace{ID: "ws2", CoderKind: "local"} // no coder fields set
	c := ForWorkspace(w, "/homes", "/data", "claude", 15*time.Minute, false)
	if c.bin != "claude" {
		t.Fatalf("bin = %q, want claude (default)", c.bin)
	}
	if c.timeout != 15*time.Minute {
		t.Fatalf("timeout = %v, want default 15m", c.timeout)
	}
}

func TestForWorkspaceAPIKindFallsBackToDefaults(t *testing.T) {
	// 'api' kind is not implemented yet: it must fall back to the default binary,
	// not try to use it as a local bin.
	w := &db.Workspace{ID: "ws3", CoderKind: "api", CoderBin: "gpt-4o", CoderProvider: "openai"}
	c := ForWorkspace(w, "/homes", "/data", "claude", time.Minute, false)
	if c.bin != "claude" {
		t.Fatalf("bin = %q, want claude (api kind not implemented → default)", c.bin)
	}
}

func TestDetectInstalledReturnsResolvedPaths(t *testing.T) {
	// Environment-dependent: just assert the shape (no panic, resolved bins non-empty).
	for _, in := range DetectInstalled() {
		if in.Bin == "" || in.Name == "" {
			t.Fatalf("detected coder with empty field: %+v", in)
		}
	}
}
