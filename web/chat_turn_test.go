package web

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rookery-ai/rookery/internal/db"
)

// chatTurnFixture builds a server with one workspace and one empty chat.
func chatTurnFixture(t *testing.T) (*Server, string, string) {
	t.Helper()
	s, database := newAPITestServer(t)

	workspaceID := uuid.NewString()
	if err := database.CreateWorkspace(&db.Workspace{ID: workspaceID, Name: "ws"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	chatID := uuid.NewString()
	if err := database.CreateChat(&db.Chat{
		ID: chatID, WorkspaceID: workspaceID, Name: "chat", Platform: "web", Active: true,
	}); err != nil {
		t.Fatalf("create chat: %v", err)
	}
	return s, workspaceID, chatID
}

// waitForTurn blocks until the tracked turn reports done.
func waitForTurn(t *testing.T, s *Server, chatID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := s.chatTurn(chatID); st != nil {
			if _, done, _ := st.snapshot(); done {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("turn never finished")
}

// The whole point of the change. The owner's message must be durable BEFORE the
// coder runs, so abandoning the page cannot destroy it. Asserting the ORDERING
// rather than the end state is deliberate: both orderings reach the same end
// state, and it was the ordering that lost people's messages.
func TestChatTurnPersistsUserMessageBeforeCallingTheCoder(t *testing.T) {
	s, workspaceID, chatID := chatTurnFixture(t)

	seen := make(chan int, 1)
	s.testCoderHook = func() {
		msgs, _ := s.db.ListChatMessages(chatID)
		seen <- len(msgs)
	}
	s.testCoderErr = "stop here"

	if _, ok := s.startChatTurn(workspaceID, chatID, "hello"); !ok {
		t.Fatal("startChatTurn refused a first turn")
	}

	select {
	case n := <-seen:
		if n != 1 {
			t.Errorf("coder saw %d persisted message(s), want 1 (the owner's)", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("coder was never reached")
	}
}

// history is read via ListChatMessages, so persisting before reading would feed
// this turn's own text twice — once as a prior turn, once as the message.
func TestChatTurnDoesNotFeedTheNewMessageAsHistory(t *testing.T) {
	s, workspaceID, chatID := chatTurnFixture(t)

	got := make(chan int, 1)
	s.testHistoryHook = func(n int) { got <- n }
	s.testCoderErr = "stop here"

	s.startChatTurn(workspaceID, chatID, "hello")

	select {
	case n := <-got:
		if n != 0 {
			t.Errorf("history carried %d message(s) on a fresh chat, want 0", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("history hook never fired")
	}
}

// One turn per chat, for the same reason startManualRun refuses a double run: a
// double-send must not point two coders at one conversation.
func TestChatTurnRefusesASecondConcurrentTurn(t *testing.T) {
	s, workspaceID, chatID := chatTurnFixture(t)
	s.testCoderBlock = make(chan struct{})
	// Set alongside the block so releasing it returns immediately rather than
	// falling through into real coder construction, which a unit server has not
	// configured.
	s.testCoderErr = "stop here"

	if _, ok := s.startChatTurn(workspaceID, chatID, "first"); !ok {
		t.Fatal("first turn refused")
	}
	// Wait until the first turn is actually inside the coder call, so the
	// refusal under test is the concurrency guard and not a scheduling race.
	time.Sleep(50 * time.Millisecond)

	if _, ok := s.startChatTurn(workspaceID, chatID, "second"); ok {
		t.Error("a second concurrent turn on the same chat was accepted")
	}
	close(s.testCoderBlock)
}

// A failed turn KEEPS the owner's message. Not persisting on failure was
// defensible while the browser held the bubble in memory; now that the message
// is durable, deleting it would be actively worse — they typed it, and it is
// the context for the retry.
func TestFailedChatTurnKeepsTheUserMessage(t *testing.T) {
	s, workspaceID, chatID := chatTurnFixture(t)
	s.testCoderErr = "provider exploded"

	if _, ok := s.startChatTurn(workspaceID, chatID, "hello"); !ok {
		t.Fatal("turn refused")
	}
	waitForTurn(t, s, chatID)

	msgs, _ := s.db.ListChatMessages(chatID)
	if len(msgs) != 1 {
		t.Fatalf("want exactly the user message, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "user" || msgs[0].Content != "hello" {
		t.Errorf("user message not preserved through failure: %+v", msgs[0])
	}

	st := s.chatTurn(chatID)
	_, _, err := st.snapshot()
	if err == nil || !strings.Contains(err.Error(), "provider exploded") {
		t.Errorf("failure not recorded on the turn: %v", err)
	}
}

// The failure also reaches the live stream, so a watching client sees why the
// card stopped rather than a spinner that simply ends.
func TestFailedChatTurnReportsOnTheProgressStream(t *testing.T) {
	s, workspaceID, chatID := chatTurnFixture(t)
	s.testCoderErr = "provider exploded"

	s.startChatTurn(workspaceID, chatID, "hello")
	waitForTurn(t, s, chatID)

	lines, _, _ := s.chatTurn(chatID).snapshot()
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "provider exploded") {
		t.Errorf("failure absent from the progress lines: %q", joined)
	}
}

// A finished turn releases the chat, so the next message starts a new one.
func TestChatTurnAllowsANewTurnOnceTheLastFinished(t *testing.T) {
	s, workspaceID, chatID := chatTurnFixture(t)
	s.testCoderErr = "stop here"

	s.startChatTurn(workspaceID, chatID, "first")
	waitForTurn(t, s, chatID)

	if _, ok := s.startChatTurn(workspaceID, chatID, "second"); !ok {
		t.Error("a new turn was refused after the previous one finished")
	}
}

// isChatTurnLive drives whether a client attaches, so it must not report true
// for a turn that has already finished — that would open a stream with no
// producer, which immediately closes and reads as a dropped connection.
func TestIsChatTurnLiveIsFalseOnceDone(t *testing.T) {
	s, workspaceID, chatID := chatTurnFixture(t)
	s.testCoderErr = "stop here"

	s.startChatTurn(workspaceID, chatID, "hello")
	if !s.isChatTurnLive(chatID) && s.chatTurn(chatID) == nil {
		t.Fatal("turn was never registered")
	}
	waitForTurn(t, s, chatID)

	if s.isChatTurnLive(chatID) {
		t.Error("isChatTurnLive still true after the turn finished")
	}
}
