package agentdesigner

import (
	"context"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/db"
)

// TestCallCoderStripsTheSpecForTheUserAndKeepsItInHistory pins the asymmetry the
// whole design rests on.
//
// The RETENTION half is the one that matters. Stripping before storage is the
// tempting simplification, and it would silently re-break the implementation
// prompt — which refers to "the design's [TECHNICAL SPEC]" by name and, before
// this change, had never once received one, because the prompt asked for the
// block "after the user approves" and approval routes straight to
// startGeneration without another coder turn.
func TestCallCoderStripsTheSpecForTheUserAndKeepsItInHistory(t *testing.T) {
	// The fake prints a proposal with the block appended, exactly as the design
	// prompt now instructs.
	fake := newFakeCoder(t, `print("""Here's the plan.

- Check the page each morning
- Message you when something changes

Type approve and I'll build it.

[TECHNICAL SPEC]
Tier: 1
Schedule: 0 8 * * *
[/TECHNICAL SPEC]""")
`)
	flow, workspaceID, _ := newGenFlow(t, fake)
	flow.sessions[workspaceID] = &DesignSession{
		WorkspaceID: workspaceID,
		AgentName:   "watcher",
		State:       StateDesigning,
	}

	shown, err := flow.callCoder(context.Background(), workspaceID, "watch this page")
	if err != nil {
		t.Fatalf("callCoder: %v", err)
	}

	if strings.Contains(shown, "TECHNICAL SPEC") || strings.Contains(shown, "Tier: 1") {
		t.Errorf("the user was shown the machine-facing spec block:\n%s", shown)
	}
	if !strings.Contains(shown, "Check the page each morning") {
		t.Errorf("stripping ate the plan prose:\n%s", shown)
	}

	hist := flow.sessions[workspaceID].History
	if len(hist) != 2 {
		t.Fatalf("history = %d turns, want 2", len(hist))
	}
	if !strings.Contains(hist[1].Content, "[TECHNICAL SPEC]") {
		t.Fatalf("History lost the spec block — the code generator reads it from here:\n%s", hist[1].Content)
	}

	snap := flow.Snapshot(workspaceID)
	if !snap.PlanReady {
		t.Error("Snapshot.PlanReady = false after a proposal turn carrying a spec block")
	}
	if !strings.Contains(snap.PendingSpec, "Tier: 1") {
		t.Errorf("Snapshot.PendingSpec = %q, want the block body", snap.PendingSpec)
	}
	// And the replay path the browser actually reads must agree with the live
	// turn about what the user was shown.
	if strings.Contains(StripTechnicalSpec(hist[1].Content), "TECHNICAL SPEC") {
		t.Error("StripTechnicalSpec left the marker in the replayed turn")
	}
}

// TestSnapshotPlanNotReadyDuringQuestions is the reported bug, at the layer that
// now decides it: a clarifying question must not arm the build button.
func TestSnapshotPlanNotReadyDuringQuestions(t *testing.T) {
	flow, workspaceID, _ := newGenFlow(t, nil)
	flow.sessions[workspaceID] = &DesignSession{
		WorkspaceID: workspaceID,
		AgentName:   "watcher",
		State:       StateDesigning,
		History: []db.ChatMessage{
			{Role: "user", Content: "watch a page for me"},
			{Role: "assistant", Content: "Sure — which page, and how often should I check it?"},
		},
	}
	if snap := flow.Snapshot(workspaceID); snap.PlanReady {
		t.Fatal("PlanReady = true while the designer is still asking questions")
	}
}
