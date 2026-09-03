package web

import (
	"strings"
	"testing"
	"time"
)

// withFastSlowNotice shortens the quiet-turn delay for the duration of a test.
// The behaviour under test is "a quiet turn eventually explains itself", not the
// exact interval, and waiting the real twenty seconds would cost that on every
// CI run.
func withFastSlowNotice(t *testing.T, d time.Duration) {
	t.Helper()
	prev := chatSlowTurnAfter
	chatSlowTurnAfter = d
	t.Cleanup(func() { chatSlowTurnAfter = prev })
}

// linesOf returns the milestones recorded for a chat's tracked turn.
func linesOf(t *testing.T, s *Server, chatID string) []string {
	t.Helper()
	st := s.chatTurn(chatID)
	if st == nil {
		t.Fatal("no tracked turn")
	}
	lines, _, _ := st.snapshot()
	return lines
}

func containsSubstring(lines []string, want string) bool {
	for _, l := range lines {
		if strings.Contains(l, want) {
			return true
		}
	}
	return false
}

// A turn that has said nothing must eventually say why it is quiet.
//
// This is the half of the fix that the fail-fast classification does not cover:
// a coder that IS reachable and merely slow. On a self-hosted local model that
// is the commoner case — loading weights on the first request of the day takes
// far longer than any hosted round-trip — and from the browser it looked
// identical to a dead one, because chat emitted no milestone until its first
// tool call and a conversational turn makes none at all.
func TestAQuietTurnSaysItIsStillWaiting(t *testing.T) {
	s, workspaceID, chatID := chatTurnFixture(t)
	withFastSlowNotice(t, 20*time.Millisecond)

	// Block inside the coder so the turn stays genuinely in flight while the
	// notice timer runs.
	s.testCoderBlock = make(chan struct{})
	s.testCoderErr = "stop here"

	if _, ok := s.startChatTurn(workspaceID, chatID, "hello"); !ok {
		t.Fatal("startChatTurn refused")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if containsSubstring(linesOf(t, s, chatID), "Still waiting") {
			close(s.testCoderBlock)
			waitForTurn(t, s, chatID)
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	close(s.testCoderBlock)
	t.Fatalf("no quiet-turn notice was emitted; lines = %q", linesOf(t, s, chatID))
}

// A turn that finishes promptly must NOT be annotated after the fact. The timer
// is stopped on the way out, so a fast turn's transcript is exactly what it was
// before this change — otherwise every short conversational exchange would
// accumulate filler nobody needs to read.
func TestAFastTurnGetsNoQuietNotice(t *testing.T) {
	s, workspaceID, chatID := chatTurnFixture(t)
	withFastSlowNotice(t, 300*time.Millisecond)

	s.testCoderErr = "stop here"
	if _, ok := s.startChatTurn(workspaceID, chatID, "hello"); !ok {
		t.Fatal("startChatTurn refused")
	}
	waitForTurn(t, s, chatID)

	// Wait past the notice interval to prove the timer really was stopped
	// rather than merely not having fired yet when the turn ended.
	time.Sleep(500 * time.Millisecond)

	if lines := linesOf(t, s, chatID); containsSubstring(lines, "Still waiting") {
		t.Errorf("a fast turn was annotated as slow; lines = %q", lines)
	}
}
