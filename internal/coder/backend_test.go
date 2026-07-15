package coder

import (
	"path/filepath"
	"testing"
)

func TestClaudeConfigEnvAndSeed(t *testing.T) {
	b := &claudeBackend{sysClaudeDir: "/op/.claude"}
	home := "/homes/ws1"

	env := b.configEnv(home)
	if got := env["CLAUDE_CONFIG_DIR"]; got != filepath.Join(home, ".claude") {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q, want %q", got, filepath.Join(home, ".claude"))
	}

	seeds := b.seedFiles(home)
	if len(seeds) != 1 {
		t.Fatalf("seedFiles len = %d, want 1", len(seeds))
	}
	if seeds[0].From != "/op/.claude/.credentials.json" {
		t.Fatalf("seed From = %q", seeds[0].From)
	}
	if seeds[0].To != filepath.Join(home, ".claude", ".credentials.json") {
		t.Fatalf("seed To = %q", seeds[0].To)
	}
	if seeds[0].Mode != 0o600 {
		t.Fatalf("seed Mode = %o, want 600", seeds[0].Mode)
	}
}
