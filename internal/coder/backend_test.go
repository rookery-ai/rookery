package coder

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestClaudeConfigEnvOnlyConfigDir(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "host-key")
	b := &claudeBackend{sysClaudeDir: "/op/.claude"}
	env := b.configEnv("/homes/ws1")
	if len(env) != 1 {
		t.Fatalf("configEnv returned %d keys, want exactly 1 (CLAUDE_CONFIG_DIR): %v", len(env), env)
	}
	if _, ok := env["ANTHROPIC_API_KEY"]; ok {
		t.Fatalf("configEnv must not forward ANTHROPIC_API_KEY (precedence flip); got %v", env)
	}
}

func TestBuildEnvWorkspaceSecretWinsForClaude(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "host-key")
	c := New("claude", time.Minute, t.TempDir(), t.TempDir())
	c.extraEnv = map[string]string{"ANTHROPIC_API_KEY": "workspace-secret"}
	env := c.buildEnv("/homes/ws1", &claudeBackend{sysClaudeDir: "/op/.claude"})
	got := ""
	for _, kv := range env {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") {
			got = strings.TrimPrefix(kv, "ANTHROPIC_API_KEY=")
		}
	}
	if got != "workspace-secret" {
		t.Fatalf("ANTHROPIC_API_KEY = %q, want workspace-secret (workspace secret must win over host env)", got)
	}
}
