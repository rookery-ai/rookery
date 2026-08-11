package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type fakeCaller struct {
	called bool
	tool   string
	args   map[string]any
}

func (f *fakeCaller) CallTool(_ context.Context, _ BoundServer, tool string, args map[string]any) (Result, error) {
	f.called = true
	f.tool = tool
	f.args = args
	return Result{Data: json.RawMessage(`"ok"`)}, nil
}

type fakeParker struct {
	ticket string
	seen   map[string]any
}

func (p *fakeParker) Park(_ context.Context, _ BoundServer, _ string, args map[string]any) (string, error) {
	p.seen = args
	return p.ticket, nil
}

func srvAndTool(readOnly bool, approval string) (BoundServer, Tool) {
	return BoundServer{ID: "s1", Name: "Test", Slug: "test"},
		Tool{
			Name:         "do_thing",
			ToolName:     "mcp__test__do_thing",
			InputSchema:  json.RawMessage(`{"type":"object","properties":{"n":{"type":"number"}},"required":["n"]}`),
			ReadOnly:     readOnly,
			ApprovalMode: approval,
		}
}

func TestBuildPhaseBlocksATooolThatIsNotReadOnly(t *testing.T) {
	s, tool := srvAndTool(false, "auto")
	c := &fakeCaller{}
	_, err := Execute(context.Background(), c, s, tool, map[string]any{"n": 1.0}, Policy{BuildPhase: true})
	if err == nil {
		t.Fatal("expected the build guard to refuse")
	}
	e, ok := err.(*Error)
	if !ok || e.Kind != KindBuildBlocked {
		t.Fatalf("err = %v (%T), want KindBuildBlocked", err, err)
	}
	if c.called {
		t.Fatal("the server was called despite the build guard")
	}
}

func TestBuildPhaseAllowsAReadOnlyTool(t *testing.T) {
	// A build must exercise real READ paths — that is the whole point of testing
	// against live services during generation.
	s, tool := srvAndTool(true, "auto")
	c := &fakeCaller{}
	if _, err := Execute(context.Background(), c, s, tool, map[string]any{"n": 1.0}, Policy{BuildPhase: true}); err != nil {
		t.Fatalf("read-only tool was blocked at build time: %v", err)
	}
	if !c.called {
		t.Fatal("the call never reached the server")
	}
}

func TestBuildGuardReadsTheOwnerColumnNotTheServerHint(t *testing.T) {
	// The MCP spec requires clients to treat annotations as untrusted. A server
	// claiming readOnlyHint:true on a destructive tool must not get a free pass at
	// build time — the owner's correction is what Execute honours. Here the owner
	// has marked it NOT read-only; the tool must still be blocked.
	s, tool := srvAndTool(false, "auto")
	c := &fakeCaller{}
	_, err := Execute(context.Background(), c, s, tool, map[string]any{"n": 1.0}, Policy{BuildPhase: true})
	if err == nil {
		t.Fatal("owner's not-read-only override was ignored; the call was allowed")
	}
	if c.called {
		t.Fatal("the server was called despite the owner marking the tool not read-only")
	}
}

func TestParkedCallIsASuccessNotAnError(t *testing.T) {
	// The coder's tool loop treats any `error:` string as a failing call worth
	// retrying. A queued action is the system working as configured, so it must come
	// back as a successful result — with wording no agent could report as "done".
	s, tool := srvAndTool(false, "approve")
	c := &fakeCaller{}
	p := &fakeParker{ticket: "pa_123"}

	res, err := Execute(context.Background(), c, s, tool, map[string]any{"n": 2.0}, Policy{Parker: p})
	if err != nil {
		t.Fatalf("a parked call must not be an error: %v", err)
	}
	if c.called {
		t.Fatal("the server was called despite the approval gate")
	}
	var pr ParkedResult
	if err := json.Unmarshal(res.Data, &pr); err != nil {
		t.Fatalf("parked payload is not the documented shape: %s", res.Data)
	}
	if pr.ID != "pa_123" || pr.Status != "queued_for_approval" {
		t.Fatalf("parked payload = %+v", pr)
	}
	if !strings.Contains(pr.Note, "NOT") {
		t.Errorf("note could be read as success: %q", pr.Note)
	}
	// The ARGS are what gets replayed on approval hours later, so they must reach
	// the parker intact.
	if pr := p.seen["n"]; pr != 2.0 {
		t.Errorf("parker saw args %v", p.seen)
	}
}

