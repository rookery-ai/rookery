package coder

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/sandbox"
)

// TestLiveCLIReadsAnOversizedPromptFromStdin drives the REAL coder binaries
// through Generate with a prompt larger than Windows' whole command-line limit.
// It is what stdinArgs' "verified" claim rests on: a fake binary can prove the
// prompt is written to stdin, never that the CLI reads it. A backend is added to
// stdinPrompter, and to this table, only once it passes here.
//
// Gated on ROOKERY_LIVE_CODER=1 because it spends real tokens; each case also
// skips when its binary is not installed.
func TestLiveCLIReadsAnOversizedPromptFromStdin(t *testing.T) {
	if os.Getenv("ROOKERY_LIVE_CODER") != "1" {
		t.Skip("set ROOKERY_LIVE_CODER=1 to run against the real coder CLIs")
	}
	prompt := "Ignore the filler text below. Reply with exactly the word PONG and nothing else.\n\n" +
		strings.Repeat(`Filler: "quoted words" and more words. `, 1200)
	if windowsCommandLineLen([]string{"-p", prompt}) < windowsMaxCommandLine {
		t.Fatal("test prompt must exceed the Windows command-line limit to prove anything")
	}

	cases := []struct{ name, bin, backend, model string }{
		{"claude", lookBin("claude"), "claude", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.bin == "" {
				t.Skipf("%s not installed", tc.name)
			}
			w := &db.Workspace{ID: "wsLive", CoderKind: "local", CoderBin: tc.bin, CoderBackendType: tc.backend, CoderModel: tc.model}
			c := ForWorkspace(w, t.TempDir(), t.TempDir(), nil, tc.bin, 3*time.Minute, false, true).WithNoTools()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			res, err := c.Generate(ctx, "wsLive", prompt)
			if err != nil {
				t.Fatalf("live %s Generate: %v", tc.name, err)
			}
			if !strings.Contains(strings.ToUpper(res.Text), "PONG") {
				t.Fatalf("live %s reply = %q, want PONG", tc.name, res.Text)
			}
			t.Logf("live %s reply=%q", tc.name, res.Text)
		})
	}
}

// TestLiveSandboxedClaudeToolCallWithStdinPrompt is the shape every scheduled
// run takes on Linux: `rookery __sandbox-exec` applies Landlock and then execs
// claude, with the prompt on a stdin pipe and a write tool granted. The stdin
// change has to survive that re-exec, and the model has to actually act.
//
// Needs ROOKERY_LIVE_CODER=1 plus ROOKERY_LIVE_SELF_EXE naming a built rookery
// binary: the test binary has no sandbox helper, so it cannot stand in for one.
func TestLiveSandboxedClaudeToolCallWithStdinPrompt(t *testing.T) {
	if os.Getenv("ROOKERY_LIVE_CODER") != "1" {
		t.Skip("set ROOKERY_LIVE_CODER=1 to run against the real coder CLIs")
	}
	self := os.Getenv("ROOKERY_LIVE_SELF_EXE")
	if self == "" {
		t.Skip("set ROOKERY_LIVE_SELF_EXE to a built rookery binary")
	}
	if !sandbox.Supported() {
		t.Skip("Landlock unavailable; this test would prove nothing")
	}
	bin := lookBin("claude")
	if bin == "" {
		t.Skip("claude not installed")
	}
	dataDir, workDir := t.TempDir(), t.TempDir()
	c := New(bin, 3*time.Minute, t.TempDir(), dataDir).WithSandbox(true).
		WithDir(workDir).WithAllowedTools("Bash,Write,Read")
	c.selfExe = self

	prompt := "Create a file named proof.txt in the current directory containing exactly the text STDIN-OK and nothing else. Then reply DONE.\n\n" +
		strings.Repeat(`Filler: "quoted words" and more words. `, 1200)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	res, err := c.Generate(ctx, "wsLive", prompt)
	if err != nil {
		t.Fatalf("sandboxed Generate: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(workDir, "proof.txt"))
	if err != nil || strings.TrimSpace(string(got)) != "STDIN-OK" {
		t.Fatalf("proof.txt = %q (err %v); reply %q", got, err, res.Text)
	}
	t.Logf("sandboxed claude wrote proof.txt; reply=%q", res.Text)
}

func lookBin(name string) string {
	p, _ := exec.LookPath(name)
	return p
}
