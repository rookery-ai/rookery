package coder

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ilijad1/rookery/internal/db"
)

// opencodeBin locates the opencode binary for the host-gated tests: PATH first,
// then the two directories npm and the official installer actually use. Returns
// "" when it is not installed, which the callers treat as "skip".
func opencodeBin() string {
	if p, err := exec.LookPath("opencode"); err == nil {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	for _, c := range []string{
		filepath.Join(home, ".opencode", "bin", "opencode"),
		filepath.Join(home, ".local", "bin", "opencode"),
	} {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// TestOpencodeBinResolves proves the host-gated tests locate opencode by PATH
// lookup with a fallback to well-known install directories under the CURRENT
// user's home, rather than by one developer's absolute home directory. The
// previous hardcoded /home/user/... path meant these tests skipped on every
// other machine while appearing to be host-gated rather than machine-gated.
//
// HOME and PATH are pinned via t.Setenv rather than read from the real host:
// the fallback deliberately resolves under whatever $HOME is, and on a
// developer machine whose own account happens to live under /home/<user> (as
// this one does, with a real opencode install under ~/.opencode/bin) a bare
// "does the result start with /home/" assertion would fail even though the
// function is behaving correctly. Pinning the environment keeps the test
// deterministic and checks the real regression: a single hardcoded literal
// path baked into the binary, independent of the host it runs on.
func TestOpencodeBinResolves(t *testing.T) {
	t.Run("not installed returns empty", func(t *testing.T) {
		empty := t.TempDir()
		t.Setenv("HOME", empty)
		t.Setenv("PATH", empty)
		if got := opencodeBin(); got != "" {
			t.Fatalf("opencodeBin() = %q, want empty when opencode is not installed", got)
		}
	})

	t.Run("resolves from HOME, not a hardcoded literal", func(t *testing.T) {
		home := t.TempDir()
		binDir := filepath.Join(home, ".local", "bin")
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			t.Fatal(err)
		}
		fake := filepath.Join(binDir, "opencode")
		if err := os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("HOME", home)
		t.Setenv("PATH", home) // deliberately excludes binDir, forcing the fallback path

		got := opencodeBin()
		if got != fake {
			t.Fatalf("opencodeBin() = %q, want %q (resolved dynamically from $HOME)", got, fake)
		}
		if strings.Contains(got, "rookie") {
			t.Fatalf("opencodeBin() returned the old hardcoded developer path: %q", got)
		}
	})
}

// Host-gated: only runs when opencode is installed. Verifies the Smoke pipeline
// reaches the coder and returns a reply OR a descriptive error (never a silent
// empty success).
func TestSmokeOpencodeHostGated(t *testing.T) {
	bin := opencodeBin()
	if bin == "" {
		t.Skip("opencode not installed; skipping host-gated smoke")
	}
	c := New(bin, 60*time.Second, t.TempDir(), t.TempDir()).WithBackendType("opencode")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	reply, err := c.Smoke(ctx, "wsSmoke")
	if err == nil && reply == "" {
		t.Fatal("Smoke returned empty reply with no error (silent failure)")
	}
	t.Logf("Smoke reply=%q err=%v", reply, err)
}

func TestSmokeMethodExists(t *testing.T) {
	c := New("claude", time.Minute, t.TempDir(), t.TempDir())
	_ = c.Smoke // compile-time check the method exists
}

// TestOpencodeLiveGenerate exercises the FULL chain — real opencode CLI + live
// provider auth + the NDJSON part.text parser — through the public backend path.
// Network + auth gated: runs only when OPENCODE_LIVE=1 (so default `go test` never
// hits the network). Model via OPENCODE_LIVE_MODEL (default a small fast model).
func TestOpencodeLiveGenerate(t *testing.T) {
	if os.Getenv("OPENCODE_LIVE") != "1" {
		t.Skip("set OPENCODE_LIVE=1 (and a valid opencode login) to run the live chain test")
	}
	bin := opencodeBin()
	if bin == "" {
		t.Skip("opencode not installed")
	}
	model := os.Getenv("OPENCODE_LIVE_MODEL")
	if model == "" {
		model = "ollama-cloud/gpt-oss:20b"
	}
	w := &db.Workspace{ID: "wsLive", CoderKind: "local", CoderBin: bin, CoderBackendType: "opencode", CoderModel: model}
	c := ForWorkspace(w, t.TempDir(), t.TempDir(), nil, bin, 90*time.Second, false, true)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	res, err := c.Generate(ctx, "wsLive", "Reply with exactly the word PONG and nothing else.")
	if err != nil {
		t.Fatalf("live opencode Generate failed: %v", err)
	}
	if res.Text == "" {
		t.Fatalf("live opencode returned empty text (parser failed to extract part.text)")
	}
	t.Logf("live opencode (%s) reply=%q", model, res.Text)
}
