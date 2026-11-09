package prompts

import (
	"strings"
	"testing"
)

func designPrompt(edit, avail, dollars bool) string {
	return BuildDesignSystemPrompt(DesignSystemParams{
		AgentName: "x", IsEdit: edit, ExistingAgentMD: "# x",
		LimitsAvailable: avail, LimitsDollars: dollars,
	})
}

func TestDesignPromptAsksLimitsOnlyWhenAvailable(t *testing.T) {
	on := designPrompt(false, true, false)
	if !strings.Contains(on, "6. Whether the user wants spending limits") || !strings.Contains(on, "\nLimits: none |") {
		t.Fatal("limits question and spec line missing when available")
	}
	off := designPrompt(false, false, false)
	if strings.Contains(off, "spending limits") || strings.Contains(off, "Limits:") {
		t.Fatal("limits leaked into a prompt where they cannot be enforced")
	}
	if !strings.Contains(on, `"token" is allowed`) {
		t.Fatal("token must be allowed")
	}
}

func TestDesignPromptOffersDollarsOnlyWhenSupported(t *testing.T) {
	if !strings.Contains(designPrompt(false, true, true), "$0.50") {
		t.Fatal("dollars should be offered")
	}
	p := designPrompt(false, true, false)
	if strings.Contains(p, "$0.50") || !strings.Contains(p, "never offer or\n   promise a dollar limit") {
		t.Fatal("dollars offered without cost reporting")
	}
}

func TestEditPromptDefaultsLimitsUnchanged(t *testing.T) {
	p := designPrompt(true, true, true)
	if !strings.Contains(p, "Limits: unchanged |") {
		t.Fatal("edit prompt must default limits to unchanged")
	}
	if strings.Contains(designPrompt(true, false, false), "Limits:") {
		t.Fatal("edit prompt mentions limits when unavailable")
	}
}

// An edit that changes one limit must not erase the others, so the designer is
// told what the agent has now and that only the periods it writes change.
func TestEditPromptStatesCurrentLimitsAndPerPeriodRule(t *testing.T) {
	p := BuildDesignSystemPrompt(DesignSystemParams{
		AgentName: "x", IsEdit: true, ExistingAgentMD: "# x",
		LimitsAvailable: true, CurrentLimits: "per day: 500000 tokens; per month: $30.00",
	})
	if !strings.Contains(p, "currently: per day: 500000 tokens; per month: $30.00") {
		t.Fatal("edit prompt must state the agent's current limits")
	}
	if !strings.Contains(p, "per day: none") || strings.Contains(p, "complete new set") {
		t.Fatal("edit prompt must describe the per-period rule, not a full replacement")
	}
	none := BuildDesignSystemPrompt(DesignSystemParams{
		AgentName: "x", IsEdit: true, ExistingAgentMD: "# x", LimitsAvailable: true,
	})
	if !strings.Contains(none, "currently: none") {
		t.Fatal("an agent with no limits must read as none, not a blank")
	}
	if strings.Contains(designPrompt(false, true, false), "currently:") {
		t.Fatal("a create has no current limits to state")
	}
}
