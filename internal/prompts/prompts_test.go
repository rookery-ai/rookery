package prompts

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

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
// TestGateToolCallingNetworkSplit guards the tier model for the tool-calling (API) backend
// now that it has web_fetch + bash: a simple PUBLIC read is web_fetch (TIER 1, no script);
// a call needing a secret / pagination / heavy processing is a helper script (TIER 2). The
// gate must express this split — not the earlier "every external call REQUIRES a script"
// clause (which over-forced scripts) and not the original "bias hard toward TIER 1" clause
// (which produced script-less fetch agents that couldn't fetch).
//
// Regression guard on prompt content — it does NOT prove a given model complies; that is
// verified by re-running the weather/news build on the API backend.
func TestGateToolCallingNetworkSplit(t *testing.T) {
	history := []ChatMessage{{Role: "user", Content: "watch prices"}}
	const oldHarmfulBias = "Bias hard toward TIER 1"
	const oldOverForce = "REQUIRES a helper script"

	weakCreate := BuildImplementationPrompt("x", history, ImplementationParams{BackendType: BackendToolCalling})
	weakEdit := BuildEditImplementationPrompt("x", history, ImplementationParams{BackendType: BackendToolCalling})
	fullCreate := BuildImplementationPrompt("x", history, ImplementationParams{BackendType: BackendFullCoder})

	for name, p := range map[string]string{"create": weakCreate, "edit": weakEdit} {
		if !strings.Contains(p, "web_fetch") {
			t.Errorf("weak tool-calling %s prompt must tell the model to use web_fetch for a simple public read", name)
		}
		if !strings.Contains(p, "secret") {
			t.Errorf("weak tool-calling %s prompt must say a script is for calls needing a secret/pagination/heavy processing", name)
		}
		if strings.Contains(p, oldHarmfulBias) {
			t.Errorf("weak tool-calling %s prompt must NOT carry the harmful 'bias hard toward TIER 1' clause", name)
		}
		if strings.Contains(p, oldOverForce) {
			t.Errorf("weak tool-calling %s prompt must NOT over-force a script for every external call now that web_fetch exists", name)
		}
	}
	if strings.Contains(fullCreate, "web_fetch") {
		t.Errorf("capable full-coder prompt must NOT carry the tool-calling network split (CLI coders fetch directly — no new friction)")
	}
}

// TestGateToolCallingHasWorkedExample guards the few-shot that steers weak-model tier/tool
// selection: the tool-calling gate must carry a worked TASK ANALYSIS example (a public fetch
// resolving to web_fetch + TIER 1). The full-coder gate must not carry it (no new friction).
func TestGateToolCallingHasWorkedExample(t *testing.T) {
	weak := BuildImplementationPrompt("x", []ChatMessage{{Role: "user", Content: "morning brief"}}, ImplementationParams{BackendType: BackendToolCalling})
	full := BuildImplementationPrompt("x", nil, ImplementationParams{BackendType: BackendFullCoder})
	if !strings.Contains(weak, "WORKED EXAMPLE") {
		t.Errorf("tool-calling gate must carry a worked example to steer weak-model tool/tier selection")
	}
	if strings.Contains(full, "WORKED EXAMPLE") {
		t.Errorf("full-coder gate must not carry the tool-calling worked example")
	}
}

// TestPhilosophyToolCallingUsesWebFetch guards that the shared philosophy block, on the
// tool-calling backend, drops the CLI "don't script one HTTP request" bullet and instead
// points at web_fetch — so it does not contradict the architecture gate for a weak model
// reading both in one prompt.
func TestPhilosophyToolCallingUsesWebFetch(t *testing.T) {
	const cliBullet = "DO NOT write a helper script to make one simple HTTP request"
	full := agentPhilosophyBlock(BackendFullCoder)
	tool := agentPhilosophyBlock(BackendToolCalling)
	if !strings.Contains(full, cliBullet) {
		t.Errorf("full-coder philosophy should keep the CLI HTTP bullet")
	}
	if strings.Contains(tool, cliBullet) {
		t.Errorf("tool-calling philosophy must NOT carry the CLI HTTP bullet")
	}
	if !strings.Contains(tool, "web_fetch") {
		t.Errorf("tool-calling philosophy must point a simple public request at web_fetch")
	}
}

