package prompts

import (
	"strings"
	"testing"
)

// Markers that must be present wherever the coder is guided to write Composio code.
// composio_helper is the seeded, Go-authored helper (internal/composioassets) that is
// now the single source of truth for the actual REST shape (base URL, connected-accounts
// lookup, execute body) — the prompt intentionally no longer restates that raw spec
// (see composioServicesBlock's doc comment), so this guard checks that every phase
// consistently points the coder at the helper + the correct host, not that it repeats
// endpoint-level detail.
const (
	v3Host    = "backend.composio.dev"
	v3Connect = "composio_helper"
)

// TestComposioSpecPresentInCodePhases is the regression guard for the bug where
// the v3 spec lived only in the design conversation, so generation wrote v1 code.
// Every phase that produces or guides CODE (create, edit, runtime) must carry the
// v3 spec when Composio is enabled — and must not leak it when disabled. The DESIGN
// conversation is deliberately excluded (see TestDesignPromptNeverLeaksComposioTech).
func TestComposioSpecPresentInCodePhases(t *testing.T) {
	history := []ChatMessage{{Role: "user", Content: "fetch my gmail"}}

	phases := map[string]struct {
		enabled  string
		disabled string
	}{
		"create": {
			enabled:  BuildImplementationPrompt("x", history, ImplementationParams{ComposioEnabled: true}),
			disabled: BuildImplementationPrompt("x", history, ImplementationParams{ComposioEnabled: false}),
		},
		"edit": {
			enabled:  BuildEditImplementationPrompt("x", history, ImplementationParams{ComposioEnabled: true}),
			disabled: BuildEditImplementationPrompt("x", history, ImplementationParams{ComposioEnabled: false}),
		},
		"runtime": {
			enabled:  BuildCoderPrompt(CoderPromptParams{AgentMD: "# Suggested schedule: none\ndo a thing", ComposioEnabled: true}),
			disabled: BuildCoderPrompt(CoderPromptParams{AgentMD: "# Suggested schedule: none\ndo a thing", ComposioEnabled: false}),
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

// TestRuntimePromptExecutesNotRebuilds guards the fix for the runtime re-building itself:
// a run must EXECUTE the already-built scripts, not re-explore, re-test, re-discover, or
// write probe scripts. Specifically, the build-time Composio DISCOVERY workflow (run
// composio_discover.py --toolkit … to find a slug) must NOT be injected at run time — that
// is what made agents re-run discovery every run.
func TestRuntimePromptExecutesNotRebuilds(t *testing.T) {
	p := BuildCoderPrompt(CoderPromptParams{AgentMD: "# Suggested schedule: none\ndo a thing", ComposioEnabled: true})

	// Establishes "already built — just run".
	if !strings.Contains(p, "ALREADY BUILT") {
		t.Error("runtime prompt should state the agent is already built and tested")
	}
	// Steers away from rebuilding/probing at run time.
	for _, want := range []string{"do NOT create any new", "Do NOT run composio_discover.py"} {
		if !strings.Contains(p, want) {
			t.Errorf("runtime prompt missing runtime guardrail %q", want)
		}
	}
	// The build-time discovery workflow must NOT leak into the run.
	if strings.Contains(p, "--toolkit") {
		t.Error("runtime prompt must not inject the build-time composio discovery workflow (--toolkit)")
	}
}

// TestDesignPromptNeverLeaksComposioTech guards the fix for the design conversation
// leaking implementation detail: the (deliberately non-technical) design prompt must
// NEVER contain the Composio helper/host/tool jargon, even when Composio is enabled —
// injecting the full spec here contradicted the jargon ban and made the designer leak
// technical detail and mix phases. When enabled it should carry only a plain-language
// capability note (<external_services>). When disabled it carries neither.
func TestDesignPromptNeverLeaksComposioTech(t *testing.T) {
	enabled := BuildDesignSystemPrompt(DesignSystemParams{AgentName: "x", ComposioEnabled: true})
	disabled := BuildDesignSystemPrompt(DesignSystemParams{AgentName: "x", ComposioEnabled: false})

	// Technical Composio detail must never reach the design conversation.
	for _, banned := range []string{v3Host, "composio_helper", "run_script", "composio_execute", "tool_slug", "COMPOSIO_API_KEY"} {
		if strings.Contains(enabled, banned) {
			t.Errorf("design prompt leaks technical Composio jargon %q", banned)
		}
	}
	// When enabled, the plain-language capability note is present.
	if !strings.Contains(enabled, "<external_services>") {
		t.Errorf("Composio-enabled design prompt missing plain-language external-services note")
	}
	// When disabled, neither the note nor the spec appears.
	if strings.Contains(disabled, "<external_services>") || strings.Contains(disabled, v3Host) {
		t.Errorf("Composio-disabled design prompt should mention no external-services capability")
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

// TestArchitectureGateInGenerationPrompts guards the tier forcing function: the
// mandatory <architecture_gate> (TASK ANALYSIS → TIER DECISION) must be present in BOTH
// the create and edit generation prompts — the edit path previously had no gate, letting
// an edit bolt on a script without justifying a [BULK] task.
func TestArchitectureGateInGenerationPrompts(t *testing.T) {
	history := []ChatMessage{{Role: "user", Content: "watch prices"}}
	for name, out := range map[string]string{
		"create": BuildImplementationPrompt("x", history, ImplementationParams{}),
		"edit":   BuildEditImplementationPrompt("x", history, ImplementationParams{}),
	} {
		if !strings.Contains(out, "<architecture_gate>") {
			t.Errorf("[%s] prompt missing <architecture_gate> forcing function", name)
		}
		if !strings.Contains(out, "TIER 1 and you create zero code files") {
			t.Errorf("[%s] prompt missing the hard default-to-TIER-1 rule", name)
		}
	}
}

// TestGateWeakBackendBias guards the capability-aware clause: only a tool-calling API
// coder (the weak builder) gets the extra "bias hard toward TIER 1" nudge; the capable
// CLI path keeps the neutral wording so it isn't burdened with friction.
func TestGateWeakBackendBias(t *testing.T) {
	history := []ChatMessage{{Role: "user", Content: "watch prices"}}
	const biasMarker = "You are a limited builder"

	weakCreate := BuildImplementationPrompt("x", history, ImplementationParams{BackendType: BackendToolCalling})
	weakEdit := BuildEditImplementationPrompt("x", history, ImplementationParams{BackendType: BackendToolCalling})
	fullCreate := BuildImplementationPrompt("x", history, ImplementationParams{BackendType: BackendFullCoder})

	if !strings.Contains(weakCreate, biasMarker) {
		t.Errorf("weak tool-calling create prompt should carry the TIER-1 bias clause")
	}
	if !strings.Contains(weakEdit, biasMarker) {
		t.Errorf("weak tool-calling edit prompt should carry the TIER-1 bias clause")
	}
	if strings.Contains(fullCreate, biasMarker) {
		t.Errorf("capable full-coder prompt must NOT carry the weak-builder bias clause (no new friction)")
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
