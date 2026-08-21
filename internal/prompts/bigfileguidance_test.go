package prompts

import (
	"strings"
	"testing"
)

// The big-file tools have to be described in the RUNTIME prompt, not only the
// build one. They were not, and the way they went missing is worth pinning.
//
// The guidance first went into agentPhilosophyBlock's `backendType ==
// BackendToolCalling` branch. BuildCoderPrompt calls that block with an empty
// backend type ON PURPOSE — the comment there explains that the tool-calling
// philosophy flip is a build concern which runtimeExecutionBlock contradicts at
// run time. So the guidance was suppressed by a gate that exists for an entirely
// unrelated reason, and every agent run was blind to the tools.
//
// The symptom was not an error. An agent asked to watch a 155 KB transaction
// table read it the old way, burned 235,692 tokens across one run, produced no
// [CHAT] output, and exited 0.
func TestRuntimePromptDescribesTheBigFileTools(t *testing.T) {
	out := BuildCoderPrompt(CoderPromptParams{
		AgentMD:     "# Test agent\n\nWatch the transactions file.\n",
		VaultRoot:   "/vaults/ws1",
		BackendType: BackendToolCalling,
	})
	for _, want := range []string{"kb_file_map", "kb_table_query"} {
		if !strings.Contains(out, want) {
			t.Errorf("the runtime prompt never mentions %s — an agent run cannot use a tool "+
				"it has not been told exists", want)
		}
	}
	// The instruction that actually changes behaviour: map before reading.
	if !strings.Contains(out, "BEFORE you read") {
		t.Errorf("the runtime prompt does not tell the agent to map a file before reading it")
	}
}

// The build prompt needs it too — a build runs the agent for real, so an agent
// authored without knowing the tools exist gets tested the slow way as well.
func TestImplementationPromptDescribesTheBigFileTools(t *testing.T) {
	out := BuildImplementationPrompt("test", nil, ImplementationParams{
		BackendType: BackendToolCalling,
	})
	for _, want := range []string{"kb_file_map", "kb_table_query"} {
		if !strings.Contains(out, want) {
			t.Errorf("the implementation prompt never mentions %s", want)
		}
	}
}

// A CLI coder has its own file tools and reaches ours over the bridge, so the
// tool-calling tool list must NOT be handed to it — it would name functions that
// backend cannot call.
func TestFullCoderPromptDoesNotClaimToolCallingFunctions(t *testing.T) {
	out := BuildCoderPrompt(CoderPromptParams{
		AgentMD:     "# Test agent\n",
		VaultRoot:   "/vaults/ws1",
		BackendType: BackendFullCoder,
	})
	if strings.Contains(out, "kb_file_map(path): describe") {
		t.Errorf("a full-CLI coder was given the tool-calling function list")
	}
}
