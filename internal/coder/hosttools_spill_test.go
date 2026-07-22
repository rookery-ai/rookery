package coder

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

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

// TestTruncateIsRuneSafe pins Fix 6: a raw byte-slice cut at maxToolResult must
// never land inside a multi-byte UTF-8 character (this operator's notes are
// routinely Cyrillic). Sweeping every possible boundary position — by growing
// the ASCII prefix one byte at a time so the multi-byte rune's position
// relative to the cut point varies — exercises landing on both the first and
// second byte of the two-byte character, without relying on luck.
func TestTruncateIsRuneSafe(t *testing.T) {
	for pad := 0; pad < 4; pad++ {
		s := strings.Repeat("a", maxToolResult+pad) + "ж" + strings.Repeat("b", 100)
		got := truncate(s)
		shown := strings.SplitN(got, "\n…[truncated:", 2)[0]
		if !utf8.ValidString(shown) {
			t.Fatalf("pad=%d: truncated output is not valid UTF-8: %q", pad, shown)
		}
	}
}

// TestReadFileSliceWindowIsRuneSafe pins Fix 6's other half: the paging window
// cut must also never split a multi-byte character, AND the next-offset hint
// must advance by the ACTUAL (possibly floored) cut length — not the nominal
// limit — or the bytes between the floored cut and the nominal limit are
// silently skipped and never shown on any page.
func TestReadFileSliceWindowIsRuneSafe(t *testing.T) {
	for pad := 0; pad < 4; pad++ {
		content := strings.Repeat("a", pad) + "ж" + strings.Repeat("b", 100)
		limit := pad + 1 // lands the window boundary at/inside the 2-byte rune
		got := readFileSlice(content, 0, limit)
		window := strings.SplitN(got, "\n…[", 2)[0]
		if !utf8.ValidString(window) {
			t.Fatalf("pad=%d, limit=%d: window is not valid UTF-8: %q", pad, limit, window)
		}
		// Reassemble every page using each page's own next-offset hint and
		// confirm no bytes are skipped or duplicated across the boundary.
		var rebuilt strings.Builder
		offset := 0
		maxPages := len(content) + 5 // each page advances by at least 1 byte, so this always terminates
		for i := 0; i < maxPages; i++ {
			page := readFileSlice(content, offset, limit)
			body := strings.SplitN(page, "\n…[", 2)[0]
			rebuilt.WriteString(body)
			next := -1
			if idx := strings.Index(page, "offset="); idx >= 0 {
				fmt.Sscanf(page[idx:], "offset=%d]", &next)
			}
			if next < 0 {
				break
			}
			offset = next
		}
		if rebuilt.String() != content {
			t.Fatalf("pad=%d, limit=%d: paging through the window lost or duplicated bytes: got %q, want %q",
				pad, limit, rebuilt.String(), content)
		}
	}
}

// TestSpillLargeOutputHeadIsRuneSafe pins Fix 6's third half: the inline head
// shown alongside the spill-file pointer must never split a multi-byte
// character, even though the FULL output (unaffected by this cut) is still
// persisted to disk byte-for-byte.
func TestSpillLargeOutputHeadIsRuneSafe(t *testing.T) {
	dir := t.TempDir()
	h := &hostToolSet{workspaceID: "wsSpillHead", workDir: dir}
	for pad := 0; pad < 4; pad++ {
		out := strings.Repeat("a", spillHeadBytes+pad) + "ж" + strings.Repeat("b", maxToolResult*2)
		got := h.spillLargeOutput(out, "run_script")
		head := strings.SplitN(got, "\n…[output is", 2)[0]
		if !utf8.ValidString(head) {
			t.Fatalf("pad=%d: spill head is not valid UTF-8: %q", pad, head)
		}
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
