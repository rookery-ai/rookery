package coder

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/llm"
)

// TestDesignAllowedToolsGrantsNoWriteAndNoConvert pins the CLI grant for a
// design conversation. The blanket "kb:*" grant that ChatAllowedTools uses
// would reach `kb convert`, which WRITES a note into the vault — so the design
// grant must name subcommands individually.
func TestDesignAllowedToolsGrantsNoWriteAndNoConvert(t *testing.T) {
	got := DesignAllowedTools("/usr/bin/rookery")

	for _, banned := range []string{"Write", "Edit", "kb convert", "kb:*"} {
		if strings.Contains(got, banned) {
			t.Errorf("DesignAllowedTools granted %q; got %q", banned, got)
		}
	}
	for _, want := range []string{
		"Read", "Glob", "Grep", "WebFetch", "WebSearch",
		"Bash(/usr/bin/rookery kb search:*)",
		"Bash(/usr/bin/rookery kb map:*)",
		"Bash(/usr/bin/rookery kb table:*)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("DesignAllowedTools missing %q; got %q", want, got)
		}
	}
}

// TestDesignAllowedToolsOmitsKBGrantsWithoutABridge covers the designers, which
// wire no KB bridge: with no binary there is nothing to authorise, and an empty
// Bash grant must not be emitted.
func TestDesignAllowedToolsOmitsKBGrantsWithoutABridge(t *testing.T) {
	got := DesignAllowedTools("")
	if strings.Contains(got, "Bash(") {
		t.Errorf("DesignAllowedTools(\"\") emitted a Bash grant: %q", got)
	}
	if got != "Read,Glob,Grep,WebFetch,WebSearch" {
		t.Errorf("DesignAllowedTools(\"\") = %q, want the bare read-only set", got)
	}
}

// TestReadOnlyProfileNeverSendsAnEmptyAllowedTools guards the documented
// indefinite hang: claudeBackend.buildArgs emits NO --allowedTools flag when
// noTools is false and allowedTools is empty, alongside --setting-sources "",
// which makes the subprocess block forever.
func TestReadOnlyProfileNeverSendsAnEmptyAllowedTools(t *testing.T) {
	c := (&Coder{}).WithReadOnlyTools()
	if c.noTools {
		t.Fatal("WithReadOnlyTools must not set noTools")
	}
	if c.effectiveAllowedTools() == "" {
		t.Fatal("read-only profile resolved to an empty allowedTools (subprocess would hang)")
	}
}

// TestEffectiveAllowedToolsLeavesEveryOtherCallerAlone is the regression guard
// for the CLI arg path: the helper must be a no-op unless the read-only profile
// is set, whatever the caller configured.
func TestEffectiveAllowedToolsLeavesEveryOtherCallerAlone(t *testing.T) {
	if got := (&Coder{}).effectiveAllowedTools(); got != "" {
		t.Errorf("default coder resolved allowedTools to %q, want empty", got)
	}
	runSet := "Bash,WebFetch,Read,Write,Edit"
	if got := (&Coder{allowedTools: runSet}).effectiveAllowedTools(); got != runSet {
		t.Errorf("run profile allowedTools = %q, want %q", got, runSet)
	}
	// An explicit grant still wins under the read-only profile, so a caller that
	// wires a KB bridge can widen it to the scoped kb subcommands.
	explicit := DesignAllowedTools("/usr/bin/rookery")
	if got := (&Coder{readOnlyTools: true, allowedTools: explicit}).effectiveAllowedTools(); got != explicit {
		t.Errorf("explicit read-only grant = %q, want %q", got, explicit)
	}
}

// TestWithReadOnlyToolsDoesNotMutateTheReceiver mirrors every other With* modifier.
func TestWithReadOnlyToolsDoesNotMutateTheReceiver(t *testing.T) {
	base := &Coder{}
	_ = base.WithReadOnlyTools()
	if base.readOnlyTools {
		t.Fatal("WithReadOnlyTools mutated its receiver")
	}
}

// readOnlySubset is the exact tool set a design conversation may be offered.
var readOnlySubset = []string{
	"read_file", "list_dir", "search_files", "glob",
	"kb_file_map", "kb_table_query", "web_fetch", "web_search",
}

// mutatingTools are withheld from the read-only profile.
var mutatingTools = []string{"write_file", "edit_file", "save_to_kb"}

func toolNameSet(tools []llm.Tool) map[string]bool {
	out := map[string]bool{}
	for _, t := range tools {
		out[t.Name] = true
	}
	return out
}

