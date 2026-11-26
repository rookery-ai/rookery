package coder

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func envOf(env []string, key string) string {
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			return strings.TrimPrefix(kv, key+"=")
		}
	}
	return ""
}

// A run's scratch dir replaces the shared <home>/tmp for every temp variable a
// subprocess might read; a coder with none set keeps the shared one.
func TestBuildEnvUsesTheRunScratchDir(t *testing.T) {
	c := New("claude", time.Minute, t.TempDir(), t.TempDir())
	env := c.WithScratchDir("/homes/ws1/tmp/run-abc").buildEnv("/homes/ws1", &claudeBackend{})
	for _, k := range []string{"TMPDIR", "TMP", "TEMP"} {
		if got := envOf(env, k); got != "/homes/ws1/tmp/run-abc" {
			t.Errorf("%s = %q, want the run scratch dir", k, got)
		}
	}
	if got := envOf(c.buildEnv("/homes/ws1", &claudeBackend{}), "TMPDIR"); got != "/homes/ws1/tmp" {
		t.Errorf("without a scratch dir TMPDIR = %q, want /homes/ws1/tmp", got)
	}
}

func TestBuildHostToolsCarriesTheScratchDir(t *testing.T) {
	c := New("claude", time.Minute, t.TempDir(), t.TempDir()).
		WithAPIConfig("openrouter", "m", "", "KEY").WithScratchDir("/x/run-1")
	if h := c.buildHostTools("ws1"); h.scratchDir != "/x/run-1" {
		t.Errorf("hostToolSet.scratchDir = %q", h.scratchDir)
	}
}

func TestRunBashSeesTheRunScratchDir(t *testing.T) {
	homes := t.TempDir()
	scratch := filepath.Join(homes, "ws1", "tmp", "run-1")
	if err := os.MkdirAll(scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	h := &hostToolSet{workspaceID: "ws1", homesDir: homes, workDir: t.TempDir(), scratchDir: scratch}
	out, err := h.runBash(context.Background(), `printf %s "$TMPDIR"`)
	if err != nil {
		t.Fatalf("runBash: %v", err)
	}
	if strings.TrimSpace(out) != scratch {
		t.Errorf("TMPDIR inside bash = %q, want %q", out, scratch)
	}
}

func TestSweepRunScratchRemovesOnlyRunDirs(t *testing.T) {
	homes := t.TempDir()
	mk := func(rel string, dir bool) string {
		p := filepath.Join(homes, rel)
		if dir {
			if err := os.MkdirAll(p, 0o700); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return p
	}
	gone := []string{mk("ws1/tmp/run-a/page.html", false), mk("ws2/tmp/run-b", true)}
	kept := []string{mk("ws1/tmp/keep.txt", false), mk("ws1/tmp/other", true), mk("ws1/run-c", true)}

	n, err := SweepRunScratch(homes)
	if err != nil || n != 2 {
		t.Fatalf("SweepRunScratch = %d, %v; want 2, nil", n, err)
	}
	for _, p := range gone {
		if _, err := os.Stat(filepath.Dir(p)); err == nil && strings.Contains(filepath.Dir(p), "run-") {
			t.Errorf("%s survived", filepath.Dir(p))
		}
	}
	if _, err := os.Stat(filepath.Join(homes, "ws2/tmp/run-b")); !os.IsNotExist(err) {
		t.Error("ws2/tmp/run-b survived")
	}
	for _, p := range kept {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was removed: %v", p, err)
		}
	}
	if n, err := SweepRunScratch(filepath.Join(homes, "missing")); err != nil || n != 0 {
		t.Errorf("missing homes dir: %d, %v", n, err)
	}
}
