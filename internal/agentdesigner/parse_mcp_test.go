package agentdesigner

import (
	"testing"

	"github.com/ilijad1/rookery/internal/db"
)

func mcpAvail() []*db.MCPServer {
	return []*db.MCPServer{
		{ID: "s1", Name: "Home Assistant", Slug: "home_assistant"},
		{ID: "s2", Name: "Paperless", Slug: "paperless"},
	}
}

func TestParseMCPLineInline(t *testing.T) {
	ids := parseMCPLine("# MCP: Home Assistant, Paperless\n\nBody", mcpAvail())
	if len(ids) != 2 {
		t.Fatalf("ids = %v", ids)
	}
}

func TestParseMCPLineBulletForm(t *testing.T) {
	// The shape a weak model actually produces.
	md := "# MCP servers:\n# - Home Assistant\n# - Paperless\n\nBody"
	ids := parseMCPLine(md, mcpAvail())
	if len(ids) != 2 {
		t.Fatalf("ids = %v", ids)
	}
}

func TestParseMCPLineToleratesTrailingProse(t *testing.T) {
	ids := parseMCPLine("# MCP: Home Assistant (for the lights)\n", mcpAvail())
	if len(ids) != 1 || ids[0] != "s1" {
		t.Fatalf("ids = %v", ids)
	}
}

func TestMissingHeaderIsNilButNoneIsEmpty(t *testing.T) {
	// This distinction drives auto-bind: nil means "the model forgot, fall back to
	// what the build used", while an empty non-nil slice means "the model said none"
	// and must be honoured. Collapsing them would either ignore an explicit none or
	// bind servers to an agent that declared it wanted none.
	if got := parseMCPLine("no header at all", mcpAvail()); got != nil {
		t.Fatalf("absent header returned %v, want nil", got)
	}
	got := parseMCPLine("# MCP: none\n", mcpAvail())
	if got == nil {
		t.Fatal("an explicit 'none' returned nil, which reads as 'no header'")
	}
	if len(got) != 0 {
		t.Fatalf("'none' bound %v", got)
	}
}

func TestAutoBindExplicitHeaderWins(t *testing.T) {
	ids, apply := AutoBindMCPTargets("# MCP: Paperless\n", mcpAvail(), []string{"s1"}, []string{"s1"})
	if !apply || len(ids) != 1 || ids[0] != "s2" {
		t.Fatalf("ids=%v apply=%v", ids, apply)
	}
}

func TestAutoBindFallsBackToWhatTheBuildUsed(t *testing.T) {
	ids, apply := AutoBindMCPTargets("no header", mcpAvail(), nil, []string{"s2"})
	if !apply || len(ids) != 1 || ids[0] != "s2" {
		t.Fatalf("ids=%v apply=%v", ids, apply)
	}
}

func TestAutoBindNeverClobbersAnExistingBinding(t *testing.T) {
	// The owner may have set this by hand on the agent page. A rebuild with no header
	// must not silently discard that.
	ids, apply := AutoBindMCPTargets("no header", mcpAvail(), []string{"s1"}, []string{"s2"})
	if apply {
		t.Fatalf("rebuild overwrote a hand-set binding with %v", ids)
	}
}

func TestAutoBindIgnoresUnknownServerIDs(t *testing.T) {
	ids, apply := AutoBindMCPTargets("no header", mcpAvail(), nil, []string{"deleted-server"})
	if apply {
		t.Fatalf("bound an id that is not in this workspace: %v", ids)
	}
}
