package prompts

import (
	"strings"
	"testing"
)

// Chat must send someone who wants an agent to the agent DESIGNER, and must not
// hand them a file format.
//
// The reported failure: a new owner finished the setup wizard, clicked "Explore
// what you can do!", asked about agents, and was told to create one manually and
// write AGENT.md. Nothing the model said about the platform was wrong — the
// problem was what it could see. platformContextBlock taught chat the whole
// agent file format (the agents/<id>/ layout, the `# Suggested schedule:` header
// line, the output protocol) and named no way to CREATE an agent, so the most
// concrete path available to it was "write the file yourself". It was reciting
// this block, the same way it once defended the [CHAT] markers to an owner who
// asked it to stop emitting them.
//
// This is the shape TestInboxBlockPromisesNoChannelSelection already pins: a
// capability the product has, absent from the prompt, produces a confident
// answer pointing somewhere else.
func TestChatIsToldHowAgentsAreActuallyCreated(t *testing.T) {
	got := BuildChatSystemPrompt("/vaults/w1", "api", nil, nil, "", nil, false)

	// The navigation path, in the owner's own terms. A model told only that
	// "the owner creates agents in the app" still has to guess where, and
	// guessing is what produced the bug.
	for _, want := range []string{"Agents", "New Agent"} {
		if !strings.Contains(got, want) {
			t.Errorf("the chat prompt must name the %q step of the agent designer flow", want)
		}
	}
	if !strings.Contains(strings.ToLower(got), "plain language") {
		t.Error("the chat prompt must say the owner DESCRIBES the agent rather than authoring it")
	}
	if !strings.Contains(strings.ToLower(got), "agent designer") {
		t.Error("the chat prompt must name the agent designer")
	}
}

// Naming the designer is not enough on its own: the model has to be told not to
// offer the alternative it can still infer from the rest of the primer, which
// describes agents/<id>/AGENT.md in detail for reasons that have nothing to do
// with chat.
func TestChatIsToldNotToHandTheOwnerAFile(t *testing.T) {
	got := BuildChatSystemPrompt("/vaults/w1", "api", nil, nil, "", nil, false)

	if !strings.Contains(got, "never tell the owner to write AGENT.md") {
		t.Error("the chat prompt must explicitly rule out telling the owner to author AGENT.md")
	}
}

// The authoring format is agent-surface knowledge. Chat holding it is what
// produced the "write AGENT.md yourself" answer, so the gate has to be real and
// not merely a nudge in the surrounding prose.
func TestChatIsNotTaughtTheAgentFileFormat(t *testing.T) {
	got := BuildChatSystemPrompt("/vaults/w1", "api", nil, nil, "", nil, false)

	// The schedule header is the sharpest case: it is a line the owner would
	// have to type into a file by hand, and chat used to be handed its exact
	// syntax.
	if strings.Contains(got, "# Suggested schedule:") {
		t.Error("chat must not be given the AGENT.md schedule header syntax — that line is one the owner never writes")
	}
	if strings.Contains(got, "AGENT.md line 1") {
		t.Error("chat must not be told where in AGENT.md the schedule goes")
	}
}

// Deleting the authoring detail from the AGENT surface would silence every
// build on the install, so both halves are pinned — the same reason the
// output-protocol gate carries a test on each side.
func TestTheAgentSurfaceKeepsTheAgentFileFormat(t *testing.T) {
	got := platformContextBlock(SurfaceAgent, nil, "/vaults/w1")

	if !strings.Contains(got, "# Suggested schedule:") {
		t.Error("the agent primer must keep the AGENT.md schedule header syntax")
	}
	if !strings.Contains(got, "[CHAT]") {
		t.Error("the agent primer must keep the output protocol")
	}
}

// An agent is told about the designer too, but for a different reason than chat
// is: an agent that rewrites its own AGENT.md is fighting the thing that wrote
// it, and the owner's next edit through the designer will overwrite the change
// without either side reporting a conflict.
func TestTheAgentSurfaceIsAlsoToldAboutTheDesigner(t *testing.T) {
	got := platformContextBlock(SurfaceAgent, nil, "/vaults/w1")

	if !strings.Contains(got, "agent designer") {
		t.Error("the agent primer must name the designer that wrote it")
	}
}

// The designer's own prompts describe the agent it is building, so telling them
// that the owner reaches the designer through Agents → New Agent would be both
// redundant and confusing — the designer IS that screen. Pinned because the
// obvious way to implement the fix above is to add the section unconditionally.
func TestTheDesignerIsNotToldToPointAtItself(t *testing.T) {
	got := BuildDesignSystemPrompt(DesignSystemParams{})

	if strings.Contains(got, "Agents → New Agent") {
		t.Error("the designer must not be told to send the owner to the screen it is already running on")
	}
}
