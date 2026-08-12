package coder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ilijad1/rookery/internal/buildphase"
	"github.com/ilijad1/rookery/internal/db"
	"github.com/ilijad1/rookery/internal/llm"
	"github.com/ilijad1/rookery/internal/vault"
)

// fakeProvider is an in-process llm.Provider used to drive the API engine in
// tests without touching the network. Each test sets testFake.script to a
// function of (call index, current request) → response/error.
type fakeProvider struct {
	script func(call int, req llm.Request) (*llm.Response, error)
	calls  int
}

func (f *fakeProvider) Name() string { return "fake" }
func (f *fakeProvider) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	c := f.calls
	f.calls++
	return f.script(c, req)
}

// testFake is the singleton returned by the "fake" provider factory. Tests set
// its script before invoking the engine.
var testFake = &fakeProvider{}

func init() {
	llm.RegisterProvider("fake", func(cfg llm.Config) (llm.Provider, error) {
		return testFake, nil
	})
}

func toolCall(name, args string) llm.ToolCall {
	return llm.ToolCall{ID: name + "-1", Name: name, Args: json.RawMessage(args)}
}

// lastToolResult finds the most recent "tool" message content in req, or "".
func lastToolResult(req llm.Request) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "tool" {
			return req.Messages[i].Content
		}
	}
	return ""
}

// lastUserMessage finds the most recent "user" message content in req, or "".
func lastUserMessage(req llm.Request) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			return req.Messages[i].Content
		}
	}
	return ""
}

// newTestCoder builds an API coder wired to the fake provider + a vault on a
// temp data dir. The provider key is resolved from a stub secrets lookup.
func newTestCoder(t *testing.T, dataDir string) *Coder {
	t.Helper()
	vlt := vault.New(dataDir)
	return New("claude", time.Minute, "", dataDir).
		WithSandbox(false).
		WithVault(vlt).
		WithAPIConfig("fake", "fake-model", "fake://test", "PROVIDER_KEY").
		WithSecretsLookup(func(_ context.Context, _, name string) (string, error) {
			if name == "PROVIDER_KEY" {
				return "test-key", nil
			}
			return "", fmt.Errorf("unknown secret %q", name)
		})
}

func TestAPIEngine_LoopTerminatesWithChat(t *testing.T) {
	dir := t.TempDir()
	ws := "ws1"
	c := newTestCoder(t, dir)
	mustMkdir(t, filepath.Join(dir, "vaults", ws))

	testFake.calls = 0
	testFake.script = func(call int, _ llm.Request) (*llm.Response, error) {
		if call == 0 {
			return &llm.Response{ToolCalls: []llm.ToolCall{
				toolCall("write_file", `{"path":"notes.txt","content":"hello vault"}`),
			}}, nil
		}
		return &llm.Response{Content: "[CHAT] wrote the note"}, nil
	}

	res, err := c.Generate(context.Background(), ws, "you are a helpful agent")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(res.Text, "[CHAT] wrote the note") {
		t.Fatalf("result = %q, want [CHAT] wrote the note", res.Text)
	}
	// The tool call must have actually written the file to the vault.
	data, err := os.ReadFile(filepath.Join(dir, "vaults", ws, "notes.txt"))
	if err != nil {
		t.Fatalf("notes.txt not written: %v", err)
	}
	if string(data) != "hello vault" {
		t.Fatalf("notes.txt = %q, want %q", data, "hello vault")
	}
}

func TestAPIEngine_RunScriptExecutesAndInjectsStdout(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	dir := t.TempDir()
	ws := "ws2"
	vaultRoot := filepath.Join(dir, "vaults", ws)
	agentDir := filepath.Join(vaultRoot, "agents", "agent1")
	mustMkdir(t, agentDir)
	mustWrite(t, filepath.Join(agentDir, "tools", "hello.py"), []byte("print('hello-from-script')\n"))

	c := newTestCoder(t, dir).WithDir(agentDir)

	testFake.calls = 0
	testFake.script = func(call int, req llm.Request) (*llm.Response, error) {
		if call == 0 {
			return &llm.Response{ToolCalls: []llm.ToolCall{
				toolCall("run_script", `{"path":"tools/hello.py"}`),
			}}, nil
		}
		return &llm.Response{Content: "[CHAT] script said: " + lastToolResult(req)}, nil
	}

	res, err := c.Generate(context.Background(), ws, "run the helper")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(res.Text, "hello-from-script") {
		t.Fatalf("result = %q, want the script stdout injected", res.Text)
	}
}

func TestAPIEngine_RunScriptIsolatesHomeAndTmpdir(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	dataDir := t.TempDir()
	homesDir := t.TempDir()
	ws := "ws-home"
	vaultRoot := filepath.Join(dataDir, "vaults", ws)
	agentDir := filepath.Join(vaultRoot, "agents", "agent1")
	mustMkdir(t, agentDir)
	mustWrite(t, filepath.Join(agentDir, "tools", "envdump.py"), []byte(
		"import os\nprint('HOME=' + os.environ.get('HOME', ''))\nprint('TMPDIR=' + os.environ.get('TMPDIR', ''))\n"))

	vlt := vault.New(dataDir)
	c := New("claude", time.Minute, homesDir, dataDir).
		WithSandbox(false).
		WithVault(vlt).
		WithAPIConfig("fake", "fake-model", "fake://test", "PROVIDER_KEY").
		WithSecretsLookup(func(_ context.Context, _, name string) (string, error) {
			return "test-key", nil
		}).
		WithDir(agentDir)

	testFake.calls = 0
	testFake.script = func(call int, req llm.Request) (*llm.Response, error) {
		if call == 0 {
			return &llm.Response{ToolCalls: []llm.ToolCall{
				toolCall("run_script", `{"path":"tools/envdump.py"}`),
			}}, nil
		}
		return &llm.Response{Content: "[CHAT] " + lastToolResult(req)}, nil
	}

	res, err := c.Generate(context.Background(), ws, "check env")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	wantHome := filepath.Join(homesDir, ws)
	if !strings.Contains(res.Text, "HOME="+wantHome) {
		t.Fatalf("result = %q, want HOME isolated to %q (not the real process HOME)", res.Text, wantHome)
	}
	wantTmp := filepath.Join(wantHome, "tmp")
	if !strings.Contains(res.Text, "TMPDIR="+wantTmp) {
		t.Fatalf("result = %q, want TMPDIR isolated to %q (not shared /tmp)", res.Text, wantTmp)
	}
	if fi, err := os.Stat(wantTmp); err != nil || !fi.IsDir() {
		t.Fatalf("expected isolated tmp dir %q to exist: %v", wantTmp, err)
	}
}

func TestAPIEngine_TransientRateLimitMapsToErrRateLimited(t *testing.T) {
	dir := t.TempDir()
	ws := "ws3"
	c := newTestCoder(t, dir)
	mustMkdir(t, filepath.Join(dir, "vaults", ws))

	testFake.calls = 0
	testFake.script = func(int, llm.Request) (*llm.Response, error) {
		return nil, llm.ErrRateLimit
	}

	_, err := c.Generate(context.Background(), ws, "anything")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited (transient throttle, not quota)", err)
	}
	// A transient throttle must NOT be misreported as quota exhaustion.
	if errors.Is(err, ErrUsageLimit) {
		t.Fatalf("err = %v, transient 429 must not map to ErrUsageLimit", err)
	}
}

