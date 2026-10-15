package prompts

import (
	"strings"
	"testing"
)

// Designing a purchase agent, the designer told the owner it would "pause at the
// final 'place order' step and ask for your go-ahead". No such step exists:
// permission for an irreversible action is granted once, at "Allow and build",
// and without it the agent stops at that step and reports. The designer must be
// told how it works, or it improvises a feature the product does not have.
func TestDesignPromptDescribesIrreversiblePermissionAccurately(t *testing.T) {
	got := BuildDesignSystemPrompt(DesignSystemParams{AgentName: "Replacement monitor"})
	for _, want := range []string{"Allow and build", "pause", "agent's page"} {
		if !strings.Contains(got, want) {
			t.Errorf("design prompt does not explain irreversible permission (missing %q)", want)
		}
	}
	if !strings.Contains(strings.ToLower(got), "never promise") {
		t.Error("design prompt does not forbid promising a mid-run approval step")
	}
}