// TestReadOnlyChatOffersExactlyTheSubset is the contract: the eight read-only
// tools, and nothing else.
func TestReadOnlyChatOffersExactlyTheSubset(t *testing.T) {
	dir := t.TempDir()
	ws := "ws-readonly-subset"
	c := newTestCoder(t, dir)
	mustMkdir(t, filepath.Join(dir, "vaults", ws))

	testFake.calls = 0
	var gotReq llm.Request
	testFake.script = func(_ int, req llm.Request) (*llm.Response, error) {
		gotReq = req
		return &llm.Response{Content: "reply"}, nil
	}
	if _, err := c.WithReadOnlyTools().Chat(context.Background(), ws, nil, "S", "hi"); err != nil {
		t.Fatalf("Chat: %v", err)
	}

	got := toolNameSet(gotReq.Tools)
	if len(got) != len(readOnlySubset) {
		t.Fatalf("read-only chat offered %d tools %v, want %d", len(got), gotReq.Tools, len(readOnlySubset))
	}
	for _, want := range readOnlySubset {
		if !got[want] {
			t.Errorf("read-only chat did not offer %q", want)
		}
	}
	for _, banned := range append(append([]string{}, mutatingTools...),
		"run_script", "bash", "get_state", "set_state") {
		if got[banned] {
			t.Errorf("read-only chat offered %q", banned)
		}
	}
}

// TestReadOnlyDispatchRejectsMutatingTools is the second half of the guard.
// Declaring fewer tools does not stop a model emitting a call for a name it was
// never offered — the dispatch switch executes BY NAME, so it must refuse too.
func TestReadOnlyDispatchRejectsMutatingTools(t *testing.T) {
	dir := t.TempDir()
	ws := "ws-readonly-dispatch"
	c := newTestCoder(t, dir).WithReadOnlyTools()
	mustMkdir(t, filepath.Join(dir, "vaults", ws))

	tools := c.buildHostTools(ws)
	if !tools.readOnly {
		t.Fatal("buildHostTools did not carry readOnly")
	}

	cases := []llm.ToolCall{
		{Name: "write_file", Args: []byte(`{"path":"notes/x.md","content":"pwned"}`)},
		{Name: "edit_file", Args: []byte(`{"path":"notes/x.md","old_string":"a","new_string":"b"}`)},
		{Name: "save_to_kb", Args: []byte(`{"source":"https://example.com","dest_dir":"notes"}`)},
	}
	for _, call := range cases {
		out := tools.execute(context.Background(), call)
		if !strings.HasPrefix(out, "error: ") {
			t.Errorf("%s dispatched under the read-only profile: %q", call.Name, out)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "vaults", ws, "notes", "x.md")); !os.IsNotExist(err) {
		t.Error("a rejected write_file still created the file")
	}
}

// TestDefaultProfileStillOffersEverything is the BUILD/RUN REGRESSION GUARD.
// If this fails, the change has taken capability away from agent builds or runs.
func TestDefaultProfileStillOffersEverything(t *testing.T) {
	dir := t.TempDir()
	ws := "ws-default-profile"
	c := newTestCoder(t, dir)
	mustMkdir(t, filepath.Join(dir, "vaults", ws))

	tools := c.buildHostTools(ws)
	if tools.readOnly {
		t.Fatal("default profile is read-only")
	}
	got := toolNameSet(tools.tools())
	for _, want := range append(append([]string{}, readOnlySubset...), mutatingTools...) {
		if !got[want] {
			t.Errorf("default profile no longer offers %q", want)
		}
	}
}

// TestReadOnlyForcesExecToolsOff pins the interlock rather than the path
// comparison. Exec tools are off today only because the designer's workDir
// equals the vault root; this asserts that a read-only coder with a DIFFERENT
// workDir still gets no shell.
func TestReadOnlyForcesExecToolsOff(t *testing.T) {
	dir := t.TempDir()
	ws := "ws-readonly-exec"
	mustMkdir(t, filepath.Join(dir, "vaults", ws, "agents", "a1"))
	c := newTestCoder(t, dir).
		WithReadOnlyTools().
		WithDir(filepath.Join(dir, "vaults", ws, "agents", "a1"))

	tools := c.buildHostTools(ws)
	if tools.includeExecTools {
		t.Fatal("read-only profile enabled exec tools")
	}
	got := toolNameSet(tools.tools())
	for _, banned := range []string{"run_script", "bash", "get_state", "set_state"} {
		if got[banned] {
			t.Errorf("read-only profile offered exec tool %q", banned)
		}
	}
}
