package agentdesigner

import (
	"testing"
)

// TestAConversationTurnStreamsWithoutLookingLikeABuild is the constraint that
// made this change non-trivial, stated directly.
//
// progressCh used to double as the "a build is running" flag. A conversation
// turn now streams its tool calls over that same channel, so had the two stayed
// fused, opening it for a turn would have made IsGenerating report true — and
// the web layer uses IsGenerating to REJECT concurrent design POSTs. Every turn
// after the first would have been answered with "still building your agent",
// and the session would have been unusable until the draft expired.
func TestAConversationTurnStreamsWithoutLookingLikeABuild(t *testing.T) {
	database, workspaceID := testDB(t)
	f := &Flow{sessions: make(map[string]*DesignSession), db: database}
	f.sessions[workspaceID] = &DesignSession{
		WorkspaceID: workspaceID,
		State:       StateDesigning,
	}

	notify, endTurn := f.beginTurnProgress(workspaceID)
	if notify == nil {
		t.Fatal("a live session must get a progress sink")
	}

	// The channel is open, so the SSE handler can attach and stream.
	if _, ok := f.GetProgressChan(workspaceID); !ok {
		t.Fatal("a conversation turn must open the progress channel, or its tool " +
			"calls have nowhere to go and the user watches a silent spinner")
	}
	// ...and yet no build is running.
	if f.IsGenerating(workspaceID) {
		t.Fatal("a conversation turn reported itself as a BUILD. The web layer " +
			"rejects design POSTs while IsGenerating is true, so every following " +
			"turn would answer 'still building your agent'")
	}

	notify("🔧 search_files(dentist)")
	if got := f.Snapshot(workspaceID).LastProgress; got != "🔧 search_files(dentist)" {
		t.Errorf("LastProgress = %q, want the milestone the turn emitted", got)
	}
	if f.Snapshot(workspaceID).Generating {
		t.Error("Snapshot reported Generating for a conversation turn")
	}

	endTurn()
	if _, ok := f.GetProgressChan(workspaceID); ok {
		t.Error("the turn's progress channel outlived the turn; the next turn would " +
			"attach to a stream nothing writes to")
	}
}

// TestATurnDoesNotCloseTheChannelABuildHasAdoptedIt covers the ordering a user
// produces by typing a message and then pressing Build.
//
// startGeneration ADOPTS an already-open channel rather than creating a second
// one, so a turn that closed unconditionally would cut the build's own progress
// stream the instant the preceding turn returned — the build would run to
// completion with the UI showing nothing.
func TestATurnDoesNotCloseTheChannelABuildHasAdoptedIt(t *testing.T) {
	database, workspaceID := testDB(t)
	f := &Flow{sessions: make(map[string]*DesignSession), db: database}
	f.sessions[workspaceID] = &DesignSession{
		WorkspaceID: workspaceID,
		State:       StateDesigning,
	}

	_, endTurn := f.beginTurnProgress(workspaceID)

	// A build starts and takes over the channel the turn opened.
	f.mu.Lock()
	f.sessions[workspaceID].generating = true
	f.mu.Unlock()

	endTurn()

	if _, ok := f.GetProgressChan(workspaceID); !ok {
		t.Fatal("the turn closed the channel the build had adopted, so the build " +
			"streams into nothing and the UI shows a frozen spinner for minutes")
	}
	if !f.IsGenerating(workspaceID) {
		t.Error("the build's generating flag was cleared by the turn ending")
	}
}

// TestATurnWithNoSessionIsHarmless: Step refuses a turn with no session, but
// beginTurnProgress runs inside callCoder and must not panic or hand back a sink
// that writes to a channel nobody owns.
func TestATurnWithNoSessionIsHarmless(t *testing.T) {
	database, _ := testDB(t)
	f := &Flow{sessions: make(map[string]*DesignSession), db: database}

	notify, endTurn := f.beginTurnProgress("nobody")
	if notify != nil {
		t.Error("no session means no progress sink")
	}
	endTurn() // must not panic
}
