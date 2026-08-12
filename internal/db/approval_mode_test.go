package db_test

import (
	"context"
	"testing"

	"github.com/rookery-ai/rookery/internal/db"
)

// approvalFixture builds a workspace + agent bound to n service connections named
// c1..cn, mirroring connectors_test.go's fixture style.
func approvalFixture(t *testing.T, n int) (*db.DB, context.Context, string, []string) {
	t.Helper()
	d, agentID, ws := connTestDB(t)
	ctx := context.Background()
	ids := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		id := string(rune('a'+i-1)) + "-conn"
		if err := d.InsertServiceConnection(ctx, db.ServiceConnection{
			ID: id, WorkspaceID: ws, Provider: "google", AccountLabel: id,
		}); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := d.SetAgentConnections(ctx, agentID, ids); err != nil {
		t.Fatal(err)
	}
	return d, ctx, agentID, ids
}

func TestApprovalModeDefaultsToAuto(t *testing.T) {
	d, ctx, agentID, ids := approvalFixture(t, 1)

	mode, err := d.AgentConnectionApprovalMode(ctx, agentID, ids[0])
	if err != nil {
		t.Fatalf("AgentConnectionApprovalMode: %v", err)
	}
	if mode != db.ApprovalModeAuto {
		t.Errorf("new binding mode = %q, want %q — the gate must be opt-in", mode, db.ApprovalModeAuto)
	}
}

// An agent not bound to a connection reports auto rather than erroring: the caller
// is deciding whether to gate, and "not bound" is not a gate.
func TestApprovalModeUnboundIsAuto(t *testing.T) {
	d, ctx, agentID, _ := approvalFixture(t, 1)

	mode, err := d.AgentConnectionApprovalMode(ctx, agentID, "no-such-connection")
	if err != nil {
		t.Fatalf("unbound lookup should not error: %v", err)
	}
	if mode != db.ApprovalModeAuto {
		t.Errorf("unbound mode = %q, want %q", mode, db.ApprovalModeAuto)
	}
}

func TestSetApprovalModeRoundTrips(t *testing.T) {
	d, ctx, agentID, ids := approvalFixture(t, 1)

	if err := d.SetAgentConnectionApprovalMode(ctx, agentID, ids[0], db.ApprovalModeApprove); err != nil {
		t.Fatalf("SetAgentConnectionApprovalMode: %v", err)
	}
	mode, err := d.AgentConnectionApprovalMode(ctx, agentID, ids[0])
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if mode != db.ApprovalModeApprove {
		t.Errorf("mode = %q, want %q", mode, db.ApprovalModeApprove)
	}
}

// An unknown mode must be rejected, never stored. A stored "aprove" typo would be
// read as "not approve" at execution time and silently publish.
func TestSetApprovalModeRejectsUnknownValue(t *testing.T) {
	d, ctx, agentID, ids := approvalFixture(t, 1)

	if err := d.SetAgentConnectionApprovalMode(ctx, agentID, ids[0], "sometimes"); err == nil {
		t.Fatal("an unknown approval mode must be rejected")
	}
	mode, _ := d.AgentConnectionApprovalMode(ctx, agentID, ids[0])
	if mode != db.ApprovalModeAuto {
		t.Errorf("rejected write must not change the stored mode, got %q", mode)
	}
}

// The designer's auto-bind and the agent page's checkbox card both call
// SetAgentConnections on every save. A naive delete-then-insert would reset a
// deliberate "require approval" back to auto as a side effect of an unrelated edit.
func TestSetAgentConnectionsPreservesApprovalMode(t *testing.T) {
	d, ctx, agentID, ids := approvalFixture(t, 2)
	keep, other := ids[0], ids[1]

	if err := d.SetAgentConnectionApprovalMode(ctx, agentID, keep, db.ApprovalModeApprove); err != nil {
		t.Fatalf("set mode: %v", err)
	}

	// Re-save the same bindings, as an unrelated agent edit would.
	if err := d.SetAgentConnections(ctx, agentID, []string{keep, other}); err != nil {
		t.Fatalf("SetAgentConnections: %v", err)
	}

	mode, err := d.AgentConnectionApprovalMode(ctx, agentID, keep)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if mode != db.ApprovalModeApprove {
		t.Errorf("approval mode was reset by an unrelated re-save: got %q, want %q",
			mode, db.ApprovalModeApprove)
	}

	if m, _ := d.AgentConnectionApprovalMode(ctx, agentID, other); m != db.ApprovalModeAuto {
		t.Errorf("other binding mode = %q, want %q", m, db.ApprovalModeAuto)
	}
}

// Unbinding then rebinding is a deliberate act, not an incidental re-save — the
// mode is not resurrected from a binding the user removed.
func TestApprovalModeNotResurrectedAfterUnbind(t *testing.T) {
	d, ctx, agentID, ids := approvalFixture(t, 2)
	gated := ids[0]

	if err := d.SetAgentConnectionApprovalMode(ctx, agentID, gated, db.ApprovalModeApprove); err != nil {
		t.Fatalf("set mode: %v", err)
	}
	if err := d.SetAgentConnections(ctx, agentID, []string{ids[1]}); err != nil {
		t.Fatalf("unbind: %v", err)
	}
	if err := d.SetAgentConnections(ctx, agentID, []string{ids[1], gated}); err != nil {
		t.Fatalf("rebind: %v", err)
	}

	if m, _ := d.AgentConnectionApprovalMode(ctx, agentID, gated); m != db.ApprovalModeAuto {
		t.Errorf("rebound connection mode = %q, want %q (a removed binding's gate is gone)",
			m, db.ApprovalModeAuto)
	}
}
