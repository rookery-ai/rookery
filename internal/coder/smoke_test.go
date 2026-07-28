package coder

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ilijad1/simple-agents/internal/db"
)

// Host-gated: only runs when opencode is installed. Verifies the Smoke pipeline
// reaches the coder and returns a reply OR a descriptive error (never a silent
// empty success).
func TestSmokeOpencodeHostGated(t *testing.T) {
	bin := "/home/rookie/.opencode/bin/opencode"
	if _, err := os.Stat(bin); err != nil {
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
	bin := "/home/rookie/.opencode/bin/opencode"
	if _, err := os.Stat(bin); err != nil {
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
