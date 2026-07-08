package coder

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ilijad1/simple-agents/internal/db"
)

// TestCLIChatFlattensHistoryIntoPrompt is the bin-coder regression guard for the
// chatAPI split: a CLI coder (api == nil) must NOT route through chatAPI. It must
// keep the legacy behaviour of flattening system + history + current message into a
// single text prompt handed to Generate, so a host CLI coder (claude-code, opencode,
// …) still receives the conversation the same way it always did.
//
// We point the coder at a fake "binary" (a tiny python script) that echoes the -p
// prompt arg to stdout, then assert the flattened structure is present in the prompt
// the binary received — proving the CLI Chat path is intact and untouched.
func TestCLIChatFlattensHistoryIntoPrompt(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	dir := t.TempDir()
	ws := "ws-cli"
	binPath := filepath.Join(dir, "fakecoder")
	mustWrite(t, binPath, []byte("#!/usr/bin/env python3\nimport sys\ni=sys.argv.index('-p')\nprint(sys.argv[i+1])\n"))
	if err := os.Chmod(binPath, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	// Generic backend auto-selected (binary name "fakecoder" has no "claude").
	c := New(binPath, time.Minute, filepath.Join(dir, "homes"), dir).
		WithSandbox(false).
		WithBackendType("generic")
	mustMkdir(t, filepath.Join(dir, "homes"))

	history := []db.ChatMessage{
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
	}

	res, err := c.Chat(context.Background(), ws, history, "SYS-CTX", "current-msg")
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	out := res.Text

	if !strings.Contains(out, "[Persistent user context]") || !strings.Contains(out, "SYS-CTX") {
		t.Fatalf("CLI Chat prompt missing system context: %q", out)
	}
	if !strings.Contains(out, "[Previous conversation]") || !strings.Contains(out, "Human: u1") || !strings.Contains(out, "Assistant: a1") {
		t.Fatalf("CLI Chat prompt missing history: %q", out)
	}
	if !strings.Contains(out, "Current message: current-msg") {
		t.Fatalf("CLI Chat prompt missing current message: %q", out)
	}
}
