package agentdesigner

import (
	"testing"

	"github.com/rookery-ai/rookery/internal/db"
)

// Every surface that can create a session must stamp its origin. A session with
// no origin is owned by nobody, which is exactly how a web-started build ended
// up announcing itself in Telegram.

func TestStartStampsChatOrigin(t *testing.T) {
	flow, workspaceID, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))

	if _, err := flow.Start(workspaceID, "price-tracker", OriginChat); err != nil {
		t.Fatalf("Start: %v", err)
	}
	sess := flow.GetSession(workspaceID)
	if sess == nil {
		t.Fatal("no session created")
	}
	if sess.Origin != OriginChat {
		t.Errorf("Origin = %q, want %q", sess.Origin, OriginChat)
	}
}

func TestOfferDraftResumeStampsOrigin(t *testing.T) {
	flow, workspaceID, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))

	draft := &db.AgentDraft{AgentID: "a1", AgentName: "price-tracker"}
	if _, err := flow.OfferDraftResume(workspaceID, "price-tracker", draft, OriginChat); err != nil {
		t.Fatalf("OfferDraftResume: %v", err)
	}
	if got := flow.GetSession(workspaceID).Origin; got != OriginChat {
		t.Errorf("Origin = %q, want %q", got, OriginChat)
	}
}

// Snapshot carries the origin because /design/state is how the SPA learns it is
// a read-only mirror rather than the driver.
func TestSnapshotCarriesOrigin(t *testing.T) {
	flow, workspaceID, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))
	if _, err := flow.Start(workspaceID, "price-tracker", OriginChat); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := flow.Snapshot(workspaceID).Origin; got != OriginChat {
		t.Errorf("Snapshot().Origin = %q, want %q", got, OriginChat)
	}
}
