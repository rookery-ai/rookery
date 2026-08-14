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
