package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rookery-ai/rookery/internal/db"
)

// memStore is an in-memory Store. Sync's contract is about WHICH columns survive a
// re-sync, which is exactly what a fake can assert precisely and a live DB only
// obscures.
type memStore struct {
	tools      map[string]*db.MCPTool // keyed by tool name
	status     string
	lastErr    string
	syncedTTL  int
	syncCalled bool
}

func newMemStore() *memStore { return &memStore{tools: map[string]*db.MCPTool{}} }

func (m *memStore) ListMCPTools(_ context.Context, _ string) ([]*db.MCPTool, error) {
	out := []*db.MCPTool{}
	for _, t := range m.tools {
		cp := *t
		out = append(out, &cp)
	}
	return out, nil
}

func (m *memStore) UpsertMCPTool(_ context.Context, t *db.MCPTool) error {
	if prev, ok := m.tools[t.Name]; ok {
		// Mirror the real ON CONFLICT clause: server-supplied columns are rewritten,
		// the owner's three are not.
		prev.Title = t.Title
		prev.Description = t.Description
		prev.InputSchema = t.InputSchema
		prev.Missing = false
		return nil
	}
	cp := *t
	m.tools[t.Name] = &cp
	return nil
}

func (m *memStore) MarkMCPToolsMissing(_ context.Context, _ string, keep []string) error {
	inKeep := map[string]bool{}
	for _, k := range keep {
		inKeep[k] = true
	}
	for name, t := range m.tools {
		if !inKeep[name] {
			t.Missing = true
		}
	}
	return nil
}

func (m *memStore) SetMCPServerSync(_ context.Context, _ string, ttlMs int, _ string) error {
	m.syncCalled = true
	m.syncedTTL = ttlMs
	m.status = db.MCPStatusActive
	return nil
}

func (m *memStore) SetMCPServerStatus(_ context.Context, _, status, lastErr string) error {
	m.status = status
	m.lastErr = lastErr
	return nil
}

// fakeLister returns a scripted catalog, so a test can change what a server
// advertises between syncs.
type fakeLister struct {
	cat Catalog
	err error
}

func (f *fakeLister) ListTools(context.Context, BoundServer) (Catalog, error) {
	return f.cat, f.err
}

func tool(name string, readOnlyHint bool) DiscoveredTool {
	return DiscoveredTool{Name: name, Description: name, InputSchema: json.RawMessage(`{"type":"object"}`), ReadOnlyHint: readOnlyHint}
}

func TestFirstSyncEnablesTheServersTools(t *testing.T) {
	// The owner is adding this server and reading its tool list in the wizard right
	// now. Making them tick thirty boxes before anything works is friction with no
	// security payoff.
	s := newMemStore()
	l := &fakeLister{cat: Catalog{Tools: []DiscoveredTool{tool("a", true), tool("b", false)}}}

	rep, err := Sync(context.Background(), s, l, BoundServer{ID: "s1", Slug: "srv"})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if rep.Added != 2 || rep.Discovered != 2 {
		t.Fatalf("report = %+v", rep)
	}
	for name, tl := range s.tools {
		if !tl.Enabled {
			t.Errorf("%s arrived disabled on the FIRST sync", name)
		}
	}
	// The hint seeds the owner's column, once.
	if !s.tools["a"].ReadOnly {
		t.Error("readOnlyHint did not seed read_only")
	}
	if s.tools["b"].ReadOnly {
		t.Error("a tool with no hint was seeded read-only")
	}
}

func TestALaterSyncLeavesNewToolsDisabled(t *testing.T) {
	// This asymmetry is the actual control in the whole design: a server cannot grow
	// a live tool between runs. Initial trust is a deliberate act; a tool that turns
	// up three weeks later is not.
	s := newMemStore()
	l := &fakeLister{cat: Catalog{Tools: []DiscoveredTool{tool("a", true)}}}
	if _, err := Sync(context.Background(), s, l, BoundServer{ID: "s1", Slug: "srv"}); err != nil {
		t.Fatalf("first Sync: %v", err)
	}

	l.cat = Catalog{Tools: []DiscoveredTool{tool("a", true), tool("sneaky", true)}}
	if _, err := Sync(context.Background(), s, l, BoundServer{ID: "s1", Slug: "srv"}); err != nil {
		t.Fatalf("second Sync: %v", err)
	}

	if !s.tools["a"].Enabled {
		t.Error("the existing tool lost its enabled state")
	}
	if s.tools["sneaky"].Enabled {
		t.Fatal("a tool that appeared AFTER the first sync was enabled automatically")
	}
}