// TestCapabilitiesToolCallingHasNetworkTools guards that the tool-calling capabilities block
// advertises web_fetch and bash (so the model knows they exist) and states the secret
// boundary (web_fetch cannot carry secrets → use a script/bash for authenticated calls).
func TestCapabilitiesToolCallingHasNetworkTools(t *testing.T) {
	block := coderCapabilitiesBlock(BackendToolCalling)
	for _, want := range []string{"web_fetch", "bash", "secret"} {
		if !strings.Contains(block, want) {
			t.Errorf("tool-calling capabilities block must mention %q", want)
		}
	}
}

// TestCapabilitiesToolCallingHasDiscoveryTools guards that the tool-calling capabilities
// block advertises the three discovery tools (search_files, glob, web_search) so a weak
// model knows they exist — and that the full-coder block does NOT carry them (CLI coders
// have native Grep/Glob/WebSearch; advertising host tools there adds friction for nothing).
func TestCapabilitiesToolCallingHasDiscoveryTools(t *testing.T) {
	tool := coderCapabilitiesBlock(BackendToolCalling)
	full := coderCapabilitiesBlock(BackendFullCoder)
	for _, want := range []string{"search_files", "glob", "web_search"} {
		if !strings.Contains(tool, want) {
			t.Errorf("tool-calling capabilities block must mention %q", want)
		}
		if strings.Contains(full, want) {
			t.Errorf("full-coder capabilities block must NOT mention host tool %q (CLI has its own)", want)
		}
	}
}

// TestGateToolCallingHasDiscoveryTools guards that the architecture gate, on the
// tool-calling backend, tells the model search_files/glob/web_search are TIER-1 reads (no
// script) and to actually call them during a build. The full-coder gate must not.
func TestGateToolCallingHasDiscoveryTools(t *testing.T) {
	weakCreate := BuildImplementationPrompt("x", nil, ImplementationParams{BackendType: BackendToolCalling})
	weakEdit := BuildEditImplementationPrompt("x", nil, ImplementationParams{BackendType: BackendToolCalling})
	fullCreate := BuildImplementationPrompt("x", nil, ImplementationParams{BackendType: BackendFullCoder})
	for name, p := range map[string]string{"create": weakCreate, "edit": weakEdit} {
		for _, want := range []string{"search_files", "glob", "web_search"} {
			if !strings.Contains(p, want) {
				t.Errorf("weak tool-calling %s gate must mention %q", name, want)
			}
		}
	}
	for _, want := range []string{"search_files", "glob(pattern)", "web_search"} {
		if strings.Contains(fullCreate, want) {
			t.Errorf("full-coder gate must NOT mention host tool %q", want)
		}
	}
}

// TestChatToolCallingHasSearchAndGlob guards chat parity with the CLI chat path: the
// tool-calling chat prompt must offer search_files + glob (file-read, safe in chat) and,
// as of Task 7, web_search/web_fetch too — both are read-only, cannot carry secrets, and
// cannot reach private addresses, so they are no longer exec-gated in chat. run_script and
// bash remain exec-gated (they execute code) and must NOT appear.
func TestChatToolCallingHasSearchAndGlob(t *testing.T) {
	p := BuildChatSystemPrompt("/tmp/vault", BackendToolCalling, nil, nil, "")
	for _, want := range []string{"search_files", "glob", "web_search", "web_fetch"} {
		if !strings.Contains(p, want) {
			t.Errorf("tool-calling chat prompt must offer %q", want)
		}
	}
	if strings.Contains(p, "run_script") {
		t.Errorf("tool-calling chat prompt must NOT offer run_script (chat cannot run code)")
	}
}

