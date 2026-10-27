package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/skilldeps"
)

func runDeps(t *testing.T, args ...string) error {
	t.Helper()
	return depsCmd().Run(context.Background(), append([]string{"deps"}, args...))
}

// A skill build writes an install step; it never installs. Same marker the API engine
// reads, so both doors refuse alike.
func TestDepsInstallRefusedDuringASkillBuild(t *testing.T) {
	t.Setenv("ROOKERY_BUILD_KIND", "skill")
	t.Setenv("HOME", t.TempDir())
	err := runDeps(t, "install", "--env", "x", "--package", "requests")
	if !errors.Is(err, skilldeps.ErrSkillBuild) {
		t.Fatalf("err = %v, want ErrSkillBuild", err)
	}
}

// A version range is one requirement, commas and all. urfave/cli splits slice flags on
// commas by default, which turned `pandas>=3.0,<4` into two invalid requirements on the
// CLI path only — so the same skill installed on the API engine and failed on a CLI coder.
func TestDepsInstallKeepsCommasInARequirement(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", "") // no python3: an accepted requirement fails at the next step
	err := runDeps(t, "install", "--env", "x", "--package", "pandas>=3.0,<4")
	if err == nil || !strings.Contains(err.Error(), "python3 is not installed") {
		t.Fatalf("err = %v, want the requirement accepted whole (and then no python3)", err)
	}
}

// A named secret that is not set is reported by NAME — the error must never become a
// way to print a value.
func TestDepsRunMissingSecretNamesIt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRESENT_SECRET", "s3cr3t-value")
	err := runDeps(t, "run", "--env", "x", "--secret", "MISSING_SECRET", "--secret", "PRESENT_SECRET", "--", "s.py")
	if err == nil || !strings.Contains(err.Error(), "MISSING_SECRET") {
		t.Fatalf("err = %v, want it to name MISSING_SECRET", err)
	}
	if strings.Contains(err.Error(), "s3cr3t-value") {
		t.Fatal("error leaked a secret value")
	}
}

// Same resolution as the API engine's run_in_env: a vault-relative skill script is found
// from an agent's own folder, and a path outside the knowledge base is refused.
func TestDepsScriptPathResolution(t *testing.T) {
	vault := t.TempDir()
	agent := filepath.Join(vault, "agents", "a1")
	script := filepath.Join(vault, "skills", "x", "scripts", "s.py")
	for _, d := range []string{agent, filepath.Dir(script)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(script, []byte("print(1)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := resolveDepsScript("skills/x/scripts/s.py", agent, vault)
	if err != nil || got != script {
		t.Fatalf("got %q, %v; want %q", got, err, script)
	}
	if _, err := resolveDepsScript("/etc/passwd", agent, vault); err == nil {
		t.Error("a script outside the knowledge base was accepted")
	}
	if got, err := resolveDepsScript("s.py", agent, ""); err != nil || got != "s.py" {
		t.Errorf("without a vault root the path passes through, got %q, %v", got, err)
	}
}

// Read from source rather than by moving the slice into a function: docs-sync's
// check_cli_coverage parses main.go's root `Commands:` literal and must keep finding it.
func TestDepsCommandIsRegistered(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "\t\t\tdepsCmd(),\n") {
		t.Fatal("depsCmd() is not in the root Commands slice")
	}
}