func TestResyncPreservesOwnerOverrides(t *testing.T) {
	// A re-sync must not reset "this tool needs approval" to auto as a side effect
	// of the server merely restating its catalog.
	s := newMemStore()
	l := &fakeLister{cat: Catalog{Tools: []DiscoveredTool{tool("post", true)}}}
	if _, err := Sync(context.Background(), s, l, BoundServer{ID: "s1", Slug: "srv"}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	// The owner disagrees with the server's readOnlyHint and gates the tool.
	s.tools["post"].ReadOnly = false
	s.tools["post"].ApprovalMode = db.ApprovalModeApprove
	s.tools["post"].Enabled = false

	l.cat = Catalog{Tools: []DiscoveredTool{tool("post", true)}} // server still claims read-only
	if _, err := Sync(context.Background(), s, l, BoundServer{ID: "s1", Slug: "srv"}); err != nil {
		t.Fatalf("re-Sync: %v", err)
	}

	got := s.tools["post"]
	if got.ReadOnly {
		t.Error("the server's hint overwrote the owner's read_only correction")
	}
	if got.ApprovalMode != db.ApprovalModeApprove {
		t.Error("the owner's approval requirement was reset by a re-sync")
	}
	if got.Enabled {
		t.Error("a tool the owner disabled was re-enabled by a re-sync")
	}
}

func TestVanishedToolIsMarkedNotDeleted(t *testing.T) {
	s := newMemStore()
	l := &fakeLister{cat: Catalog{Tools: []DiscoveredTool{tool("a", true), tool("b", true)}}}
	if _, err := Sync(context.Background(), s, l, BoundServer{ID: "s1", Slug: "srv"}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	s.tools["b"].ApprovalMode = db.ApprovalModeApprove

	l.cat = Catalog{Tools: []DiscoveredTool{tool("a", true)}} // b disappeared
	rep, err := Sync(context.Background(), s, l, BoundServer{ID: "s1", Slug: "srv"})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if rep.Missing != 1 {
		t.Errorf("report.Missing = %d, want 1", rep.Missing)
	}
	if s.tools["b"] == nil {
		t.Fatal("the vanished tool row was deleted, discarding the owner's settings")
	}
	if !s.tools["b"].Missing {
		t.Error("the vanished tool was not marked missing")
	}
	if s.tools["b"].ApprovalMode != db.ApprovalModeApprove {
		t.Error("a briefly-absent tool lost the owner's approval requirement")
	}

	// It comes back — the marking must clear, and the settings must still be there.
	l.cat = Catalog{Tools: []DiscoveredTool{tool("a", true), tool("b", true)}}
	if _, err := Sync(context.Background(), s, l, BoundServer{ID: "s1", Slug: "srv"}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if s.tools["b"].Missing {
		t.Error("a returning tool stayed marked missing")
	}
	if s.tools["b"].ApprovalMode != db.ApprovalModeApprove {
		t.Error("settings lost across a disappear/reappear cycle")
	}
}

func TestFirstSyncHoldsBackToolsOverTheCap(t *testing.T) {
	// Tool-list size is a shared budget: a server advertising far too many tools
	// degrades the model's selection across every OTHER tool the agent has. The cap
	// must be visible in the report — a silent truncation reads as "that is all it
	// had".
	s := newMemStore()
	var many []DiscoveredTool
	for i := 0; i < MaxEnabledToolsPerServer+5; i++ {
		many = append(many, tool(string(rune('a'+i%26))+string(rune('a'+i/26)), true))
	}
	l := &fakeLister{cat: Catalog{Tools: many}}

	rep, err := Sync(context.Background(), s, l, BoundServer{ID: "s1", Slug: "srv"})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if rep.HeldBack != 5 {
		t.Fatalf("HeldBack = %d, want 5", rep.HeldBack)
	}
	enabled := 0
	for _, tl := range s.tools {
		if tl.Enabled {
			enabled++
		}
	}
	if enabled != MaxEnabledToolsPerServer {
		t.Fatalf("enabled %d tools, cap is %d", enabled, MaxEnabledToolsPerServer)
	}
}

func TestSyncRecordsTheServersOwnTTL(t *testing.T) {
	s := newMemStore()
	l := &fakeLister{cat: Catalog{Tools: []DiscoveredTool{tool("a", true)}, TTLMs: 300000}}
	if _, err := Sync(context.Background(), s, l, BoundServer{ID: "s1", Slug: "srv"}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if s.syncedTTL != 300000 {
		t.Fatalf("ttl = %d, want the server's own hint", s.syncedTTL)
	}
}

func TestFailedSyncSetsUnreachableNotNeedsAuth(t *testing.T) {
	s := newMemStore()
	l := &fakeLister{err: errf(KindUnreachable, "dial tcp: refused")}
	if _, err := Sync(context.Background(), s, l, BoundServer{ID: "s1", Slug: "srv"}); err == nil {
		t.Fatal("expected the sync to fail")
	}
	if s.status != db.MCPStatusUnreachable {
		t.Fatalf("status = %q, want UNREACHABLE — a dead host must not cost the owner a working server", s.status)
	}
	if s.syncCalled {
		t.Error("a failed sync recorded a successful sync timestamp")
	}
}

func TestRejectedCredentialSetsNeedsAuth(t *testing.T) {
	s := newMemStore()
	l := &fakeLister{err: errf(KindAuth, "401")}
	if _, err := Sync(context.Background(), s, l, BoundServer{ID: "s1", Slug: "srv"}); err == nil {
		t.Fatal("expected the sync to fail")
	}
	if s.status != db.MCPStatusNeedsAuth {
		t.Fatalf("status = %q, want NEEDS_AUTH for a definitive rejection", s.status)
	}
}