// TestChatSystemPromptConnectorsNativeTools guards a real bug: BuildChatSystemPrompt must
// map the raw coder backend string (e.g. "api", what coder.BackendType() actually returns)
// to the canonical BackendToolCalling constant before handing it to connectedToolsBlock —
// otherwise the direct string comparison inside that block fails and it wrongly emits the
// CLI `connector exec` bridge instructions (which chat never wires up) instead of the
// native-tools instructions.
func TestChatSystemPromptConnectorsNativeTools(t *testing.T) {
	p := BuildChatSystemPrompt("/tmp/vault", "api", []ConnectionRef{{Provider: "google", Label: "work", Identity: "a@b.com"}}, []string{"gmail_send_email"}, "")
	if !strings.Contains(p, "NATIVE tools") {
		t.Errorf("chat prompt with connections on the API backend must advertise NATIVE tools, got:\n%s", p)
	}
	if strings.Contains(p, "connector exec") {
		t.Errorf("chat prompt with connections on the API backend must NOT advertise the CLI bridge command, got:\n%s", p)
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

// TestMapCoderBackendCLICoders locks the backend-type→capability mapping: every CLI
// coder backend (claude and the newer opencode/codex/gemini/cursor backends, which
// fall through the default case) must map to BackendFullCoder, while the API engine
// maps to BackendToolCalling. Regression guard against a future MapCoderBackend edit
// silently downgrading a CLI coder's capability tier.
func TestMapCoderBackendCLICoders(t *testing.T) {
	for _, bt := range []string{"claude", "opencode", "codex", "gemini", "cursor"} {
		if got := MapCoderBackend(bt); got != BackendFullCoder {
			t.Errorf("MapCoderBackend(%q) = %q, want BackendFullCoder", bt, got)
		}
	}
	if MapCoderBackend("api") != BackendToolCalling {
		t.Errorf("api should map to BackendToolCalling")
	}
}

// TestNoStateJSONMentionsAnywhere guards the state.json → state.md rename (spec
// §8): agent memory now lives in state.md (a json fence + an agent-writable
// "## Notes" section), but the [STATE] output marker is UNCHANGED — agents still
// emit [STATE], the runner still does the write. No built prompt string — design,
// create, edit, or runtime, on any backend — should still tell a model its memory
// file is called state.json. The runtime prompt (and the shared platform-context
// block it's built from) must positively mention state.md so the rename is actually
// communicated, not just the old name removed.
func TestNoStateJSONMentionsAnywhere(t *testing.T) {
	history := []ChatMessage{{Role: "user", Content: "track something and remember it"}}

	prompts := map[string]string{
		"design_create":      BuildDesignSystemPrompt(DesignSystemParams{AgentName: "x"}),
		"design_edit":        BuildDesignSystemPrompt(DesignSystemParams{AgentName: "x", IsEdit: true, ExistingAgentMD: "# Suggested schedule: none"}),
		"create_full":        BuildImplementationPrompt("x", history, ImplementationParams{BackendType: BackendFullCoder}),
		"create_toolcalling": BuildImplementationPrompt("x", history, ImplementationParams{BackendType: BackendToolCalling}),
		"edit_full":          BuildEditImplementationPrompt("x", history, ImplementationParams{BackendType: BackendFullCoder}),
		"edit_toolcalling":   BuildEditImplementationPrompt("x", history, ImplementationParams{BackendType: BackendToolCalling}),
		"runtime_full": BuildCoderPrompt(CoderPromptParams{
			AgentMD: "# Suggested schedule: none\ndo a thing", BackendType: BackendFullCoder,
			VaultRoot: "/tmp/vault", AgentDir: "/tmp/vault/agents/1",
		}),
		"runtime_toolcalling": BuildCoderPrompt(CoderPromptParams{
			AgentMD: "# Suggested schedule: none\ndo a thing", BackendType: BackendToolCalling,
			VaultRoot: "/tmp/vault", AgentDir: "/tmp/vault/agents/1",
		}),
		"philosophy_full":   agentPhilosophyBlock(BackendFullCoder),
		"platform_context":  platformContextBlock(nil, "/tmp/vault"),
		"capabilities_tool": coderCapabilitiesBlock(BackendToolCalling),
		"capabilities_full": coderCapabilitiesBlock(BackendFullCoder),
	}

	for name, out := range prompts {
		if strings.Contains(out, "state.json") {
			t.Errorf("[%s] prompt still mentions state.json (memory now lives in state.md):\n%s", name, out)
		}
	}

	for name := range map[string]string{"runtime_full": "", "runtime_toolcalling": ""} {
		if !strings.Contains(prompts[name], "state.md") {
			t.Errorf("[%s] runtime prompt must positively mention state.md", name)
		}
	}
	if !strings.Contains(prompts["platform_context"], "state.md") {
		t.Errorf("platform context block must positively mention state.md")
	}

	// The [STATE] output marker itself must be untouched by the rename.
	if !strings.Contains(prompts["runtime_full"], "[STATE]") {
		t.Errorf("runtime prompt must still document the [STATE] output marker")
	}
}

func TestImplementationPromptOffersSkills(t *testing.T) {
	p := ImplementationParams{
		BackendType: BackendToolCalling,
		Skills: []SkillRef{
			{Name: "pdf", Description: "Read and extract text from PDF files."},
			{Name: "csv", Description: "Read, filter and aggregate CSV data."},
		},
	}
	out := BuildImplementationPrompt("reader", nil, p)

	require.Contains(t, out, "<available_skills>")
	require.Contains(t, out, "pdf")
	require.Contains(t, out, "Read and extract text from PDF files.")
	require.Contains(t, out, "# Skills:")
}

func TestEditImplementationPromptOffersSkills(t *testing.T) {
	p := ImplementationParams{
		BackendType: BackendToolCalling,
		Skills:      []SkillRef{{Name: "pdf", Description: "Read PDFs."}},
	}
	out := BuildEditImplementationPrompt("reader", nil, p)

	require.Contains(t, out, "<available_skills>")
	require.Contains(t, out, "# Skills:")
}

// With no skills in the pool the block must be omitted entirely rather than rendering
// an empty section that invites the model to invent names.
func TestImplementationPromptOmitsEmptySkillBlock(t *testing.T) {
	out := BuildImplementationPrompt("x", nil, ImplementationParams{})
	require.NotContains(t, out, "<available_skills>")
}

func TestAvailableSkillsGroupedByCategory(t *testing.T) {
	p := ImplementationParams{
		Skills: []SkillRef{
			{Name: "pdf", Description: "Read PDFs.", Category: "File Processing"},
			{Name: "kb-curation", Description: "Write notes.", Category: "Agent Behaviour"},
			{Name: "csv", Description: "Read CSVs.", Category: "File Processing"},
		},
	}
	out := BuildImplementationPrompt("x", nil, p)

	require.Contains(t, out, "Agent Behaviour")
	require.Contains(t, out, "File Processing")

	// Both File Processing skills sit under one heading, not two.
	require.Equal(t, 1, strings.Count(out, "File Processing"))
}

// A skill with no category still appears — it must never be silently dropped.
func TestAvailableSkillsUncategorised(t *testing.T) {
	p := ImplementationParams{
		Skills: []SkillRef{{Name: "loose", Description: "No category set."}},
	}
	out := BuildImplementationPrompt("x", nil, p)
	require.Contains(t, out, "loose")
}

func TestBuildChatTitlePrompt(t *testing.T) {
	sys := BuildChatTitlePrompt()
	for _, want := range []string{"title", "3", "6"} { // mentions a short word-count target
		if !strings.Contains(strings.ToLower(sys), want) {
			t.Errorf("title system prompt missing %q; got:\n%s", want, sys)
		}
	}
	u := ChatTitleUserPrompt("hello there", "general kenobi")
	if !strings.Contains(u, "hello there") || !strings.Contains(u, "general kenobi") {
		t.Errorf("user prompt must include both turns; got:\n%s", u)
	}
	// Truncates very long turns.
	long := strings.Repeat("x", 5000)
	got := ChatTitleUserPrompt(long, long)
	if len(got) > 6000 {
		t.Errorf("user prompt not truncated: %d chars", len(got))
	}
}
