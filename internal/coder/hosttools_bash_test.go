package coder

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/llm"
)

// newBashToolSet builds a hostToolSet wired for bash tests: exec tools on, sandbox off
// (Landlock is exercised separately), workDir + homesDir under a temp dir.
func newBashToolSet(t *testing.T) *hostToolSet {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	dir := t.TempDir()
	return &hostToolSet{
		workspaceID:      "wsBash",
		includeExecTools: true,
		sandbox:          false,
		workDir:          dir,
		homesDir:         dir,
	}
}

func bashCall(cmd string) llm.ToolCall {
	b, _ := json.Marshal(map[string]string{"command": cmd})
	return llm.ToolCall{Name: "bash", Args: b}
}

// TestBashEcho: a simple command returns its stdout.
func TestBashEcho(t *testing.T) {
	h := newBashToolSet(t)
	res := h.execute(context.Background(), bashCall("echo hello-from-bash"))
	if !strings.Contains(res, "hello-from-bash") {
		t.Fatalf("bash should return stdout; got: %q", res)
	}
	if strings.HasPrefix(res, "error:") {
		t.Fatalf("successful command must not be an error; got: %q", res)
	}
}

// TestBashNonzeroReportsStreams: a failing command surfaces BOTH stdout and stderr so the
// model can self-correct (mirrors run_script).
func TestBashNonzeroReportsStreams(t *testing.T) {
	h := newBashToolSet(t)
	res := h.execute(context.Background(), bashCall("echo out-line; echo err-line >&2; exit 3"))
	if !strings.HasPrefix(res, "error:") {
		t.Fatalf("nonzero exit should be an error result; got: %q", res)
	}
	if !strings.Contains(res, "out-line") || !strings.Contains(res, "err-line") {
		t.Fatalf("bash failure must report both stdout and stderr; got: %q", res)
	}
}

// TestBashSeesSecretEnv: the agent's secrets (subprocessEnv) are visible to the command.
func TestBashSeesSecretEnv(t *testing.T) {
	h := newBashToolSet(t)
	h.subprocessEnv = map[string]string{"MY_TOKEN": "s3cr3t-value"}
	res := h.execute(context.Background(), bashCall(`echo "tok=$MY_TOKEN"`))
	if !strings.Contains(res, "tok=s3cr3t-value") {
		t.Fatalf("bash should see the agent's secret env vars; got: %q", res)
	}
}

// TestBashDisabledWhenNotExec: without exec tools the tool refuses (chat parity).
func TestBashDisabledWhenNotExec(t *testing.T) {
	h := &hostToolSet{includeExecTools: false}
	res := h.execute(context.Background(), bashCall("echo hi"))
	if !strings.HasPrefix(res, "error:") {
		t.Fatalf("bash must be unavailable when exec tools are off; got: %q", res)
	}
}
