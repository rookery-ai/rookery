package coder

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/llm"
)

func setStateCall(patchJSON string) llm.ToolCall {
	return llm.ToolCall{Name: "set_state", Args: []byte(`{"patch":` + patchJSON + `}`)}
}

func getStateCall() llm.ToolCall {
	return llm.ToolCall{Name: "get_state", Args: []byte(`{}`)}
}

// TestSetStateMergesAndGetStateReturnsIt is the brief's Step 1 test, adapted to the
// package's real conventions: hostToolSet is built as a plain struct literal (no
// hostToolConfig wrapper) and dispatched through execute(ctx, llm.ToolCall), matching
// every other tool test in this package (see connectortools_test.go, hosttools_bash_test.go).
func TestSetStateMergesAndGetStateReturnsIt(t *testing.T) {
	dir := t.TempDir()
	h := &hostToolSet{workDir: dir, includeExecTools: true, agentName: "a"}

	if out := h.execute(context.Background(), setStateCall(`{"seen":1}`)); strings.HasPrefix(out, "error:") {
		t.Fatalf("set_state failed: %s", out)
	}
	out := h.execute(context.Background(), getStateCall())
	if !strings.Contains(out, "\"seen\"") {
		t.Fatalf("state not returned: %s", out)
	}
}

// TestSetStatePatchSemantics pins the patch-not-replace contract the tool description
// promises the model: an untouched key survives a later set_state call, and an explicit
// null deletes a key. Both directions are worth pinning separately — a merge that
// silently drops unmentioned keys (a wholesale overwrite) and a merge that never deletes
// anything (ignoring null) are both plausible, opposite regressions of the same helper.
func TestSetStatePatchSemantics(t *testing.T) {
	dir := t.TempDir()
	h := &hostToolSet{workDir: dir, includeExecTools: true, agentName: "a"}

	h.execute(context.Background(), setStateCall(`{"seen":1,"cursor":"abc"}`))
	h.execute(context.Background(), setStateCall(`{"cursor":"def"}`))

	out := h.execute(context.Background(), getStateCall())
	if !strings.Contains(out, "\"seen\": 1") {
		t.Fatalf("key absent from the second patch was dropped instead of left untouched: %s", out)
	}
	if !strings.Contains(out, "\"cursor\": \"def\"") {
		t.Fatalf("key present in the second patch was not updated: %s", out)
	}

	h.execute(context.Background(), setStateCall(`{"seen":null}`))
	out = h.execute(context.Background(), getStateCall())
	if strings.Contains(out, "\"seen\"") {
		t.Fatalf("null patch value did not delete the key: %s", out)
	}
}

// Chat must not be able to reach an agent's memory — get_state/set_state ride the same
// includeExecTools gate as run_script/bash, which is off for chat (workDir == vault
// root). Checked at both the offer site (tools()) and the dispatch site (execute()),
// mirroring how run_script/bash defend in depth against a stale/forged tool call.
func TestStateToolsAreAbsentWithoutExecTools(t *testing.T) {
	h := &hostToolSet{includeExecTools: false}
	for _, d := range h.tools() {
		if d.Name == "get_state" || d.Name == "set_state" {
			t.Fatalf("%s offered to a non-agent surface", d.Name)
		}
	}
}

func TestStateToolsRefuseExecutionWithoutExecTools(t *testing.T) {
	h := &hostToolSet{includeExecTools: false}
	if out := h.execute(context.Background(), getStateCall()); !strings.Contains(out, "not available") {
		t.Fatalf("get_state should refuse when exec tools are off, got: %s", out)
	}
	if out := h.execute(context.Background(), setStateCall(`{"x":1}`)); !strings.Contains(out, "not available") {
		t.Fatalf("set_state should refuse when exec tools are off, got: %s", out)
	}
}

// get_state must read the SAME file agentstate.Apply (and therefore the [STATE]
// marker and the CLI bridge) writes — agentstate.StateFilePath(workDir) — or a model
// using the tool and a run emitting [STATE] would silently disagree about where memory
// lives.
func TestGetStateReadsAgentStateFilePath(t *testing.T) {
	dir := t.TempDir()
	h := &hostToolSet{workDir: dir, includeExecTools: true, agentName: "a"}
	h.execute(context.Background(), setStateCall(`{"k":"v"}`))

	if _, err := os.Stat(filepath.Join(dir, "state.md")); err != nil {
		t.Fatalf("expected state.md at the agent dir root: %v", err)
	}
}
