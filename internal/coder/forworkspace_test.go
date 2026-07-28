package coder

import (
	"context"
	"errors"
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
	c := ForWorkspace(w, "/homes", "/data", nil, "claude", 20*time.Minute, false, true)
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
	c := ForWorkspace(w, "/homes", "/data", nil, "claude", 15*time.Minute, false, true)
	if c.bin != "claude" {
		t.Fatalf("bin = %q, want claude (default)", c.bin)
	}
	if c.timeout != 15*time.Minute {
		t.Fatalf("timeout = %v, want default 15m", c.timeout)
	}
}

func TestForWorkspaceAPIKindBuildsAPIEngine(t *testing.T) {
	// 'api' kind now builds a real API coder — it must NOT fall back to the
	// default binary. IsAPI() is true and the backend type maps to "api".
	w := &db.Workspace{
		ID:                "ws3",
		CoderKind:         "api",
		CoderProvider:     "openai",
		CoderModel:        "gpt-4o",
		CoderAPIKeySecret: "OPENAI_API_KEY",
		CoderBaseURL:      "https://api.openai.com/v1",
		CoderTimeoutS:     120,
	}
	c := ForWorkspace(w, "/homes", "/data", nil, "claude", time.Minute, false, true)
	if !c.IsAPI() {
		t.Fatal("IsAPI() = false, want true for api-kind workspace")
	}
	if c.BackendType() != "api" {
		t.Fatalf("backend = %q, want api", c.BackendType())
	}
	if c.timeout != 120*time.Second {
		t.Fatalf("timeout = %v, want 120s", c.timeout)
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

func TestForWorkspaceRejectsLocalWhenNotAllowed(t *testing.T) {
	w := &db.Workspace{ID: "w1", CoderKind: "local", CoderBin: "claude"}
	c := ForWorkspace(w, "/homes", "/data", nil, "claude", time.Minute, false, false)

	if _, err := c.Ping(context.Background(), "w1"); !errors.Is(err, ErrLocalCoderDisabled) {
		t.Fatalf("Ping error = %v, want ErrLocalCoderDisabled", err)
	}
	if _, err := c.Generate(context.Background(), "w1", "hi"); !errors.Is(err, ErrLocalCoderDisabled) {
		t.Fatalf("Generate error = %v, want ErrLocalCoderDisabled", err)
	}
}

// An API-kind workspace must keep working in a slim build — that is the whole
// point of slim.
func TestForWorkspaceAllowsAPIWhenLocalDisabled(t *testing.T) {
	w := &db.Workspace{ID: "w1", CoderKind: "api", CoderProvider: "openai", CoderModel: "gpt-4o"}
	c := ForWorkspace(w, "/homes", "/data", nil, "claude", time.Minute, false, false)

	if !c.IsAPI() {
		t.Fatal("api workspace did not produce an API coder")
	}
	if _, err := c.Ping(context.Background(), "w1"); errors.Is(err, ErrLocalCoderDisabled) {
		t.Fatal("API coder was wrongly disabled in slim mode")
	}
}
