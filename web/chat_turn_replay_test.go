package web

import (
	"testing"
	"time"
)

// A client attaching mid-turn must see each milestone ONCE.
//
// The SSE handler replays st.lines and then follows st.progressCh — but nothing
// has consumed the channel, so every line emitted before the client attached is
// still sitting in its buffer and arrives a second time. Latent until a turn
// reliably emitted something before the client could attach, which the opening
// "💭 Contacting <model>…" milestone now does.
func TestAttachingDoesNotReplayAMilestoneTwice(t *testing.T) {
	s, workspaceID, chatID := chatTurnFixture(t)

	// Block inside the coder so the turn is genuinely mid-flight with lines
	// already emitted, which is the state a client attaches into.
	s.testCoderBlock = make(chan struct{})
	s.testCoderErr = "stop here"

	if _, ok := s.startChatTurn(workspaceID, chatID, "hello"); !ok {
		t.Fatal("startChatTurn refused")
	}
	st := s.chatTurn(chatID)
	if st == nil {
		t.Fatal("no tracked turn")
	}

	// Emit two milestones before anyone is listening.
	st.mu.Lock()
	st.lines = append(st.lines, "🔧 one")
	select {
	case st.progressCh <- "🔧 one":
	default:
	}
	st.lines = append(st.lines, "🔧 two")
	select {
	case st.progressCh <- "🔧 two":
	default:
	}
	st.mu.Unlock()

	// Attach exactly as the SSE handler does.
	backlog, done, _ := st.attach()
	if done {
		t.Fatal("turn finished early")
	}
	if len(backlog) != 2 {
		t.Fatalf("backlog = %v, want the two emitted milestones", backlog)
	}

	// Nothing may remain queued: those messages are already in the backlog the
	// caller just replayed.
	select {
	case msg := <-st.progressCh:
		t.Fatalf("milestone %q is still queued after being replayed; the client would see it twice", msg)
	case <-time.After(50 * time.Millisecond):
	}

	close(s.testCoderBlock)
	waitForTurn(t, s, chatID)
}

// The drain must terminate on a state whose channel is CLOSED but whose
// `closed` flag was never set.
//
// This is the shape that actually broke: an existing SSE test builds a finished
// turn by hand — close(progressCh), done = true — and predates the flag, so it
// has no reason to set it. A drain that trusted the flag spun forever holding
// the lock and hung the whole package.
//
// Going through startChatTurn (as the sibling test below does) cannot catch it,
// because that path always sets both together. The hazard lives precisely where
// the two facts disagree, so the state is assembled here the same way the SSE
// test assembles it.
func TestAttachingTerminatesWhenTheFlagAndTheChannelDisagree(t *testing.T) {
	st := &chatTurnState{id: "t1", progressCh: make(chan string)}
	close(st.progressCh)
	st.done = true // note: st.closed deliberately NOT set

	returned := make(chan struct{})
	go func() {
		_, _, _ = st.attach()
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("attach spun on a closed channel whose closed flag was never set")
	}
}

// Draining must not spin on a turn that has already finished.
func TestAttachingToAFinishedTurnReturns(t *testing.T) {
	s, workspaceID, chatID := chatTurnFixture(t)
	s.testCoderErr = "stop here"

	if _, ok := s.startChatTurn(workspaceID, chatID, "hello"); !ok {
		t.Fatal("startChatTurn refused")
	}
	waitForTurn(t, s, chatID)

	doneCh := make(chan struct{})
	go func() {
		st := s.chatTurn(chatID)
		_, _, _ = st.attach()
		close(doneCh)
	}()
	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("attach did not return on a finished turn — the drain is spinning on a closed channel")
	}
}
