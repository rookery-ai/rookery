package prompts

import (
	"strings"
	"testing"
)

// The agent surface must keep the protocol: an agent that is not told about
// [CHAT] cannot report at all, and deleting this section would look like tidying
// up while silencing every agent on the install.
func TestAgentSurfaceKeepsTheOutputProtocol(t *testing.T) {
	p := platformContextBlock(SurfaceAgent, nil, "/vault")
	if !strings.Contains(p, "## Output protocol (how agents communicate)") {
		t.Fatal("agent surface lost the output protocol section")
	}
	for _, want := range []string{"[CHAT]", "[STATE]", "[CALL: agent-name]", "[SILENT]"} {
		if !strings.Contains(p, want) {
			t.Errorf("agent surface no longer describes %s", want)
		}
	}
}

// The chat surface must NOT carry it. This block was the cause of the leak: a
// standing instruction to wrap replies in [CHAT], obeyed at a rate that varied
// by model family, by model strength and by turn depth — which is why it read as
// flakiness. Chat has no parser for the markers, so anything emitted reaches the
// owner verbatim.
func TestChatSurfaceDoesNotInstructTheOutputProtocol(t *testing.T) {
	p := platformContextBlock(SurfaceChat, nil, "/vault")
	if strings.Contains(p, "## Output protocol (how agents communicate)") {
		t.Fatal("chat surface still carries the agent output-protocol section")
	}
	// The instruction lines specifically — not every mention of the token, since
	// the Inbox section legitimately explains that notifying reaches both the
	// inbox and chat apps.
	for _, banned := range []string{
		"[CHAT] Message to send to the user.",
		"Agents produce output ONLY via these markers",
		"Emit this ALONE as the last line",
	} {
		if strings.Contains(p, banned) {
			t.Errorf("chat surface still instructs the protocol: %q", banned)
		}
	}
}

// Silence is the reproducer. "stay silent" / "don't say anything" produced a
// bare [SILENT] on every model tested, because the protocol section offered a
// marker as the way to say nothing. The chat surface has to name the right
// behaviour instead, or the model reaches for a marker it was never given.
func TestChatSurfaceSaysWhatToDoWhenAskedToBeQuiet(t *testing.T) {
	p := platformContextBlock(SurfaceChat, nil, "/vault")
	if !strings.Contains(p, "never\nemit those markers") {
		t.Error("chat surface does not tell the model to avoid the markers")
	}
	if !strings.Contains(p, "a short sentence, not a marker") {
		t.Error("chat surface does not say what to do when asked to say nothing — " +
			"the case that reproduced the leak on every model")
	}
}

// BuildChatSystemPrompt is what the chat surface actually ships; asserting on
// platformContextBlock alone would miss a caller that reintroduced the section
// some other way.
func TestBuildChatSystemPromptShipsNoProtocolInstruction(t *testing.T) {
	p := BuildChatSystemPrompt("/vault", "api", nil, nil, "", nil, false)
	if strings.Contains(p, "Agents produce output ONLY via these markers") {
		t.Fatal("the chat system prompt instructs the agent output protocol")
	}
}
