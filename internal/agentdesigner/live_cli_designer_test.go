package agentdesigner

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/rookery-ai/rookery/internal/coder"
)

// TestLiveCLIDesignerFirstTurn runs the agent designer's first turn against the
// REAL Claude Code CLI, through the same path the web page takes: StartDesign →
// callCoder → the read-only tool profile → Chat → Generate. That path's system
// prompt is larger than Windows' whole command-line limit, which is what made
// the first turn report "claude.exe hit its usage limit" there.
//
// Gated on ROOKERY_LIVE_CODER=1 because it spends real tokens.
func TestLiveCLIDesignerFirstTurn(t *testing.T) {
	if os.Getenv("ROOKERY_LIVE_CODER") != "1" {
		t.Skip("set ROOKERY_LIVE_CODER=1 to run against the real Claude Code CLI")
	}
	bin, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude not installed")
	}
	real := coder.New(bin, 5*time.Minute, t.TempDir(), t.TempDir())
	flow, workspaceID, _, v := newGenFlowWithVault(t, real)
	if err := os.MkdirAll(v.Root(workspaceID), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	reply, err := flow.StartDesign(ctx, workspaceID, "weather-digest",
		"Every morning, tell me the weather in Skopje.", OriginWeb)
	if err != nil {
		t.Fatalf("StartDesign: %v", err)
	}
	if strings.Contains(reply, "usage limit") || strings.TrimSpace(reply) == "" {
		t.Fatalf("designer first turn failed: %q", reply)
	}
	t.Logf("designer reply (%d chars): %.300s", len(reply), reply)
}
