package prompts

import (
	"strings"
	"testing"
)

// Chat must know what the platform IS, not only what its own tools are.
//
// Onboarding now hands a new owner a chat and invites them to ask what Rookery
// can do. productIdentityBlock alone named the knowledge base, agents, skills,
// reminders and connections — and said nothing about secrets, MCP servers,
// providers, coders or chat apps. A button labelled "Explore what you can do!"
// opening a conversation that cannot answer that question is worse than no
// button.
func TestChatPromptCarriesThePlatformPrimer(t *testing.T) {
	got := BuildChatSystemPrompt("/vaults/w1", "api", nil, nil, "", nil)

	if !strings.Contains(got, "<platform_context>") {
		t.Fatal("the chat prompt must carry the platform primer")
	}
	for _, want := range []string{"secret", "knowledge base", "agent", "skill"} {
		if !strings.Contains(strings.ToLower(got), want) {
			t.Errorf("the chat prompt does not mention %q", want)
		}
	}
}

// The primer takes the surface as a parameter for exactly this reason: it
// embeds productIdentityBlock, and hardcoding SurfaceAgent would open a CHAT
// with "right now you are an AGENT run" — false, and an invitation to emit the
// [CHAT]/[STATE] output-protocol markers at a human.
func TestChatPromptDoesNotClaimToBeAnAgentRun(t *testing.T) {
	got := BuildChatSystemPrompt("/vaults/w1", "api", nil, nil, "", nil)

	if strings.Contains(got, "you are an AGENT run") {
		t.Error("the chat prompt must not describe itself as an agent run")
	}
	if !strings.Contains(got, "you are the CHAT assistant") {
		t.Error("the chat prompt must keep the chat surface's own description")
	}
	// The chat surface's honest statement of its limits has to survive too —
	// it is what stops the model claiming it created an agent.
	if !strings.Contains(got, "cannot") {
		t.Error("the chat prompt must keep its statement of what chat cannot do")
	}
}

// The agent-facing prompts must be unaffected by the surface becoming a
// parameter.
func TestAgentPromptsStillDescribeAnAgentRun(t *testing.T) {
	got := platformContextBlock(SurfaceAgent, nil, "/vaults/w1")
	if !strings.Contains(got, "you are an AGENT run") {
		t.Error("the agent primer must still describe an agent run")
	}
}

// A workspace with a chat app connected should have it named, on both
// surfaces, from the one mapping.
func TestChatPromptNamesConnectedChatApps(t *testing.T) {
	apps := ChatAppsForPlatforms([]string{"telegram"})
	got := BuildChatSystemPrompt("/vaults/w1", "api", nil, nil, "", apps)
	if !strings.Contains(strings.ToLower(got), "telegram") {
		t.Error("a connected chat app must be named in the chat prompt")
	}
}
