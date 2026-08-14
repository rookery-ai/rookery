package config

import (
	"os"
	"strings"
	"testing"
)

// ── Default coder binary: ROOKERY_CODER_BIN, and the retired spellings ───────
//
// The variable was ROOKERY_CLAUDE_BIN, from when Claude Code was the only
// supported CLI. Five are supported now, and it never selected Claude anyway —
// it names the DEFAULT binary a workspace gets when it has not chosen one.

func TestCoderBinEnvSetsTheDefaultBinary(t *testing.T) {
	os.Unsetenv("ROOKERY_CLAUDE_BIN")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ROOKERY_CODER_BIN", "/opt/bin/opencode")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Coder.Bin != "/opt/bin/opencode" {
		t.Errorf("Coder.Bin = %q, want /opt/bin/opencode", cfg.Coder.Bin)
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("the current spelling must not warn, got %v", cfg.Warnings)
	}
}

// The retired variable keeps working. It is documented and released, so an
// install that sets it is doing what it was told; refusing to start would
// punish following the instructions.
func TestLegacyClaudeBinEnvStillWorksAndWarns(t *testing.T) {
	os.Unsetenv("ROOKERY_CODER_BIN")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ROOKERY_CLAUDE_BIN", "/opt/bin/claude")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Coder.Bin != "/opt/bin/claude" {
		t.Errorf("Coder.Bin = %q, want the legacy value to still apply", cfg.Coder.Bin)
	}
	if !strings.Contains(strings.Join(cfg.Warnings, "\n"), "ROOKERY_CODER_BIN") {
		t.Errorf("the deprecation must name the replacement, got %v", cfg.Warnings)
	}
}

// Both set: the new one wins. A host migrating will carry both for a while, and
// the one it keeps is the one that should take effect.
func TestCoderBinEnvBeatsTheLegacyVariable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ROOKERY_CLAUDE_BIN", "/old/claude")
	t.Setenv("ROOKERY_CODER_BIN", "/new/opencode")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Coder.Bin != "/new/opencode" {
		t.Errorf("Coder.Bin = %q, want the current variable to win", cfg.Coder.Bin)
	}
}

func TestCoderBinYAMLKey(t *testing.T) {
	os.Unsetenv("ROOKERY_CLAUDE_BIN")
	os.Unsetenv("ROOKERY_CODER_BIN")
	t.Setenv("HOME", t.TempDir())
	cfg, err := Load(writeConfig(t, "coder:\n  coder_bin: /usr/local/bin/codex\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Coder.Bin != "/usr/local/bin/codex" {
		t.Errorf("Coder.Bin = %q, want /usr/local/bin/codex", cfg.Coder.Bin)
	}
}

func TestLegacyClaudeBinYAMLKeyStillWorksAndWarns(t *testing.T) {
	os.Unsetenv("ROOKERY_CLAUDE_BIN")
	os.Unsetenv("ROOKERY_CODER_BIN")
	t.Setenv("HOME", t.TempDir())
	cfg, err := Load(writeConfig(t, "coder:\n  claude_bin: /usr/local/bin/claude\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Coder.Bin != "/usr/local/bin/claude" {
		t.Errorf("Coder.Bin = %q, want the legacy key to still apply", cfg.Coder.Bin)
	}
	if !strings.Contains(strings.Join(cfg.Warnings, "\n"), "coder_bin") {
		t.Errorf("the deprecation must name the replacement key, got %v", cfg.Warnings)
	}
}

// A config carrying BOTH keys must honour the current one.
//
// This is the case a naive `if legacy != ""` check gets backwards, and it is
// why the decision is made against the SECOND parse of the file rather than
// against the merged result: the merged Bin is never empty, because defaults()
// fills it, so "did the file set this?" cannot be asked of the merged value.
func TestCoderBinYAMLKeyBeatsTheLegacyKey(t *testing.T) {
	os.Unsetenv("ROOKERY_CLAUDE_BIN")
	os.Unsetenv("ROOKERY_CODER_BIN")
	t.Setenv("HOME", t.TempDir())
	cfg, err := Load(writeConfig(t,
		"coder:\n  coder_bin: /new/opencode\n  claude_bin: /old/claude\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Coder.Bin != "/new/opencode" {
		t.Errorf("Coder.Bin = %q, want the current key to win", cfg.Coder.Bin)
	}
}

// The legacy field is plumbing, not configuration: it must never survive Load
// as a second place the binary appears to be set.
func TestLegacyClaudeBinFieldIsClearedAfterLoad(t *testing.T) {
	os.Unsetenv("ROOKERY_CLAUDE_BIN")
	os.Unsetenv("ROOKERY_CODER_BIN")
	t.Setenv("HOME", t.TempDir())
	cfg, err := Load(writeConfig(t, "coder:\n  claude_bin: /usr/local/bin/claude\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Coder.LegacyClaudeBin != "" {
		t.Errorf("LegacyClaudeBin = %q, want it cleared after Load", cfg.Coder.LegacyClaudeBin)
	}
}
