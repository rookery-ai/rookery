package coder

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ilijad1/simple-agents/internal/llm"
)

// TestReadFileSlicePaging exercises the byte-range paging contract of readFileSlice:
// the default (0,0) path is byte-identical to truncate(), a window returns exactly the
// requested bytes with a next-offset hint when more remains, and out-of-range offsets
// are clamped rather than panicking.
func TestReadFileSlicePaging(t *testing.T) {
	small := "hello world"
	if got := readFileSlice(small, 0, 0); got != small {
		t.Fatalf("default (0,0) must be identical to the raw small content; got %q", got)
	}

	big := strings.Repeat("A", maxToolResult*3)
	// Default path on an over-cap file is exactly truncate() (same escape-hatch notice).
	if got := readFileSlice(big, 0, 0); got != truncate(big) {
		t.Fatalf("default path on a large file must equal truncate(); diverged")
	}

	// A limited window returns exactly `limit` bytes plus a next-offset hint.
	win := readFileSlice(big, 100, 50)
	if !strings.HasPrefix(win, strings.Repeat("A", 50)) {
		t.Fatalf("window should start with the 50 requested bytes; got prefix %q", win[:min(60, len(win))])
	}
	if !strings.Contains(win, "offset=150") {
		t.Fatalf("window with more bytes remaining must hint the next offset (150); got %q", win)
	}

	// A window that reaches EOF carries no next-offset hint.
	tail := readFileSlice("0123456789", 8, 100)
	if tail != "89" {
		t.Fatalf("tail window should be exactly the remaining bytes; got %q", tail)
	}

	// Offset past EOF is clamped and reported, not a panic/empty slice.
	past := readFileSlice("short", 999, 10)
	if !strings.Contains(past, "no bytes at offset") {
		t.Fatalf("out-of-range offset should be reported; got %q", past)
	}
}

// TestTruncateNotice: an over-cap string reports the true total size and the escape hatch,
// and stays within the size budget the web_fetch truncation test relies on.
func TestTruncateNotice(t *testing.T) {
	total := maxToolResult * 4
	got := truncate(strings.Repeat("Z", total))
	if !strings.Contains(got, "of "+strconv.Itoa(total)+" bytes") {
		t.Fatalf("truncate notice must state the true total (%d); got tail %q", total, got[len(got)-160:])
	}
	if !strings.Contains(got, "read_file offset/limit") {
		t.Fatalf("truncate notice must point at the paging escape hatch; got tail %q", got[len(got)-160:])
	}
	if len(got) > maxToolResult+512 {
		t.Fatalf("truncated result must stay under maxToolResult+512 (web_fetch test budget); got %d", len(got))
	}
}