func TestAPIEngine_QuotaExhaustedMapsToErrUsageLimit(t *testing.T) {
	dir := t.TempDir()
	ws := "ws-quota"
	c := newTestCoder(t, dir)
	mustMkdir(t, filepath.Join(dir, "vaults", ws))

	testFake.calls = 0
	testFake.script = func(int, llm.Request) (*llm.Response, error) {
		return nil, llm.ErrQuotaExhausted
	}

	_, err := c.Generate(context.Background(), ws, "anything")
	if !errors.Is(err, ErrUsageLimit) {
		t.Fatalf("err = %v, want ErrUsageLimit (quota/credits exhausted)", err)
	}
}

func TestAPIEngine_PerToolCallProgressMilestones(t *testing.T) {
	dir := t.TempDir()
	ws := "ws-prog"
	c := newTestCoder(t, dir)
	mustMkdir(t, filepath.Join(dir, "vaults", ws))

	var milestones []string
	c = c.WithProgress(func(msg string) { milestones = append(milestones, msg) })

	testFake.calls = 0
	testFake.script = func(call int, _ llm.Request) (*llm.Response, error) {
		switch call {
		case 0:
			return &llm.Response{ToolCalls: []llm.ToolCall{
				toolCall("write_file", `{"path":"a.txt","content":"x"}`),
				toolCall("read_file", `{"path":"b.txt"}`),
			}}, nil
		case 1:
			return &llm.Response{Content: "[CHAT] done"}, nil
		}
		return &llm.Response{Content: "[CHAT] done"}, nil
	}

	if _, err := c.Generate(context.Background(), ws, "go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(milestones) != 2 {
		t.Fatalf("got %d milestones, want 2: %v", len(milestones), milestones)
	}
	if milestones[0] != "🔧 write_file(a.txt)" {
		t.Fatalf("milestone[0] = %q", milestones[0])
	}
	if milestones[1] != "🔧 read_file(b.txt)" {
		t.Fatalf("milestone[1] = %q", milestones[1])
	}
}

