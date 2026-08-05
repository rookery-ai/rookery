package gateway_test

import (
	"context"
	"strings"
	"testing"
)

// The web surface has always rejected a design turn while a build runs
// (handleDesignChat). The chat router did not, so a message sent mid-build
// stepped the FSM concurrently with the build still writing to the same session.
//
// In the reported transcript that is what happened: the user typed "This is
// already built" while a build was live, and the reply came back as though the
// designer had moved on.

// startBuildingSession puts the workspace into a session whose build is in
// flight, using the same signal the guard reads (agentdesigner.IsGenerating).
func TestGuardRejectsATurnWhileABuildIsRunning(t *testing.T) {
	r, database, agentFlow, _ := newTestRouter(t)
	seedAgentDraft(t, database, "hackernews")

	// Open a session, then mark it generating the way a real build does.
	if _, err := agentFlow.Start(testWorkspaceID, "hackernews"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	agentFlow.MarkGeneratingForTest(testWorkspaceID)
	if !agentFlow.IsGenerating(testWorkspaceID) {
		t.Fatal("fixture failed to mark the session as generating")
	}

	send, got := collect()
	if err := r.Handle(context.Background(), testMsg("This is already built"),
		send, func() {}, func(string) {}, func(string) {}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	joined := strings.Join(*got, "\n")
	if !strings.Contains(joined, "Still building") {
		t.Errorf("replies = %q, want the still-building notice", joined)
	}
	// The session must be untouched: the guard returns before Step.
	if !agentFlow.IsGenerating(testWorkspaceID) {
		t.Error("the guard must not disturb the running build")
	}
}

// Without a build in flight the same message must reach the designer as before —
// the guard cannot swallow ordinary turns.
func TestGuardLetsOrdinaryTurnsThrough(t *testing.T) {
	r, _, agentFlow, _ := newTestRouter(t)

	if _, err := agentFlow.Start(testWorkspaceID, "hackernews"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	send, got := collect()
	// This flow has no coder wired, so reaching the designer panics on the nil
	// coder. That panic IS the evidence the guard did not short-circuit —
	// recovering keeps the assertion meaningful without standing up a coder.
	func() {
		defer func() { _ = recover() }()
		_ = r.Handle(context.Background(), testMsg("monitor hacker news"),
			send, func() {}, func(string) {}, func(string) {})
	}()

	if strings.Contains(strings.Join(*got, "\n"), "Still building") {
		t.Errorf("replies = %q, want the turn to reach the designer", *got)
	}
}