// TestRunScriptSpillsLargeOutput: a script whose stdout exceeds the per-result cap has its FULL
// output persisted to .sa_out/ under the agent workDir, and the model-facing result is a compact
// head + a steering pointer (not the whole payload). This is the primary fix — the large output
// reaches the filesystem without transiting the model context.
func TestRunScriptSpillsLargeOutput(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	dir := t.TempDir()
	toolsDir := filepath.Join(dir, "tools")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Print ~3x the cap of a recognizable payload to stdout.
	script := "import sys\nsys.stdout.write('B' * " + strconv.Itoa(maxToolResult*3) + ")\n"
	if err := os.WriteFile(filepath.Join(toolsDir, "big.py"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &hostToolSet{
		workspaceID:      "wsSpill",
		includeExecTools: true,
		sandbox:          false,
		workDir:          dir,
		homesDir:         dir,
	}
	res := h.execute(context.Background(), toolCall("run_script", `{"path":"tools/big.py"}`))
	if strings.HasPrefix(res, "error:") {
		t.Fatalf("run_script should succeed; got %q", res)
	}
	if len(res) >= maxToolResult*3 {
		t.Fatalf("model-facing result must be a compact head, not the full payload; got len %d", len(res))
	}
	if !strings.Contains(res, spillDirName) || !strings.Contains(strings.ToLower(res), "saved in full") {
		t.Fatalf("result must point at the spill file and steer the model; got %q", res)
	}
	// The full output must exist on disk under .sa_out/.
	spillDir := filepath.Join(dir, spillDirName)
	entries, err := os.ReadDir(spillDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected a spill file under %s; err=%v entries=%d", spillDir, err, len(entries))
	}
	data, err := os.ReadFile(filepath.Join(spillDir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != maxToolResult*3 {
		t.Fatalf("spill file must hold the FULL output (%d bytes); got %d", maxToolResult*3, len(data))
	}
}

// TestSpillPreservesVerification: spilling a large output must not defeat the build-time script
// verification bridge — the returned head is real, non-empty output, so an authored script that
// spills still latches producedOutput / clears the verification gate.
func TestSpillPreservesVerification(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	dir := t.TempDir()
	toolsDir := filepath.Join(dir, "tools")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte("# agent\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	script := "import sys\nsys.stdout.write('C' * " + strconv.Itoa(maxToolResult*3) + ")\n"
	if err := os.WriteFile(filepath.Join(toolsDir, "fetch.py"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &hostToolSet{
		workspaceID:      "wsSpillVerify",
		includeExecTools: true,
		sandbox:          false,
		verifyBuild:      true,
		workDir:          dir,
		homesDir:         dir,
	}
	// Mark the script authored, then run it (via executeOrNudge so trackScriptProgress fires).
	h.trackScriptProgress(toolCall("write_file", `{"path":"tools/fetch.py"}`), "ok", false)
	if !h.needsScriptVerification() {
		t.Fatal("authored-but-never-run script should need verification")
	}
	res := h.executeOrNudge(context.Background(), toolCall("run_script", `{"path":"tools/fetch.py"}`))
	if strings.HasPrefix(res, "error:") {
		t.Fatalf("run_script should succeed; got %q", res)
	}
	if h.needsScriptVerification() {
		t.Fatal("a script that ran and produced (spilled) real output must clear the verification gate")
	}
	if !h.scriptVerified() {
		t.Fatal("scriptVerified() should report true after a real (spilled) run")
	}
}

// TestAPIEngine_RunScriptSpillDoesNotAccumulateInHistory is the loop-level guard for the actual
// context-blowup vector: after a script prints a huge stdout, the message the engine appends to
// req.Messages (and re-sends on every subsequent Complete) must be the small spill head + pointer,
// NOT the full payload. Unit tests prove spillLargeOutput in isolation; this proves the payload
// doesn't pile up in the conversation the way it did on the 856K-token failing run.
func TestAPIEngine_RunScriptSpillDoesNotAccumulateInHistory(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	dir := t.TempDir()
	ws := "wsSpillLoop"
	agentDir := filepath.Join(dir, "vaults", ws, "agents", "agent1")
	mustMkdir(t, agentDir)
	big := strconv.Itoa(maxToolResult * 3)
	mustWrite(t, filepath.Join(agentDir, "tools", "big.py"),
		[]byte("import sys\nsys.stdout.write('B' * "+big+")\n"))

	c := newTestCoder(t, dir).WithDir(agentDir)

	var toolMsg string
	testFake.calls = 0
	testFake.script = func(call int, req llm.Request) (*llm.Response, error) {
		if call == 0 {
			return &llm.Response{ToolCalls: []llm.ToolCall{
				toolCall("run_script", `{"path":"tools/big.py"}`),
			}}, nil
		}
		toolMsg = lastToolResult(req) // the tool-role message now sitting in the history
		return &llm.Response{Content: "[CHAT] done"}, nil
	}

	if _, err := c.Generate(context.Background(), ws, "run the big helper"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if toolMsg == "" {
		t.Fatal("expected a tool result in the message history")
	}
	if len(toolMsg) >= maxToolResult*3 {
		t.Fatalf("the full payload leaked into the message history (len %d) — this is the blowup vector", len(toolMsg))
	}
	if !strings.Contains(toolMsg, spillDirName) {
		t.Fatalf("tool result in history must be the spill head+pointer; got %q", toolMsg[:min(200, len(toolMsg))])
	}
	// The full output is on disk, reachable without transiting the model context.
	entries, err := os.ReadDir(filepath.Join(agentDir, spillDirName))
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected the full output spilled to disk; err=%v entries=%d", err, len(entries))
	}
}

// TestClearSpillDir removes stale spill files at run start so .sa_out can't grow unbounded.
func TestClearSpillDir(t *testing.T) {
	dir := t.TempDir()
	spillDir := filepath.Join(dir, spillDirName)
	if err := os.MkdirAll(spillDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(spillDir, "old.txt"), []byte("stale"), 0o640); err != nil {
		t.Fatal(err)
	}
	h := &hostToolSet{workDir: dir}
	h.clearSpillDir()
	if _, err := os.Stat(spillDir); !os.IsNotExist(err) {
		t.Fatalf("clearSpillDir must remove the spill dir; stat err=%v", err)
	}
}
