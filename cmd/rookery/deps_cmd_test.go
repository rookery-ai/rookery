package main

import (
	"context"
	"errors"
	"os"
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