// TestToolMilestoneShowsQueryPatternURL guards the observability extension: the
// live progress stream shows the most useful identifier per tool — path for file
// tools, query for search_files/web_search, pattern for glob, url for web_fetch —
// instead of a raw JSON arg blob. The detail is capped so a long query/URL can't
// blow out the milestone line.
func TestToolMilestoneShowsQueryPatternURL(t *testing.T) {
	cases := []struct{ name, args, want string }{
		{"search_files", `{"query":"dentist appointment"}`, "🔧 search_files(dentist appointment)"},
		{"glob", `{"pattern":"notes/*-meeting.md"}`, "🔧 glob(notes/*-meeting.md)"},
		{"web_search", `{"query":"weather skopje"}`, "🔧 web_search(weather skopje)"},
		{"web_fetch", `{"url":"https://example.com/x"}`, "🔧 web_fetch(https://example.com/x)"},
		{"read_file", `{"path":"notes.md"}`, "🔧 read_file(notes.md)"},
	}
	for _, c := range cases {
		if got := toolMilestone(toolCall(c.name, c.args), "", ""); got != c.want {
			t.Errorf("toolMilestone(%s) = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestToolMilestoneShowsBashCommand covers the tool that was WORST served by the
// old renderer. bash's only argument is `command`, which matched none of the
// extracted fields, so every shell step an agent took reached the user as a
// truncated raw-JSON blob: `🔧 bash({"command": "cd /home/user/…`.
func TestToolMilestoneShowsBashCommand(t *testing.T) {
	got := toolMilestone(toolCall("bash", `{"command":"date -u +%Y-%m-%d"}`), "", "")
	if want := "🔧 bash(date -u +%Y-%m-%d)"; got != want {
		t.Errorf("toolMilestone(bash) = %q, want %q", got, want)
	}
	// save_to_kb had the same defect for the same reason: its subject arrives as
	// `source`, which matched none of the extracted fields either.
	got = toolMilestone(toolCall("save_to_kb", `{"source":"downloads/report.pdf"}`), "", "")
	if want := "🔧 save_to_kb(downloads/report.pdf)"; got != want {
		t.Errorf("toolMilestone(save_to_kb) = %q, want %q", got, want)
	}
	// The raw-JSON fallback still applies to a call with no recognised subject —
	// better a truncated blob than a bare tool name with no context at all.
	got = toolMilestone(toolCall("mystery", `{"unknown":"xyz"}`), "", "")
	if want := `🔧 mystery({"unknown":"xyz"})`; got != want {
		t.Errorf("toolMilestone(unknown args) = %q, want %q", got, want)
	}
}

// TestToolMilestoneStripsHostPaths pins the second half of the same defect: even
// with `command` extracted, the command TEXT embeds the absolute vault path, so
// the progress stream read as a tour of the server's filesystem. The vault root
// collapses away entirely (it is the user's "here") and the workspace home
// becomes "~".
func TestToolMilestoneStripsHostPaths(t *testing.T) {
	const (
		vaultRoot = "/home/user/.rookery/vaults/fd11c47e-646e-48e0-9ef8-fc54a2f184ac"
		homeDir   = "/home/user/.rookery/claude-homes/fd11c47e-646e-48e0-9ef8-fc54a2f184ac"
	)
	cases := []struct{ name, args, want string }{
		{"bash", `{"command":"cd ` + vaultRoot + `/notes && ls"}`, "🔧 bash(cd notes && ls)"},
		// A bare reference to the root is the vault's "here", not an empty string.
		{"bash", `{"command":"cd ` + vaultRoot + `"}`, "🔧 bash(cd .)"},
		{"bash", `{"command":"cat ` + homeDir + `/.cache/x"}`, "🔧 bash(cat ~/.cache/x)"},
		// A model that types an absolute path to a file tool is cleaned too.
		{"read_file", `{"path":"` + vaultRoot + `/notes/trip.md"}`, "🔧 read_file(notes/trip.md)"},
		// Paths that are NOT one of the two known roots are meaningful to the
		// user and must survive untouched, leading slash included.
		{"bash", `{"command":"/usr/bin/python3 --version"}`, "🔧 bash(/usr/bin/python3 --version)"},
	}
	for _, c := range cases {
		if got := toolMilestone(toolCall(c.name, c.args), vaultRoot, homeDir); got != c.want {
			t.Errorf("toolMilestone(%s, %s):\n got %q\nwant %q", c.name, c.args, got, c.want)
		}
	}
}

// TestShortenHostPathsLeavesSiblingDirs: a directory that merely STARTS with the
// vault root's name ("<root>-backup") is a different directory, and collapsing it
// as though it were the root would misreport which path the agent touched.
// Asserted on shortenHostPaths directly rather than through toolMilestone, whose
// 60-rune cap would truncate these long paths before the assertion could see the
// difference.
func TestShortenHostPathsLeavesSiblingDirs(t *testing.T) {
	const vaultRoot = "/data/vault"
	cases := []struct{ in, want string }{
		{"ls /data/vault-backup", "ls /data/vault-backup"},
		{"ls /data/vault/notes", "ls notes"},
		{"ls /data/vault", "ls ."},
		// Repeated occurrences in one command line are all rewritten.
		{"cp /data/vault/a.md /data/vault/b.md", "cp a.md b.md"},
	}
	for _, c := range cases {
		if got := shortenHostPaths(c.in, vaultRoot, ""); got != c.want {
			t.Errorf("shortenHostPaths(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestShortenHostPathsIgnoresDegenerateRoots: an unset vault/home (empty string)
// must not become a matching prefix. filepath.Clean("") is ".", and "/" cleans to
// itself — either would match essentially every path in a command line and
// shred it.
func TestShortenHostPathsIgnoresDegenerateRoots(t *testing.T) {
	const cmd = "cd /home/user/notes && ls ."
	for _, root := range []string{"", ".", "/", "   "} {
		if got := shortenHostPaths(cmd, root, root); got != cmd {
			t.Errorf("shortenHostPaths with degenerate root %q rewrote the command: %q", root, got)
		}
	}
}

// TestToolMilestoneTruncatesAfterShortening guards the ordering: truncating
// before stripping the host path would spend the whole character budget on the
// absolute prefix and cut away the part that says what the command does.
func TestToolMilestoneTruncatesAfterShortening(t *testing.T) {
	const vaultRoot = "/home/user/.rookery/vaults/fd11c47e-646e-48e0-9ef8-fc54a2f184ac"
	got := toolMilestone(toolCall("bash", `{"command":"cd `+vaultRoot+`/notes && grep -r todo ."}`), vaultRoot, "")
	if want := "🔧 bash(cd notes && grep -r todo .)"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestTruncateRunesDoesNotSplitRunes: the details rendered here routinely carry
// non-ASCII (note titles, search queries, emoji in filenames). Byte slicing can
// cut a multi-byte rune in half and emit U+FFFD into the user's stream.
func TestTruncateRunesDoesNotSplitRunes(t *testing.T) {
	got := truncateRunes(strings.Repeat("é", 80), 60)
	if want := strings.Repeat("é", 60) + "…"; got != want {
		t.Errorf("truncateRunes mangled multi-byte runes: got %q", got)
	}
	if strings.ContainsRune(got, '�') {
		t.Error("truncateRunes produced a replacement char")
	}
}

func TestAPIEngine_RunawayLoopHitsErrMaxTurns(t *testing.T) {
	dir := t.TempDir()
	ws := "ws4"
	c := newTestCoder(t, dir)
	mustMkdir(t, filepath.Join(dir, "vaults", ws))

	testFake.calls = 0
	// Every turn requests another tool call → the engine exhausts maxAPITurns.
	testFake.script = func(int, llm.Request) (*llm.Response, error) {
		return &llm.Response{ToolCalls: []llm.ToolCall{
			toolCall("write_file", `{"path":"x.txt","content":"x"}`),
		}}, nil
	}

	_, err := c.Generate(context.Background(), ws, "loop forever")
	if !errors.Is(err, ErrMaxTurns) {
		t.Fatalf("err = %v, want ErrMaxTurns", err)
	}
}

func TestAPIEngine_VaultPathEscapeRejected(t *testing.T) {
	dir := t.TempDir()
	ws := "ws5"
	vlt := vault.New(dir)
	h := &hostToolSet{workspaceID: ws, vlt: vlt}

	for _, bad := range []string{"../escape.txt", "../../etc/passwd"} {
		if _, err := h.resolveVault(bad); err == nil {
			t.Fatalf("resolveVault(%q) = nil err, want escape rejected", bad)
		}
	}
	// A vault-relative path resolves inside the vault root.
	abs, err := h.resolveVault("notes/todo.md")
	if err != nil {
		t.Fatalf("resolveVault(notes/todo.md): %v", err)
	}
	if !strings.HasPrefix(abs, vlt.Root(ws)+string(os.PathSeparator)) {
		t.Fatalf("resolved %q outside vault root %q", abs, vlt.Root(ws))
	}
}

// TestAPIEngine_EmptyToolResultNormalized guards the Mistral-422 root cause: a tool
// call that yields empty output (e.g. a run_script with no stdout, or a read_file of an
// empty file) must NOT produce an empty tool-result string — an empty tool message is
// dropped by the OpenAI-compatible serializer and rejected by Mistral (422), failing the
// run. executeOrNudge must normalize it to a non-empty placeholder.
func TestAPIEngine_EmptyToolResultNormalized(t *testing.T) {
	dir := t.TempDir()
	ws := "wsEmpty"
	vlt := vault.New(dir)
	root := vlt.Root(ws)
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "empty.md"), []byte(""), 0o640); err != nil {
		t.Fatal(err)
	}
	h := &hostToolSet{workspaceID: ws, vlt: vlt, workDir: root}

	res := h.executeOrNudge(context.Background(), llm.ToolCall{
		Name: "read_file",
		Args: json.RawMessage(`{"path":"empty.md"}`),
	})
	if strings.TrimSpace(res) == "" {
		t.Fatal("empty read_file result must be normalized to a non-empty placeholder")
	}
}

// TestAPIEngine_BuildVerifyLoopDrivesScriptFix guards the weak-model reliability fix:
// during a build the engine must NOT accept "done" while the model has authored a
// helper script it never got real output from — it nudges the model to run and fix the
// script, and once the script returns real data the build is allowed to finish.
func TestAPIEngine_BuildVerifyLoopDrivesScriptFix(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	dir := t.TempDir()
	ws := "wsVerify"
	agentDir := filepath.Join(dir, "vaults", ws, "agents", "a1")
	mustMkdir(t, agentDir)

	c := newTestCoder(t, dir).
		WithDir(agentDir).
		WithExtraEnv(map[string]string{buildphase.EnvVar: buildphase.Generation})

	nudged := false
	testFake.calls = 0
	testFake.script = func(call int, req llm.Request) (*llm.Response, error) {
		switch call {
		case 0: // author AGENT.md + a working script (AGENT.md first so gate 1 is satisfied)
			return &llm.Response{ToolCalls: []llm.ToolCall{
				toolCall("write_file", `{"path":"AGENT.md","content":"# agent\n"}`),
				toolCall("write_file", `{"path":"tools/fetch.py","content":"print('REAL_DATA')\n"}`),
			}}, nil
		case 1: // try to finish WITHOUT running the script → gate 2 must nudge
			return &llm.Response{Content: "[CHAT] all done"}, nil
		case 2: // responded to the nudge → actually run it
			if strings.Contains(strings.ToLower(lastUserMessage(req)), "broken") {
				nudged = true
			}
			return &llm.Response{ToolCalls: []llm.ToolCall{
				toolCall("run_script", `{"path":"tools/fetch.py"}`),
			}}, nil
		default: // now finish — script produced output, so no more nudges
			return &llm.Response{Content: "[CHAT] all done"}, nil
		}
	}

	res, err := c.Generate(context.Background(), ws, "build it")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !nudged {
		t.Error("expected the engine to nudge the model to run its unverified script")
	}
	if !strings.Contains(res.Text, "all done") {
		t.Errorf("expected the build to finish once the script produced output; got %q", res.Text)
	}
	// The engine's ground truth must be surfaced on the Result so the agent designer can
	// present the build as verified with real output — even though the model never emitted
	// a [TEST_OUTPUT] marker.
	if !res.ScriptVerified {
		t.Error("expected Result.ScriptVerified=true after the authored script ran with real output")
	}
	if !strings.Contains(res.ScriptOutput, "REAL_DATA") {
		t.Errorf("expected Result.ScriptOutput to carry the captured stdout; got %q", res.ScriptOutput)
	}
}

// TestAPIEngine_SeededScriptDoesNotSatisfyVerification guards the correctness fix: a
// Composio build runs the SEEDED composio_discover.py early (it returns output), but that
// must NOT count as verifying the model's OWN authored script — otherwise the verify loop
// silently never fires for Composio agents. The model's tools/fetch.py never runs, so the
// engine must still nudge.
func TestAPIEngine_SeededScriptDoesNotSatisfyVerification(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	dir := t.TempDir()
	ws := "wsSeeded"
	agentDir := filepath.Join(dir, "vaults", ws, "agents", "a1")
	mustMkdir(t, agentDir)
	// A pre-existing helper (NOT authored during this build) exists and returns output.
	mustWrite(t, filepath.Join(agentDir, "tools", "preexisting.py"), []byte("print('SLUGS')\n"))

	c := newTestCoder(t, dir).
		WithDir(agentDir).
		WithExtraEnv(map[string]string{buildphase.EnvVar: buildphase.Generation})

	nudged := false
	testFake.calls = 0
	testFake.script = func(call int, req llm.Request) (*llm.Response, error) {
		switch call {
		case 0: // author AGENT.md + own fetch script (never run)
			return &llm.Response{ToolCalls: []llm.ToolCall{
				toolCall("write_file", `{"path":"AGENT.md","content":"# agent\n"}`),
				toolCall("write_file", `{"path":"tools/fetch.py","content":"pass\n"}`),
			}}, nil
		case 1: // run the PRE-EXISTING helper (returns output, must NOT satisfy verification of fetch.py)
			return &llm.Response{ToolCalls: []llm.ToolCall{
				toolCall("run_script", `{"path":"tools/preexisting.py"}`),
			}}, nil
		default: // try to finish — fetch.py never ran, so must still be nudged
			if strings.Contains(strings.ToLower(lastUserMessage(req)), "broken") {
				nudged = true
			}
			return &llm.Response{Content: "[CHAT] done"}, nil
		}
	}

	if _, err := c.Generate(context.Background(), ws, "build it"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !nudged {
		t.Error("seeded composio_discover.py output must NOT satisfy verification of the model's own script")
	}
}

// TestAPIEngine_BuildVerifyLoopIsBounded guards that the verify loop gives up after
// maxVerifyNudges (does not loop forever) when the model never gets the script working.
func TestAPIEngine_BuildVerifyLoopIsBounded(t *testing.T) {
	dir := t.TempDir()
	ws := "wsVerifyBounded"
	agentDir := filepath.Join(dir, "vaults", ws, "agents", "a1")
	mustMkdir(t, agentDir)

	c := newTestCoder(t, dir).
		WithDir(agentDir).
		WithExtraEnv(map[string]string{buildphase.EnvVar: buildphase.Generation})

	testFake.calls = 0
	testFake.script = func(call int, _ llm.Request) (*llm.Response, error) {
		if call == 0 {
			return &llm.Response{ToolCalls: []llm.ToolCall{
				toolCall("write_file", `{"path":"tools/broken.py","content":"pass\n"}`),
			}}, nil
		}
		// Always try to finish without ever producing output.
		return &llm.Response{Content: "[CHAT] giving up"}, nil
	}

	res, err := c.Generate(context.Background(), ws, "build it")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// 1 write + maxVerifyNudges nudged finish-attempts + 1 finally-allowed finish.
	if want := 1 + maxVerifyNudges + 1; testFake.calls != want {
		t.Errorf("provider calls = %d, want %d (bounded by maxVerifyNudges=%d)", testFake.calls, want, maxVerifyNudges)
	}
	if !strings.Contains(res.Text, "giving up") {
		t.Errorf("expected the build to finish after the nudge budget; got %q", res.Text)
	}
	// The script never produced output, so the engine must report it unverified — the agent
	// designer relies on this to keep the "couldn't confirm" keep-as-is escape.
	if res.ScriptVerified {
		t.Error("expected Result.ScriptVerified=false when the authored script never produced output")
	}
}

func TestAPIEngine_ProviderKeyStrippedFromSubprocessEnv(t *testing.T) {
	// stripKey removes the provider-key name so the LLM provider key never reaches
	// the agent's run_script subprocess (the agent's own secrets remain).
	stripped := stripKey(map[string]string{
		"PROVIDER_KEY": "secret-provider-key",
		"MY_AGENT_KEY": "agent-secret",
	}, "PROVIDER_KEY")
	if _, ok := stripped["PROVIDER_KEY"]; ok {
		t.Fatal("PROVIDER_KEY present in subprocess env, should be stripped")
	}
	if stripped["MY_AGENT_KEY"] != "agent-secret" {
		t.Fatal("agent's own secret was stripped; only the provider key should be removed")
	}

	// End-to-end: the engine's hostToolSet inherits the stripped env.
	dir := t.TempDir()
	ws := "ws6"
	c := newTestCoder(t, dir).
		WithExtraEnv(map[string]string{
			"PROVIDER_KEY": "secret-provider-key",
			"MY_AGENT_KEY": "agent-secret",
		})
	h := c.buildHostTools(ws)
	if _, ok := h.subprocessEnv["PROVIDER_KEY"]; ok {
		t.Fatal("buildHostTools left PROVIDER_KEY in subprocessEnv")
	}
	if h.subprocessEnv["MY_AGENT_KEY"] != "agent-secret" {
		t.Fatal("buildHostTools stripped the agent's own secret")
	}
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

// TestAPIEngine_FailingScriptSurfacesStdoutAndNudgesIdenticalRepeat covers the
// two fixes for the agent run-loop the user hit: (1) a failing script that
// prints its error to stdout (then sys.exit(1)) must have that stdout surfaced
// in the tool result so the model can self-correct — the old code reported only
// stderr (empty) and the model re-ran the script blindly; (2) when the model
// re-requests the exact same tool call that just failed, the engine nudges it to
// change approach instead of re-executing and burning the turn budget.
func TestAPIEngine_FailingScriptSurfacesStdoutAndNudgesIdenticalRepeat(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	dir := t.TempDir()
	ws := "ws-loop"
	vaultRoot := filepath.Join(dir, "vaults", ws)
	agentDir := filepath.Join(vaultRoot, "agents", "a1")
	mustMkdir(t, agentDir)
	// Prints its error to STDOUT (as JSON), then exits 1 — the pattern the
	// gmail-draft script and many agent helpers use.
	mustWrite(t, filepath.Join(agentDir, "tools", "fail.py"), []byte(
		"import json, sys\nprint(json.dumps({\"error\": \"boom\"}))\nsys.exit(1)\n"))

	c := newTestCoder(t, dir).WithDir(agentDir)
	testFake.calls = 0

	sameCall := toolCall("run_script", `{"path":"tools/fail.py"}`)
	var firstResult string
	testFake.script = func(call int, req llm.Request) (*llm.Response, error) {
		switch call {
		case 0:
			// First turn: request the failing script.
			return &llm.Response{ToolCalls: []llm.ToolCall{sameCall}}, nil
		case 1:
			// Capture the result of the first execution, then re-request the
			// IDENTICAL call (same name + same args) — the loop pattern.
			firstResult = lastToolResult(req)
			return &llm.Response{ToolCalls: []llm.ToolCall{sameCall}}, nil
		default:
			// Final turn: echo back the last tool result (the nudge).
			return &llm.Response{Content: "[CHAT] last=" + lastToolResult(req)}, nil
		}
	}

	res, err := c.Generate(context.Background(), ws, "go")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// Fix #1: the first failure surfaces the script's stdout, not just empty stderr.
	if !strings.Contains(firstResult, "boom") {
		t.Fatalf("first failure result = %q, want it to contain the script's stdout \"boom\"", firstResult)
	}
	if !strings.Contains(firstResult, "script failed") {
		t.Fatalf("first failure result = %q, want \"script failed\"", firstResult)
	}
	// Fix #2: the identical repeat was nudged, not re-executed.
	if !strings.Contains(res.Text, "you already tried") {
		t.Fatalf("result = %q, want the do-not-retry nudge for the identical repeat", res.Text)
	}
}

// TestAPIEngine_OscillatingFailuresAreNudged proves the loop guard catches a model
// alternating between two different failing approaches (A fails, B fails, A again), not
// just an exact back-to-back repeat. A single-slot "last failure" memory would miss this
// (B overwrites A's record before A's retry is checked); the bounded ring in
// hostToolSet.recentFails must not.
func TestAPIEngine_OscillatingFailuresAreNudged(t *testing.T) {
	dir := t.TempDir()
	ws := "ws-oscillate"
	vaultRoot := filepath.Join(dir, "vaults", ws)
	agentDir := filepath.Join(vaultRoot, "agents", "a1")
	mustMkdir(t, agentDir)

	c := newTestCoder(t, dir).WithDir(agentDir)
	testFake.calls = 0

	callA := toolCall("run_script", `{"path":"tools/missingA.py"}`)
	callB := toolCall("run_script", `{"path":"tools/missingB.py"}`)
	var thirdCallResult string
	testFake.script = func(call int, req llm.Request) (*llm.Response, error) {
		switch call {
		case 0:
			return &llm.Response{ToolCalls: []llm.ToolCall{callA}}, nil // real exec, fails
		case 1:
			return &llm.Response{ToolCalls: []llm.ToolCall{callB}}, nil // real exec, fails
		case 2:
			return &llm.Response{ToolCalls: []llm.ToolCall{callA}}, nil // oscillate back to A
		default:
			thirdCallResult = lastToolResult(req)
			return &llm.Response{Content: "[CHAT] done"}, nil
		}
	}

	if _, err := c.Generate(context.Background(), ws, "go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(thirdCallResult, "you already tried") {
		t.Fatalf("third call (oscillating repeat of A) result = %q, want the nudge, not a fresh execution", thirdCallResult)
	}
	if strings.Contains(thirdCallResult, "script not found") {
		t.Fatalf("third call result = %q, looks like it was actually re-executed instead of nudged", thirdCallResult)
	}
}

// TestAPIEngine_ConsecutiveFailuresEscalateNudge proves that after
// consecutiveFailWarnThreshold DIFFERENT (non-repeating) failures in a row, an extra
// "stop iterating / consider [BLOCKED]" nudge is appended — independent of the
// exact/oscillating-repeat guard, which wouldn't otherwise trip since these calls never
// repeat.
func TestAPIEngine_ConsecutiveFailuresEscalateNudge(t *testing.T) {
	dir := t.TempDir()
	ws := "ws-consecutive"
	vaultRoot := filepath.Join(dir, "vaults", ws)
	agentDir := filepath.Join(vaultRoot, "agents", "a1")
	mustMkdir(t, agentDir)

	c := newTestCoder(t, dir).WithDir(agentDir)
	testFake.calls = 0

	var thirdCallResult string
	testFake.script = func(call int, req llm.Request) (*llm.Response, error) {
		switch call {
		case 0, 1, 2:
			path := fmt.Sprintf(`{"path":"tools/missing%d.py"}`, call) // distinct args every time
			return &llm.Response{ToolCalls: []llm.ToolCall{toolCall("run_script", path)}}, nil
		default:
			thirdCallResult = lastToolResult(req)
			return &llm.Response{Content: "[CHAT] done"}, nil
		}
	}

	if _, err := c.Generate(context.Background(), ws, "go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(thirdCallResult, "Stop iterating blindly") {
		t.Fatalf("result after 3 consecutive distinct failures = %q, want the escalating nudge", thirdCallResult)
	}
	if !strings.Contains(thirdCallResult, "[BLOCKED]") {
		t.Fatalf("result = %q, want it to point the model at [BLOCKED]", thirdCallResult)
	}
}

// TestAPIEngine_TurnBudgetExhaustionGracefullyEmitsBlocked proves that exhausting
// maxAPITurns no longer fails opaquely: the loop gives the model one final, tools-off
// turn to explain itself, and that text (expected to be a [BLOCKED] block per the nudge)
// is returned as a normal successful Result instead of a bare ErrMaxTurns.
func TestAPIEngine_TurnBudgetExhaustionGracefullyEmitsBlocked(t *testing.T) {
	dir := t.TempDir()
	ws := "ws-gracebudget"
	c := newTestCoder(t, dir)
	mustMkdir(t, filepath.Join(dir, "vaults", ws))

	testFake.calls = 0
	testFake.script = func(call int, req llm.Request) (*llm.Response, error) {
		if call < maxAPITurns {
			// Never finishes on its own — burns the whole main-loop budget.
			return &llm.Response{ToolCalls: []llm.ToolCall{
				toolCall("list_dir", `{"path":"."}`),
			}}, nil
		}
		// The grace call: Tools must be stripped (forces a text-only reply).
		if len(req.Tools) != 0 {
			t.Errorf("grace call req.Tools = %v, want none offered", req.Tools)
		}
		return &llm.Response{Content: "[BLOCKED]\nWhat failed: could not finish in time.\nWhat you can do instead: simplify the task.\n[/BLOCKED]"}, nil
	}

	res, err := c.Generate(context.Background(), ws, "go")
	if err != nil {
		t.Fatalf("Generate: %v, want a graceful [BLOCKED] result instead of an error", err)
	}
	if !strings.Contains(res.Text, "[BLOCKED]") {
		t.Fatalf("result = %q, want the grace-turn [BLOCKED] explanation", res.Text)
	}
	if testFake.calls != maxAPITurns+1 {
		t.Fatalf("provider calls = %d, want exactly %d (main loop + one grace call)", testFake.calls, maxAPITurns+1)
	}
}

// TestAPIEngine_TurnBudgetExhaustionFallsBackToErrMaxTurns proves that if the grace call
// ITSELF produces nothing usable (empty content, mirroring a model that ignores the
// nudge), the engine still falls back to the original ErrMaxTurns rather than returning
// an empty/useless success.
func TestAPIEngine_TurnBudgetExhaustionFallsBackToErrMaxTurns(t *testing.T) {
	dir := t.TempDir()
	ws := "ws-gracebudget-empty"
	c := newTestCoder(t, dir)
	mustMkdir(t, filepath.Join(dir, "vaults", ws))

	testFake.calls = 0
	testFake.script = func(call int, req llm.Request) (*llm.Response, error) {
		if call < maxAPITurns {
			return &llm.Response{ToolCalls: []llm.ToolCall{
				toolCall("list_dir", `{"path":"."}`),
			}}, nil
		}
		return &llm.Response{Content: ""}, nil // ignores the nudge entirely
	}

	_, err := c.Generate(context.Background(), ws, "go")
	if !errors.Is(err, ErrMaxTurns) {
		t.Fatalf("err = %v, want ErrMaxTurns when the grace call also produces nothing", err)
	}
	if testFake.calls != maxAPITurns+1 {
		t.Fatalf("provider calls = %d, want exactly %d (main loop + one grace call)", testFake.calls, maxAPITurns+1)
	}
}

// TestAPIEngine_GraceNudge_NonBuildUsesPlainWrapUp guards N2: an agent RUN or one-off chat
// that exhausts its turn budget must get the plain-language wrap-up nudge, NOT the build-only
// [BLOCKED] instruction (which those paths don't parse and would surface as stray text).
func TestAPIEngine_GraceNudge_NonBuildUsesPlainWrapUp(t *testing.T) {
	dir := t.TempDir()
	ws := "ws-grace-run"
	c := newTestCoder(t, dir)
	mustMkdir(t, filepath.Join(dir, "vaults", ws))

	var graceNudge string
	testFake.calls = 0
	testFake.script = func(call int, req llm.Request) (*llm.Response, error) {
		if call < maxAPITurns {
			return &llm.Response{ToolCalls: []llm.ToolCall{toolCall("list_dir", `{"path":"."}`)}}, nil
		}
		graceNudge = lastUserMessage(req) // the grace call
		return &llm.Response{Content: "wrapped up in plain language"}, nil
	}

	// Run (not a build): Generate without ROOKERY_BUILD_PHASE, so tools.verifyBuild is false.
	// (Agent runs and builds share Generate; only the build sets the build-phase env.)
	if _, err := c.Generate(context.Background(), ws, "go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if graceNudge != graceTurnWrapUpNudge {
		t.Fatalf("non-build grace nudge = %q, want the plain wrap-up nudge", graceNudge)
	}
	if strings.Contains(graceNudge, "[BLOCKED]") {
		t.Fatalf("non-build grace nudge must not mention [BLOCKED]: %q", graceNudge)
	}
	if testFake.calls != maxAPITurns+1 {
		t.Fatalf("provider calls = %d, want %d", testFake.calls, maxAPITurns+1)
	}
}

// TestAPIEngine_GraceNudge_BuildUsesBlockedAndLargerBudget guards N2 + H4: a BUILD keeps the
// [BLOCKED] grace nudge (the designer parses it) and gets the larger turn budget.
func TestAPIEngine_GraceNudge_BuildUsesBlockedAndLargerBudget(t *testing.T) {
	dir := t.TempDir()
	ws := "ws-grace-build"
	c := newTestCoder(t, dir).WithExtraEnv(map[string]string{
		buildphase.EnvVar: buildphase.Generation,
	})
	mustMkdir(t, filepath.Join(dir, "vaults", ws))

	var graceNudge string
	testFake.calls = 0
	testFake.script = func(call int, req llm.Request) (*llm.Response, error) {
		if call < maxBuildAPITurns {
			return &llm.Response{ToolCalls: []llm.ToolCall{toolCall("list_dir", `{"path":"."}`)}}, nil
		}
		graceNudge = lastUserMessage(req)
		return &llm.Response{Content: "[BLOCKED]\nWhat couldn't be done: ran out of time.\n[/BLOCKED]"}, nil
	}

	if _, err := c.Generate(context.Background(), ws, "go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if graceNudge != graceTurnBudgetNudge {
		t.Fatalf("build grace nudge = %q, want the [BLOCKED] nudge", graceNudge)
	}
	if testFake.calls != maxBuildAPITurns+1 {
		t.Fatalf("build provider calls = %d, want %d (larger build budget + grace)", testFake.calls, maxBuildAPITurns+1)
	}
}

// TestHostTools_StderrOnlyDoesNotCountAsProducedOutput guards A7: a script that printed only
// to stderr (the "(no stdout; stderr)" sentinel) must NOT satisfy the build verification gate.
func TestHostTools_StderrOnlyDoesNotCountAsProducedOutput(t *testing.T) {
	h := &hostToolSet{verifyBuild: true}
	h.trackScriptProgress(toolCall("write_file", `{"path":"tools/fetch.py"}`), "ok: wrote", false)
	if !h.needsScriptVerification() {
		t.Fatal("an authored-but-never-run script should still need verification")
	}
	// Ran, but only stderr came back — must not clear the gate.
	h.trackScriptProgress(toolCall("run_script", `{"path":"tools/fetch.py"}`), noStdoutSentinel+"\nTraceback (most recent call last)", false)
	if !h.needsScriptVerification() {
		t.Fatal("stderr-only output must NOT count as produced output")
	}
	// Real stdout clears it.
	h.trackScriptProgress(toolCall("run_script", `{"path":"tools/fetch.py"}`), `{"emails":[{"id":"1"}]}`, false)
	if h.needsScriptVerification() {
		t.Fatal("real stdout should clear the verification gate")
	}
}

// TestVerifyFinishNudge_RequiresAgentMDFirst guards the no-AGENT.md loop fix: when a build
// authored a helper script but AGENT.md is not on disk, the finish nudge must redirect the
// model to WRITE AGENT.md (not keep fixing the script). Only once AGENT.md exists does the
// script-verification gate (gate 2) apply. Without this, a weak model burns its whole turn
// budget trying to verify a helper that's intentionally blocked at build time and never
// writes AGENT.md — the loop the user hit.
func TestVerifyFinishNudge_RequiresAgentMDFirst(t *testing.T) {
	dir := t.TempDir()

	// No AGENT.md, an authored unverified script → gate 1 fires, demands AGENT.md.
	h := &hostToolSet{verifyBuild: true, workDir: dir}
	h.trackScriptProgress(toolCall("write_file", `{"path":"tools/fetch.py"}`), "ok", false)
	nudge := h.verifyFinishNudge()
	if nudge == "" {
		t.Fatal("a build with an unverified script and no AGENT.md must be nudged, not allowed to finish")
	}
	if !strings.Contains(strings.ToLower(nudge), "agent.md") {
		t.Errorf("the nudge must redirect to writing AGENT.md first; got %q", nudge)
	}
	if strings.Contains(strings.ToLower(nudge), "run it (run_script)") {
		t.Errorf("gate 1 must NOT tell the model to keep running the blocked script; got %q", nudge)
	}

	// Write AGENT.md → gate 1 passes, gate 2 (script verification) now applies.
	if err := os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte("# agent\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	h2 := &hostToolSet{verifyBuild: true, workDir: dir}
	h2.trackScriptProgress(toolCall("write_file", `{"path":"tools/fetch.py"}`), "ok", false)
	nudge2 := h2.verifyFinishNudge()
	if nudge2 == "" {
		t.Fatal("with AGENT.md present + an unverified script, gate 2 must still nudge")
	}
	if !strings.Contains(nudge2, "run_script") {
		t.Errorf("gate 2 should steer toward running the script; got %q", nudge2)
	}

	// AGENT.md present + script produced real output → no nudge (finish allowed).
	h3 := &hostToolSet{verifyBuild: true, workDir: dir}
	h3.trackScriptProgress(toolCall("write_file", `{"path":"tools/fetch.py"}`), "ok", false)
	h3.trackScriptProgress(toolCall("run_script", `{"path":"tools/fetch.py"}`), `{"data":[1,2]}`, false)
	if h3.verifyFinishNudge() != "" {
		t.Fatal("a verified build (AGENT.md + real script output) must be allowed to finish with no nudge")
	}
}

// TestIsAgentScriptPath_ExcludesLibrariesAndTests guards A4: only entry-point scripts count
// toward the build-verification gate. The supporting files the testing_rules prompt tells the
// model to create (tools/lib/*.py, tools/tests/test_*.py, __init__.py) must NOT be treated as
// scripts-that-must-produce-output, or a correct multi-file agent false-[BLOCKED]s.
func TestIsAgentScriptPath_ExcludesLibrariesAndTests(t *testing.T) {
	entry := []string{"tools/fetch_emails.py", "tools/send_digest.py", "agents/a1/tools/run.py"}
	for _, p := range entry {
		if !isAgentScriptPath(p) {
			t.Errorf("isAgentScriptPath(%q) = false, want true (entry script must be verified)", p)
		}
	}
	nonEntry := []string{
		"tools/lib/pricing.py",        // imported library module
		"tools/tests/test_pricing.py", // test file under tests/
		"tools/test_fetch.py",         // test_-prefixed test file
		"tools/pricing_test.py",       // _test.py-suffixed test file
		"tools/__init__.py",           // package marker
		"tools/conftest.py",           // pytest config
		"notes/plan.md",               // not a .py under tools/
	}
	for _, p := range nonEntry {
		if isAgentScriptPath(p) {
			t.Errorf("isAgentScriptPath(%q) = true, want false (not an entry script)", p)
		}
	}
}

func TestAPIEngine_RunScriptPassesArgs(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	dir := t.TempDir()
	ws := "ws-args"
	vaultRoot := filepath.Join(dir, "vaults", ws)
	agentDir := filepath.Join(vaultRoot, "agents", "a1")
	mustMkdir(t, agentDir)
	// Mirrors the Composio helper pattern: read a payload file from sys.argv[1].
	mustWrite(t, filepath.Join(agentDir, "tools", "payload.json"), []byte(`{"recipient":"x@y.z","subject":"hi","body":"b"}`))
	mustWrite(t, filepath.Join(agentDir, "tools", "echo_args.py"), []byte(
		"import json, sys\nwith open(sys.argv[1]) as f: p=json.load(f)\nprint(json.dumps(p))\n"))

	c := newTestCoder(t, dir).WithDir(agentDir)
	testFake.calls = 0
	testFake.script = func(call int, req llm.Request) (*llm.Response, error) {
		if call == 0 {
			return &llm.Response{ToolCalls: []llm.ToolCall{
				toolCall("run_script", `{"path":"tools/echo_args.py","args":["tools/payload.json"]}`),
			}}, nil
		}
		return &llm.Response{Content: "[CHAT] " + lastToolResult(req)}, nil
	}

	res, err := c.Generate(context.Background(), ws, "echo the payload")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(res.Text, "x@y.z") {
		t.Fatalf("result = %q, want the script to have received tools/payload.json as argv[1]", res.Text)
	}
}

func TestAPIEngine_RunScriptPipesStdin(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	dir := t.TempDir()
	ws := "ws-stdin"
	vaultRoot := filepath.Join(dir, "vaults", ws)
	agentDir := filepath.Join(vaultRoot, "agents", "a1")
	mustMkdir(t, agentDir)
	mustWrite(t, filepath.Join(agentDir, "tools", "cat_stdin.py"), []byte("import sys\nprint('S:' + sys.stdin.read())\n"))

	c := newTestCoder(t, dir).WithDir(agentDir)
	testFake.calls = 0
	testFake.script = func(call int, req llm.Request) (*llm.Response, error) {
		if call == 0 {
			return &llm.Response{ToolCalls: []llm.ToolCall{
				toolCall("run_script", `{"path":"tools/cat_stdin.py","stdin":"hello-stdin"}`),
			}}, nil
		}
		return &llm.Response{Content: "[CHAT] " + lastToolResult(req)}, nil
	}

	res, err := c.Generate(context.Background(), ws, "pipe it")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(res.Text, "S:hello-stdin") {
		t.Fatalf("result = %q, want the script to have received stdin 'hello-stdin'", res.Text)
	}
}

func TestAPIEngine_ChatThreadsHistoryAsMessageTurns(t *testing.T) {
	dir := t.TempDir()
	ws := "ws-chat"
	c := newTestCoder(t, dir)
	mustMkdir(t, filepath.Join(dir, "vaults", ws))

	history := []db.ChatMessage{
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
	}
	testFake.calls = 0
	var gotReq llm.Request
	testFake.script = func(_ int, req llm.Request) (*llm.Response, error) {
		gotReq = req
		return &llm.Response{Content: "reply"}, nil
	}

	res, err := c.Chat(context.Background(), ws, history, "SYS-PROMPT", "current")
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if res.Text != "reply" {
		t.Fatalf("result = %q, want reply", res.Text)
	}
	// The system prompt must be the design system prompt, NOT a flattened blob
	// containing the history (the old bug that made the model re-ask Q1 every turn).
	if gotReq.System != "SYS-PROMPT" {
		t.Fatalf("System = %q, want exactly \"SYS-PROMPT\"", gotReq.System)
	}
	// History must arrive as real alternating user/assistant turns + the current
	// user message — not flattened into one user blob.
	wantMsgs := []struct{ role, content string }{
		{"user", "u1"},
		{"assistant", "a1"},
		{"user", "current"},
	}
	if len(gotReq.Messages) != len(wantMsgs) {
		t.Fatalf("messages = %d, want %d: %+v", len(gotReq.Messages), len(wantMsgs), gotReq.Messages)
	}
	for i, w := range wantMsgs {
		if gotReq.Messages[i].Role != w.role || gotReq.Messages[i].Content != w.content {
			t.Fatalf("msg[%d] = {%s, %q}, want {%s, %q}", i, gotReq.Messages[i].Role, gotReq.Messages[i].Content, w.role, w.content)
		}
	}
}

func TestAPIEngine_ChatCoalescesConsecutiveSameRoleForAnthropic(t *testing.T) {
	dir := t.TempDir()
	ws := "ws-coal"
	c := newTestCoder(t, dir)
	mustMkdir(t, filepath.Join(dir, "vaults", ws))

	// Two user turns back-to-back (no assistant reply between) would make a
	// strict-alternation provider reject the request; chatAPI must coalesce them.
	history := []db.ChatMessage{
		{Role: "user", Content: "u1"},
		{Role: "user", Content: "u2"},
	}
	testFake.calls = 0
	var gotReq llm.Request
	testFake.script = func(_ int, req llm.Request) (*llm.Response, error) {
		gotReq = req
		return &llm.Response{Content: "ok"}, nil
	}

	// WithNoTools → text-only chatAPI path.
	if _, err := c.WithNoTools().Chat(context.Background(), ws, history, "S", "current"); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if len(gotReq.Messages) != 1 {
		t.Fatalf("messages = %d, want 1 (coalesced)", len(gotReq.Messages))
	}
	if gotReq.Messages[0].Role != "user" {
		t.Fatalf("role = %q, want user", gotReq.Messages[0].Role)
	}
	want := "u1\n\nu2\n\ncurrent"
	if gotReq.Messages[0].Content != want {
		t.Fatalf("content = %q, want %q", gotReq.Messages[0].Content, want)
	}
}

// TestAPIEngine_ChatWithToolsRunsToolLoopAndWritesNote covers the one-off chat
// path for API coders: Chat WITHOUT WithNoTools must run the tool-calling loop
// (chatToolsAPI) so the chat can read/write the user's knowledge base on demand.
// The model is offered the host file tools (read_file/write_file/edit_file/
// list_dir) — NOT run_script (chat's workDir is the vault root, the no-shell
// boundary) — and a write_file call must actually create the note in the vault.
// History is threaded as real alternating turns with the chat system prompt as
// the system message (not flattened), same as the text-only chat path.
func TestAPIEngine_ChatWithToolsRunsToolLoopAndWritesNote(t *testing.T) {
	dir := t.TempDir()
	ws := "ws-chat-tools"
	vaultRoot := filepath.Join(dir, "vaults", ws)
	mustMkdir(t, vaultRoot)

	// One-off chat sets the CWD to the vault root (no WithNoTools → tool loop).
	c := newTestCoder(t, dir).WithDir(vaultRoot)
	history := []db.ChatMessage{
		{Role: "user", Content: "what notes do I have?"},
		{Role: "assistant", Content: "let me check"},
	}
	testFake.calls = 0
	var firstReq llm.Request
	testFake.script = func(call int, req llm.Request) (*llm.Response, error) {
		if call == 0 {
			firstReq = req
			return &llm.Response{ToolCalls: []llm.ToolCall{
				toolCall("write_file", `{"path":"notes/chat-note.md","content":"from chat"}`),
			}}, nil
		}
		return &llm.Response{Content: "[CHAT] I saved a note."}, nil
	}

	res, err := c.Chat(context.Background(), ws, history, "SYS-CHAT", "save a note")
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if !strings.Contains(res.Text, "I saved a note") {
		t.Fatalf("result = %q", res.Text)
	}
	// The note must have been written to the vault root (chat CWD = vault root).
	got, err := os.ReadFile(filepath.Join(vaultRoot, "notes", "chat-note.md"))
	if err != nil {
		t.Fatalf("note not written via tool call: %v", err)
	}
	if string(got) != "from chat" {
		t.Fatalf("note = %q, want %q", got, "from chat")
	}
	// The system prompt is the chat system prompt (not flattened history).
	if firstReq.System != "SYS-CHAT" {
		t.Fatalf("System = %q, want SYS-CHAT", firstReq.System)
	}
	// History threaded as real turns, not flattened.
	if len(firstReq.Messages) != 3 || firstReq.Messages[0].Role != "user" ||
		firstReq.Messages[1].Role != "assistant" || firstReq.Messages[2].Role != "user" {
		t.Fatalf("messages not threaded as alternating turns: %+v", firstReq.Messages)
	}
	// File tools offered, run_script NOT offered (chat no-shell boundary).
	names := map[string]bool{}
	for _, t2 := range firstReq.Tools {
		names[t2.Name] = true
	}
	for _, must := range []string{"read_file", "write_file", "edit_file", "list_dir"} {
		if !names[must] {
			t.Fatalf("tool %q not offered for chat", must)
		}
	}
	if names["run_script"] {
		t.Fatalf("run_script must NOT be offered for chat (no-shell boundary)")
	}
}

// TestAPIEngine_ChatWithNoToolsOffersNoTools confirms the design-conversation
// path (WithNoTools) offers no tools and does a single completion — the text-only
// Q&A the agent/skill designer relies on.
func TestAPIEngine_ChatWithNoToolsOffersNoTools(t *testing.T) {
	dir := t.TempDir()
	ws := "ws-chat-notools"
	c := newTestCoder(t, dir)
	mustMkdir(t, filepath.Join(dir, "vaults", ws))

	testFake.calls = 0
	var gotReq llm.Request
	testFake.script = func(_ int, req llm.Request) (*llm.Response, error) {
		gotReq = req
		return &llm.Response{Content: "reply"}, nil
	}
	if _, err := c.WithNoTools().Chat(context.Background(), ws, nil, "S", "hi"); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if len(gotReq.Tools) != 0 {
		t.Fatalf("WithNoTools chat offered %d tools, want 0", len(gotReq.Tools))
	}
	if gotReq.System != "S" {
		t.Fatalf("System = %q, want S", gotReq.System)
	}
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, data, 0o640); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestAPIEngine_RelativePathWritesToWorkDirNotVaultRoot is the generation regression
// guard: when the API coder runs with a working directory that is the agent's own dir
// (as generation and runs do), a RELATIVE write_file("AGENT.md") must land in the
// working directory — matching the CLI coder's CWD semantic — NOT in the vault root.
// Before the fix, write_file resolved relative paths against the vault root, so AGENT.md
// landed at <vault>/AGENT.md while the agent designer looked for it at <workDir>/AGENT.md
// and reported "The coder didn't create AGENT.md" even though the coder had written it.
func TestAPIEngine_RelativePathWritesToWorkDirNotVaultRoot(t *testing.T) {
	dir := t.TempDir()
	ws := "ws-gen"
	vaultRoot := filepath.Join(dir, "vaults", ws)
	agentDir := filepath.Join(vaultRoot, "agents", "agent1")
	mustMkdir(t, agentDir)

	c := newTestCoder(t, dir).WithDir(agentDir)
	testFake.calls = 0
	testFake.script = func(call int, _ llm.Request) (*llm.Response, error) {
		if call == 0 {
			return &llm.Response{ToolCalls: []llm.ToolCall{
				toolCall("write_file", `{"path":"AGENT.md","content":"# Suggested schedule: */10 * * * *\nDo the thing."}`),
			}}, nil
		}
		return &llm.Response{Content: "[CHAT] built it"}, nil
	}

	if _, err := c.Generate(context.Background(), ws, "build the agent"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// AGENT.md must be in the working directory (the agent dir), where the designer reads it.
	got, err := os.ReadFile(filepath.Join(agentDir, "AGENT.md"))
	if err != nil {
		t.Fatalf("AGENT.md not written to workDir %s: %v", agentDir, err)
	}
	if !strings.Contains(string(got), "Suggested schedule") {
		t.Fatalf("AGENT.md content = %q", got)
	}
	// And it must NOT have landed in the vault root (the old, broken behaviour).
	if _, err := os.Stat(filepath.Join(vaultRoot, "AGENT.md")); err == nil {
		t.Fatalf("AGENT.md was wrongly written to the vault root — relative paths must resolve against the workDir")
	}
}
