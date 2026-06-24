package prompts

import (
	"strings"
	"testing"
)

// v3 markers that must be present wherever the coder writes Composio code.
const (
	v3Host    = "backend.composio.dev/api/v3"
	v3Connect = "connected_accounts"
)

// TestComposioSpecPresentInEveryPhase is the regression guard for the bug where
// the v3 spec lived only in the design conversation, so generation wrote v1 code.
// Every phase that produces or guides code must carry the v3 spec when Composio
// is enabled — and must not leak it when disabled.
func TestComposioSpecPresentInEveryPhase(t *testing.T) {
	history := []ChatMessage{{Role: "user", Content: "fetch my gmail"}}

	phases := map[string]struct {
		enabled  string
		disabled string
	}{
		"design": {
			enabled:  BuildDesignSystemPrompt(DesignSystemParams{AgentName: "x", ComposioEnabled: true}),
			disabled: BuildDesignSystemPrompt(DesignSystemParams{AgentName: "x", ComposioEnabled: false}),
		},
		"create": {
			enabled:  BuildImplementationPrompt("x", history, ImplementationParams{ComposioEnabled: true}),
			disabled: BuildImplementationPrompt("x", history, ImplementationParams{ComposioEnabled: false}),
		},
		"edit": {
			enabled:  BuildEditImplementationPrompt("x", history, ImplementationParams{ComposioEnabled: true}),
			disabled: BuildEditImplementationPrompt("x", history, ImplementationParams{ComposioEnabled: false}),
		},
	}

	for name, p := range phases {
		if !strings.Contains(p.enabled, v3Host) || !strings.Contains(p.enabled, v3Connect) {
			t.Errorf("[%s] enabled prompt missing v3 spec (%q / %q)", name, v3Host, v3Connect)
		}
		// The block warns against v1 — confirm that guidance travels too.
		if !strings.Contains(p.enabled, "v3") {
			t.Errorf("[%s] enabled prompt missing v3 guidance", name)
		}
		if strings.Contains(p.disabled, v3Host) {
			t.Errorf("[%s] disabled prompt should not contain the Composio block", name)
		}
	}
}

// TestAgentPhilosophyPresentInEveryPhase guards the brain-vs-scripts contract: the
// agent must be told to reason about ambiguity at runtime and script only the
// repetitive work — at design, create, edit, AND runtime. Regression guard so a
// future prompt edit can't silently drop it from one phase.
func TestAgentPhilosophyPresentInEveryPhase(t *testing.T) {
	const marker = "<agent_philosophy>"
	history := []ChatMessage{{Role: "user", Content: "email me payroll"}}

	prompts := map[string]string{
		"design":  BuildDesignSystemPrompt(DesignSystemParams{AgentName: "x"}),
		"create":  BuildImplementationPrompt("x", history, ImplementationParams{}),
		"edit":    BuildEditImplementationPrompt("x", history, ImplementationParams{}),
		"runtime": BuildCoderPrompt(CoderPromptParams{AgentMD: "# Suggested schedule: none\ndo a thing"}),
	}

	for name, out := range prompts {
		if !strings.Contains(out, marker) {
			t.Errorf("[%s] prompt missing agent philosophy block (%q)", name, marker)
		}
	}

	// The design prompt must also carry the flexibility guidance that fixes the
	// "force the user to specify an exact pattern" failure.
	design := prompts["design"]
	if !strings.Contains(design, "design_for_flexibility") {
		t.Errorf("design prompt missing design_for_flexibility guidance")
	}
}

