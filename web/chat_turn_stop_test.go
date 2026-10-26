package web

import (
	"net/http"
	"testing"
)

// startBlockedTurn starts a turn whose coder blocks until the turn's context is
// cancelled — the shape of a turn stuck in a search loop.
func startBlockedTurn(t *testing.T, s *Server, workspaceID, chatID string) {
	t.Helper()
	s.testCoderBlock = make(chan struct{}) // never closed: only a cancel ends it
	if _, ok := s.startChatTurn(workspaceID, chatID, "find the code"); !ok {
		t.Fatal("turn did not start")
	}
}

// Stopping must end the turn as STOPPED, not FAILED: the owner asked for it, so
// no red banner, and the transcript gets a reply so the next turn's history
// does not hold two user messages in a row.
func TestStopChatTurnCancelsAndRecordsPlaceholder(t *testing.T) {
	s, workspaceID, chatID := chatTurnFixture(t)
	startBlockedTurn(t, s, workspaceID, chatID)

	if !s.stopChatTurn(chatID) {
		t.Fatal("stopChatTurn reported no turn to stop")
	}
	waitForTurn(t, s, chatID)

	lines, _, err := s.chatTurn(chatID).snapshot()
	if err != nil {
		t.Fatalf("a stopped turn is not a failed one; err = %v", err)
	}
	if len(lines) == 0 || lines[len(lines)-1] != chatTurnStoppedMilestone {
		t.Fatalf("last milestone = %v, want %q", lines, chatTurnStoppedMilestone)
	}
	msgs, _ := s.db.ListChatMessages(chatID)
	if len(msgs) != 2 || msgs[0].Role != "user" || msgs[1].Role != "assistant" || msgs[1].Content != chatTurnStoppedReply {
		t.Fatalf("messages = %+v, want [user, assistant %q]", msgs, chatTurnStoppedReply)
	}
}

func TestStopChatTurnWithNoTurnReportsFalse(t *testing.T) {
	s, _, chatID := chatTurnFixture(t)
	if s.stopChatTurn(chatID) {
		t.Fatal("stopChatTurn reported success with no turn in flight")
	}
}

func TestStopChatTurnTwiceRecordsOnePlaceholder(t *testing.T) {
	s, workspaceID, chatID := chatTurnFixture(t)
	startBlockedTurn(t, s, workspaceID, chatID)
	s.stopChatTurn(chatID)
	s.stopChatTurn(chatID)
	waitForTurn(t, s, chatID)
	if s.stopChatTurn(chatID) {
		t.Fatal("a finished turn must not report as stoppable")
	}
	msgs, _ := s.db.ListChatMessages(chatID)
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want exactly one user + one placeholder", len(msgs))
	}
}

func TestChatTurnCancelEndpoint(t *testing.T) {
	s, cookies, chatID := chatAPIFixture(t)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/chats/"+chatID+"/turn/cancel", nil, cookies)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cancel with no turn: %d, want 404", rec.Code)
	}

	ch, err := s.db.GetChat(chatID)
	if err != nil {
		t.Fatal(err)
	}
	startBlockedTurn(t, s, ch.WorkspaceID, chatID)
	rec = doJSON(t, s, http.MethodPost, "/api/v1/chats/"+chatID+"/turn/cancel", nil, cookies)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("cancel: %d %s, want 204", rec.Code, rec.Body.String())
	}
	waitForTurn(t, s, chatID)

	rec = doJSON(t, s, http.MethodPost, "/api/v1/chats/does-not-exist/turn/cancel", nil, cookies)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cancel of an unknown chat: %d, want 404", rec.Code)
	}
}
