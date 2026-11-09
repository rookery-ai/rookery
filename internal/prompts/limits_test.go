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