// TestTestingRulesInGenerationPrompts guards the fix for the guardrail blocking
// subprocess-based tests: the create and edit prompts must tell the coder to write
// import-based unit tests and must NOT shell out via subprocess from a test (which
// the AST guardrail rejects in every .py tool file).
func TestTestingRulesInGenerationPrompts(t *testing.T) {
	history := []ChatMessage{{Role: "user", Content: "test my agent"}}
	for name, out := range map[string]string{
		"create": BuildImplementationPrompt("x", history, ImplementationParams{}),
		"edit":   BuildEditImplementationPrompt("x", history, ImplementationParams{}),
	} {
		if !strings.Contains(out, "<testing_rules>") {
			t.Errorf("[%s] prompt missing <testing_rules> block", name)
		}
		if !strings.Contains(out, "subprocess") {
			t.Errorf("[%s] prompt should warn about subprocess in tests", name)
		}
		if !strings.Contains(out, "import") {
			t.Errorf("[%s] prompt should steer toward import-based tests", name)
		}
	}
}

// TestEditConversationIncludesToolScripts guards the fix for the edit flow not
// seeing the agent's scripts: when editing, the design system prompt must embed the
// actual tool source so the coder can diagnose code bugs without file access (and not
// ask the user where the scripts are). Create sessions must NOT include it.
func TestEditConversationIncludesToolScripts(t *testing.T) {
	tools := map[string]string{
		"fetch_bitcoin.py": "price = data['lastPrice']  # buggy field",
		"gmail_draft.py":   "body = f'Price: {price}'",
	}
	edit := BuildDesignSystemPrompt(DesignSystemParams{
		AgentName: "BTC", IsEdit: true, ExistingAgentMD: "# Suggested schedule: none",
		ExistingTools: tools,
	})
	if !strings.Contains(edit, "<current_tools>") {
		t.Fatalf("edit prompt missing <current_tools> block")
	}
	for _, want := range []string{"fetch_bitcoin.py", "gmail_draft.py", "lastPrice"} {
		if !strings.Contains(edit, want) {
			t.Errorf("edit prompt missing %q from tool scripts", want)
		}
	}

	// Create sessions never carry existing tools.
	create := BuildDesignSystemPrompt(DesignSystemParams{AgentName: "BTC"})
	if strings.Contains(create, "<current_tools>") {
		t.Errorf("create prompt should not contain a tools block")
	}
}

// TestShellSafetyInPrompts guards the fix for the shell-$-expansion bug (a "$62,752"
// shell argument arriving as "2,752"): the create, edit, AND runtime prompts must warn
// against passing dynamic/$-containing data as a shell argument.
func TestShellSafetyInPrompts(t *testing.T) {
	prompts := map[string]string{
		"create":  BuildImplementationPrompt("x", nil, ImplementationParams{}),
		"edit":    BuildEditImplementationPrompt("x", nil, ImplementationParams{}),
		"runtime": BuildCoderPrompt(CoderPromptParams{AgentMD: "# Suggested schedule: none\ndo a thing"}),
	}
	for name, out := range prompts {
		if !strings.Contains(out, "<shell_safety>") {
			t.Errorf("[%s] prompt missing <shell_safety> block", name)
		}
	}

	// set -euo pipefail guidance and the path rule live in the shell block.
	create := prompts["create"]
	for _, want := range []string{"set -euo pipefail", "do NOT `cd`"} {
		if !strings.Contains(create, want) {
			t.Errorf("create prompt missing shell guidance %q", want)
		}
	}

	// Script-robustness defenses must reach generation; the runtime restates the
	// judgment-level rules (sanity-check, side-effect verification).
	for _, name := range []string{"create", "edit"} {
		if !strings.Contains(prompts[name], "<script_robustness>") {
			t.Errorf("[%s] prompt missing <script_robustness> block", name)
		}
	}
	if !strings.Contains(prompts["runtime"], "Sanity-check before acting") {
		t.Errorf("runtime prompt missing sanity-check guidance")
	}
}

// TestConnectedPlatformsInGeneration verifies platform context reaches generation.
func TestConnectedPlatformsInGeneration(t *testing.T) {
	out := BuildImplementationPrompt("x", nil, ImplementationParams{ConnectedPlatforms: []string{"Telegram"}})
	if !strings.Contains(out, "Telegram") || !strings.Contains(out, "[CHAT]") {
		t.Errorf("generation prompt missing connected-platform guidance:\n%s", out)
	}
}