func TestParkerReturningEmptyTicketMeansSendNow(t *testing.T) {
	// ("", nil) is how a mixed set of bindings is honoured: this particular binding
	// is not gated, so the call proceeds.
	s, tool := srvAndTool(false, "approve")
	c := &fakeCaller{}
	p := &fakeParker{ticket: ""}
	if _, err := Execute(context.Background(), c, s, tool, map[string]any{"n": 1.0}, Policy{Parker: p}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !c.called {
		t.Fatal("an ungated binding did not reach the server")
	}
}

func TestAutoModeToolIsNeverParked(t *testing.T) {
	s, tool := srvAndTool(false, "auto")
	c := &fakeCaller{}
	p := &fakeParker{ticket: "pa_should_not_be_used"}
	if _, err := Execute(context.Background(), c, s, tool, map[string]any{"n": 1.0}, Policy{Parker: p}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !c.called {
		t.Fatal("an auto-mode tool was parked")
	}
}

func TestMissingRequiredArgIsRejectedBeforeTheNetwork(t *testing.T) {
	// Validation sits before parking on purpose: never ask a human to approve a call
	// that could not have worked.
	s, tool := srvAndTool(false, "approve")
	c := &fakeCaller{}
	p := &fakeParker{ticket: "pa_1"}
	_, err := Execute(context.Background(), c, s, tool, map[string]any{}, Policy{Parker: p})
	if err == nil {
		t.Fatal("expected a bad-args error")
	}
	e, ok := err.(*Error)
	if !ok || e.Kind != KindBadArgs {
		t.Fatalf("err = %v, want KindBadArgs", err)
	}
	if p.seen != nil {
		t.Fatal("a malformed call was queued for human approval")
	}
	if c.called {
		t.Fatal("a malformed call reached the server")
	}
}

func TestWrongArgTypeIsRejected(t *testing.T) {
	s, tool := srvAndTool(true, "auto")
	c := &fakeCaller{}
	_, err := Execute(context.Background(), c, s, tool, map[string]any{"n": "not a number"}, Policy{})
	if err == nil {
		t.Fatal("expected a type error")
	}
	if !strings.Contains(err.Error(), `"n"`) {
		t.Errorf("error does not name the offending argument: %v", err)
	}
}

func TestUnparseableSchemaDefersToTheServer(t *testing.T) {
	// The server is the authority on its own arguments. A schema we cannot read is
	// not grounds to refuse the call.
	s, tool := srvAndTool(true, "auto")
	tool.InputSchema = json.RawMessage(`not json`)
	c := &fakeCaller{}
	if _, err := Execute(context.Background(), c, s, tool, map[string]any{"anything": 1}, Policy{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !c.called {
		t.Fatal("the call was refused on an unreadable schema")
	}
}

func TestToolDefsNamespaceEveryToolAndCarryTheServerName(t *testing.T) {
	bound := []BoundServer{
		{ID: "a", Name: "Sentry", Slug: "sentry", Tools: []Tool{{Name: "search", ToolName: "mcp__sentry__search", Description: "Find issues"}}},
		{ID: "b", Name: "Linear", Slug: "linear", Tools: []Tool{{Name: "search", ToolName: "mcp__linear__search", Description: "Find tickets"}}},
	}
	defs := ToolDefs(bound)
	if len(defs) != 2 {
		t.Fatalf("got %d defs", len(defs))
	}
	seen := map[string]bool{}
	for _, d := range defs {
		if seen[d.Name] {
			t.Fatalf("duplicate exposed name %q", d.Name)
		}
		seen[d.Name] = true
		// With several servers bound, a bare "Find issues" gives the model no way to
		// choose between two of them.
		if !strings.Contains(d.Description, d.Server.Name) {
			t.Errorf("description %q does not name its server", d.Description)
		}
		if len(d.Params) == 0 {
			t.Errorf("%s has no params schema; a provider rejects that", d.Name)
		}
	}
}

func TestResolveToolRoundTripsToolDefs(t *testing.T) {
	bound := []BoundServer{
		{ID: "a", Name: "Sentry", Slug: "sentry", Tools: []Tool{{Name: "admin.tools.list", ToolName: "mcp__sentry__admin_tools_list"}}},
	}
	for _, d := range ToolDefs(bound) {
		srv, tool, ok := ResolveTool(bound, d.Name)
		if !ok {
			t.Fatalf("ResolveTool(%q) failed", d.Name)
		}
		if srv.ID != "a" || tool.Name != "admin.tools.list" {
			t.Fatalf("resolved to %s/%s", srv.ID, tool.Name)
		}
	}
	if _, _, ok := ResolveTool(bound, "mcp__sentry__nope"); ok {
		t.Error("ResolveTool matched a name that does not exist")
	}
}

func TestToolDefsBoundsAHostileDescription(t *testing.T) {
	// A tool description is untrusted remote text with no connector analogue. The
	// bound is not a defence against a determined injection — the owner chose to
	// trust this server — but it stops one verbose server crowding every other
	// tool's description out of a finite context.
	long := strings.Repeat("x", maxDescriptionLen*3)
	bound := []BoundServer{{ID: "a", Name: "S", Slug: "s", Tools: []Tool{{Name: "t", ToolName: "mcp__s__t", Description: long}}}}
	d := ToolDefs(bound)[0]
	if len(d.Description) > maxDescriptionLen+len("[S] ")+4 {
		t.Fatalf("description not bounded: %d bytes", len(d.Description))
	}
}
