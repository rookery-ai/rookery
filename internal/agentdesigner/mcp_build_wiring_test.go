package agentdesigner

import (
	"os"
	"strings"
	"testing"

	"github.com/ilijad1/rookery/internal/prompts"
)

// TestGenerationWiresMCPTools is a SOURCE-level guard, and it is deliberate.
//
// It exists because a whole class of bug here is invisible to behavioural tests: a
// call that is never made. MCP shipped with parseMCPLine and AutoBindMCPTargets fully
// tested and completely unreachable, because runGeneration never attached MCP tools to
// the build — so the model was never shown any, was never asked for a "# MCP:" header,
// and auto-bind had no used-server list to work from. Every unit test still passed.
// Only the agent-page card actually bound anything.
//
// The same shape already cost this project once with `# Skills:`, which was requested
// during the design conversation but not in the implementation prompts, leaving
// agent_skills empty across an entire install.
//
// A test that drives a real build cannot cover this without a full coder, and a mock
// coder would assert against the mock. Reading the source is the honest check.
func TestGenerationWiresMCPTools(t *testing.T) {
	src, err := os.ReadFile("flow.go")
	if err != nil {
		t.Fatalf("read flow.go: %v", err)
	}
	body := string(src)

	i := strings.Index(body, "generationCoder := coderSvc.WithDir(workDir)")
	if i < 0 {
		t.Fatal("could not find the generation coder setup in flow.go — update this guard")
	}
	j := strings.Index(body[i:], "generationCoder.Generate(")
	if j < 0 {
		t.Fatal("could not find the Generate call in flow.go — update this guard")
	}
	region := body[i : i+j]

	if !strings.Contains(region, "WithMCP(") {
		t.Error("the build never attaches MCP tools: with none attached the model is " +
			"never asked for a '# MCP:' header and auto-bind has nothing to infer from, " +
			"so two of the three binding paths are silently dead")
	}
	if !strings.Contains(region, "MCPBuildBlock(") {
		t.Error("the build prompt does not carry MCPBuildBlock, so nothing instructs the " +
			"model to declare which servers the agent needs")
	}
	// The connector equivalent must stay wired too — this guard is cheap enough to
	// cover both, and they fail the same way.
	if !strings.Contains(region, "WithConnectors(") {
		t.Error("the build no longer attaches connector tools")
	}
}

// TestMCPBuildBlockDemandsTheHeader pins the instruction itself. Attaching the tools
// is only half of it: without the header requirement in the prompt, a model that uses
// MCP during a build still produces an AGENT.md that declares nothing, and binding
// falls back to auto-bind alone.
func TestMCPBuildBlockDemandsTheHeader(t *testing.T) {
	block := prompts.MCPBuildBlock(
		[]prompts.MCPServerRef{{Name: "Home Assistant"}},
		[]string{"mcp__home_assistant__list_states"},
		prompts.BackendToolCalling, "",
	)
	if block == "" {
		t.Fatal("no block produced for a workspace WITH servers")
	}
	for _, want := range []string{"# MCP:", "none", "Home Assistant"} {
		if !strings.Contains(block, want) {
			t.Errorf("build block is missing %q:\n%s", want, block)
		}
	}
}

// TestMCPBuildBlockIsEmptyWithoutServers keeps a workspace that has no MCP servers
// from paying any prompt budget for the feature.
func TestMCPBuildBlockIsEmptyWithoutServers(t *testing.T) {
	if got := prompts.MCPBuildBlock(nil, nil, prompts.BackendToolCalling, ""); got != "" {
		t.Fatalf("expected an empty block, got %q", got)
	}
}
