package prompts

import (
	"strings"
	"testing"
)

// The designer used to be told to append [TECHNICAL SPEC] "after the user
// approves". That turn does not exist: agentdesigner.stepDesigning matches
// isApproval and calls startGeneration without another coder round trip, so the
// block was never written — while BuildImplementationPrompt refers to it by name
// and had been reading a block nothing produced.
//
// The fix is timing, and timing is exactly the kind of prompt detail a later
// rewrite restores to the plausible-sounding wrong version. Both branches are
// pinned.
func TestSpecBlockIsRequestedWithTheProposalNotAfterApproval(t *testing.T) {
	for _, isEdit := range []bool{false, true} {
		name := "create"
		if isEdit {
			name = "edit"
		}
		t.Run(name, func(t *testing.T) {
			p := BuildDesignSystemPrompt(DesignSystemParams{AgentName: "watcher", IsEdit: isEdit})
			if !strings.Contains(p, "[TECHNICAL SPEC]") {
				t.Fatal("prompt no longer asks for a [TECHNICAL SPEC] block at all")
			}
			if strings.Contains(p, "After the user approves, append this block") {
				t.Error("prompt asks for the spec AFTER approval — that turn never happens")
			}
			if !strings.Contains(p, "In the SAME message") {
				t.Error("prompt does not tie the spec block to the proposal turn")
			}
			// The block is stripped before display, so the model must be told not
			// to mention it — otherwise the user reads prose referring to a block
			// they cannot see.
			if !strings.Contains(p, "never refer to it in your prose") {
				t.Error("prompt does not tell the designer the block is hidden from the user")
			}
		})
	}
}

// Both surfaces must name the act the same way: the browser button says
// "Approve & build", so chat must not say merely "type approve to proceed".
func TestDesignPromptNamesApprovalAndBuildTogether(t *testing.T) {
	for _, isEdit := range []bool{false, true} {
		p := BuildDesignSystemPrompt(DesignSystemParams{AgentName: "watcher", IsEdit: isEdit})
		if !strings.Contains(p, "build it") {
			t.Errorf("isEdit=%v: approval copy does not say the designer will build it", isEdit)
		}
	}
}

// The technical spec is what the pre-build Spec view renders, and the bindings
// are the part a user re-reading an approved plan most wants to check. They used
// to be visible only after a build, parsed off AGENT.md — the one moment they
// have stopped being a question.
func TestSpecBlockDeclaresTheBindings(t *testing.T) {
	p := BuildDesignSystemPrompt(DesignSystemParams{AgentName: "watcher"})
	for _, want := range []string{"Connections:", "Skills:", "MCP servers:"} {
		if !strings.Contains(p, want) {
			t.Errorf("the [TECHNICAL SPEC] block no longer asks for %q", want)
		}
	}
}

// A model cannot name a server it has never been shown, and asking it to would
// turn the spec line into an invitation to invent one.
func TestMCPServersAreNamedToTheDesigner(t *testing.T) {
	p := BuildDesignSystemPrompt(DesignSystemParams{
		AgentName:  "watcher",
		MCPServers: []MCPServerRef{{Name: "weather"}, {Name: "home-assistant"}},
	})
	if !strings.Contains(p, "The user has connected these MCP servers.") {
		t.Fatal("no MCP server block in the design prompt")
	}
	for _, name := range []string{"weather", "home-assistant"} {
		if !strings.Contains(p, name) {
			t.Errorf("server %q is not named to the designer", name)
		}
	}
	// A workspace with no servers must not get an empty block claiming it has
	// them — the same shape as <available_connections>.
	bare := BuildDesignSystemPrompt(DesignSystemParams{AgentName: "watcher"})
	// Probed by the block's own sentence, not its tag: the tag also appears in
	// the [TECHNICAL SPEC] line that tells the designer where to read server
	// names from, which is emitted unconditionally.
	if strings.Contains(bare, "The user has connected these MCP servers.") {
		t.Error("an empty MCP block is emitted for a workspace with no servers")
	}
}
