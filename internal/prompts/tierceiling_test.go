package prompts

import (
	"strings"
	"testing"
)

// The API engine (BackendToolCalling) builds TIER 1 agents only; TIER 2/3 are reserved for
// CLI coders. These pin both halves in every prompt the build and design read, plus the
// runtime half that must NOT change: an agent a CLI coder built still runs on an API coder.

func TestToolCallingBuildPromptsCapTheTierAtOne(t *testing.T) {
	p := ImplementationParams{BackendType: BackendToolCalling}
	for name, prompt := range map[string]string{
		"create": BuildImplementationPrompt("a", nil, p),
		"edit":   BuildEditImplementationPrompt("a", nil, p),
	} {
		if !strings.Contains(prompt, "TIER CEILING ON THIS BACKEND: TIER 1") {
			t.Errorf("%s: the gate must cap the API engine at TIER 1", name)
		}
		if !strings.Contains(prompt, "Tier ceiling on THIS coder: TIER 1 only") {
			t.Errorf("%s: the philosophy block must state the ceiling", name)
		}
		// The old routing sent exactly these calls to a helper script — the failure.
		if strings.Contains(prompt, "That is [BULK] → TIER 2") || strings.Contains(prompt, "one thin helper script") {
			t.Errorf("%s: the API engine must no longer be routed to a TIER 2 script", name)
		}
		if !strings.Contains(prompt, "CLI coder") {
			t.Errorf("%s: the prompt must name the way through (a CLI coder)", name)
		}
	}
}

func TestCLIBuildPromptsKeepAllThreeTiers(t *testing.T) {
	prompt := BuildImplementationPrompt("a", nil, ImplementationParams{BackendType: BackendFullCoder})
	for _, banned := range []string{"TIER CEILING ON THIS BACKEND", "Tier ceiling on THIS coder"} {
		if strings.Contains(prompt, banned) {
			t.Errorf("a CLI coder must keep TIER 2/3; found %q", banned)
		}
	}
}

func TestEditPromptKeepsCLIBuiltToolsOnTheAPIEngine(t *testing.T) {
	prompt := BuildEditImplementationPrompt("a", nil, ImplementationParams{BackendType: BackendToolCalling})
	if !strings.Contains(prompt, "leave every one\n  of them byte-for-byte unchanged") {
		t.Error("an API-engine edit must be told to leave the CLI-built tools alone")
	}
}

// forceTier1Block used to say "DELETE" the code files unconditionally. On an edit the
// working copy holds the live agent's tools, so that silently stripped a CLI-built agent.
func TestForceTier1OnAnEditDoesNotDeleteTheAgentsTools(t *testing.T) {
	edit := BuildEditImplementationPrompt("a", nil, ImplementationParams{BackendType: BackendToolCalling, ForceTier1: true, IsEdit: true})
	if strings.Contains(edit, "DELETE them") {
		t.Error("an edit's zero-code override must not tell the model to delete the agent's tools")
	}
	if !strings.Contains(edit, "Leave the agent's EXISTING code files exactly as they are") {
		t.Error("an edit's zero-code override must say to keep the existing tools")
	}
	create := BuildImplementationPrompt("a", nil, ImplementationParams{BackendType: BackendToolCalling, ForceTier1: true})
	if !strings.Contains(create, "DELETE them") {
		t.Error("a create's zero-code override must still clean up the previous attempt's files")
	}
}

func TestDesignPromptCapsTheTierOnlyForTheAPIEngine(t *testing.T) {
	api := BuildDesignSystemPrompt(DesignSystemParams{AgentName: "a", BackendType: BackendToolCalling})
	if !strings.Contains(api, "<coder_tier_ceiling>") {
		t.Error("the designer must know an API-engine workspace builds TIER 1 only, or it proposes plans the build refuses")
	}
	cli := BuildDesignSystemPrompt(DesignSystemParams{AgentName: "a", BackendType: BackendFullCoder})
	if strings.Contains(cli, "<coder_tier_ceiling>") {
		t.Error("a CLI-coder workspace must not be told it is limited to TIER 1")
	}
}

// Running is not building: a TIER 2 agent a CLI coder built must keep working when the
// workspace switches to a cheaper API coder, which needs run_script at run time.
func TestRuntimePromptStillOffersScriptsOnTheAPIEngine(t *testing.T) {
	prompt := BuildCoderPrompt(CoderPromptParams{AgentMD: "run tools/fetch.py", BackendType: BackendToolCalling})
	if !strings.Contains(prompt, "run_script") {
		t.Error("the runtime prompt must still describe run_script on the API engine")
	}
	if strings.Contains(prompt, "TIER CEILING") || strings.Contains(prompt, "Tier ceiling on THIS coder") {
		t.Error("the tier ceiling is a BUILD rule and must not reach the runtime prompt")
	}
}
