package agentdesigner

import (
	"context"
	"strings"
	"testing"
	"time"

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

// A chat message aimed at a web-owned session must be refused WITHOUT touching
// the session: the whole point of exclusive ownership is that two surfaces
// cannot drive one FSM.
func TestStepRefusesNonOwnerAndLeavesSessionAlone(t *testing.T) {
	flow, workspaceID, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))
	flow.mu.Lock()
	flow.sessions[workspaceID] = &DesignSession{
		AgentName: "price-tracker",
		State:     StateDesigning,
		Origin:    OriginWeb,
		History:   []db.ChatMessage{{Role: "assistant", Content: "hello"}},
	}
	flow.mu.Unlock()

	resp, isDone, agentID, err := flow.Step(context.Background(), workspaceID, "approve", OriginChat)
	if err != nil {
		t.Fatalf("a refusal is a normal answer, not an error: %v", err)
	}
	if isDone || agentID != "" {
		t.Errorf("refused turn must not finish anything: (%v, %q)", isDone, agentID)
	}
	if !strings.Contains(resp, "the web app") {
		t.Errorf("refusal = %q, want it to name the owning surface", resp)
	}
	if !strings.Contains(resp, "/agent cancel") {
		t.Errorf("refusal = %q, want it to name the escape hatch", resp)
	}
	sess := flow.GetSession(workspaceID)
	if sess.State != StateDesigning {
		t.Errorf("state = %v, want it untouched", sess.State)
	}
	if len(sess.History) != 1 {
		t.Errorf("history len = %d, want the refused turn NOT recorded", len(sess.History))
	}
}

// A session created by a test, or by a build predating the Origin field, must
// stay drivable — Owns fails open and this pins that end to end.
func TestStepAllowsZeroOrigin(t *testing.T) {
	flow, workspaceID, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))
	startedSession(t, flow, workspaceID) // no Origin set

	resp, _, _, err := flow.Step(context.Background(), workspaceID, "tell me more", OriginChat)
	if err != nil {
		t.Fatalf("zero-origin session must stay drivable: %v", err)
	}
	if strings.Contains(resp, "please continue there") {
		t.Errorf("zero-origin session was refused: %q", resp)
	}
}

// Starting a second session must say WHERE the first one lives — "you already
// have an active design session" told the user neither where to go nor how out.
func TestStartNamesTheOwningSurface(t *testing.T) {
	flow, workspaceID, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))
	if _, err := flow.Start(workspaceID, "first", OriginWeb); err != nil {
		t.Fatalf("Start: %v", err)
	}

	_, err := flow.Start(workspaceID, "second", OriginChat)
	if err == nil {
		t.Fatal("want a refusal, got nil")
	}
	if !strings.Contains(err.Error(), "the web app") {
		t.Errorf("err = %q, want it to name the web app", err)
	}
	if !strings.Contains(err.Error(), "/agent cancel") {
		t.Errorf("err = %q, want it to name the escape hatch", err)
	}
}

// The bug this whole change exists for: a build started in the web must not be
// announced in Telegram. The completion hook is registered once at wiring time
// and cannot see which surface the user is on, so the origin has to travel WITH
// the result.
func TestBuildCompleteCarriesTheSessionOrigin(t *testing.T) {
	flow, workspaceID, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))
	flow.mu.Lock()
	flow.sessions[workspaceID] = &DesignSession{
		AgentName: "price-tracker",
		State:     StateDesigning,
		Origin:    OriginWeb,
	}
	flow.mu.Unlock()

	got := make(chan Origin, 1)
	flow.OnBuildComplete(func(_ string, origin Origin, _ string, _ bool, _ string, _ error) {
		got <- origin
	})

	if _, _, _, err := flow.startGeneration(workspaceID); err != nil {
		t.Fatalf("startGeneration: %v", err)
	}

	select {
	case origin := <-got:
		if origin != OriginWeb {
			t.Errorf("origin = %q, want %q", origin, OriginWeb)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("build never completed")
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
