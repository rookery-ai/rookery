package coder

import (
	"context"
	"os"
	"testing"
	"time"
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
