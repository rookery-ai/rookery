package web

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

var errTurnFailedForTest = errors.New("invalid model name")

// A client that watched a turn fail must be told it FAILED.
//
// This is the bug behind "I type a message, I see 'Contacting …', the card
// disappears, and I get no message at all". The live loop emitted `event: done`
// unconditionally when the channel closed, so the browser ran its success path
// and set no banner. Only the attach-AFTER-done branch distinguished the two.
//
// It is the common path, not an edge case: the browser opens this stream right
// after the 202, so it is attached for essentially every turn and the
// attach-after-done branch is the rare one. That is why the failure reached a
// real user on every attempt while the tests covering the other branch passed.
func TestALiveClientIsToldWhenTheTurnFailed(t *testing.T) {
	s, cookies, chatID := chatAPIFixture(t)

	// A turn that is still running when the client attaches, so the handler
	// takes the LIVE loop rather than the attach-after-done early return.
	st := &chatTurnState{id: "t1", progressCh: make(chan string, 8)}
	s.chatTurnsMu.Lock()
	s.chatTurns[chatID] = st
	s.chatTurnsMu.Unlock()

	// Finish it as a FAILURE while the request is in flight, in the same order
	// startChatTurn uses: record the milestone and the error, then close.
	go func() {
		time.Sleep(50 * time.Millisecond)
		st.mu.Lock()
		st.lines = append(st.lines, "⚠️ invalid model name")
		st.progressCh <- "⚠️ invalid model name"
		st.done = true
		st.err = errTurnFailedForTest
		st.closed = true
		close(st.progressCh)
		st.mu.Unlock()
	}()

	rec := doJSON(t, s, http.MethodGet, "/api/v1/chats/"+chatID+"/turn/progress", nil, cookies)
	body := rec.Body.String()

	if !strings.Contains(body, "event: error") {
		t.Errorf("a failed turn reported success to a live client; the browser sets no banner. body = %q", body)
	}
	if strings.Contains(body, "event: done") {
		t.Errorf("a failed turn also emitted done: %q", body)
	}
	if !strings.Contains(body, "invalid model name") {
		t.Errorf("the reason never reached the client: %q", body)
	}
}

// The success path must keep reporting done, or every healthy turn would raise
// a banner.
func TestALiveClientIsToldWhenTheTurnSucceeded(t *testing.T) {
	s, cookies, chatID := chatAPIFixture(t)

	st := &chatTurnState{id: "t1", progressCh: make(chan string, 8)}
	s.chatTurnsMu.Lock()
	s.chatTurns[chatID] = st
	s.chatTurnsMu.Unlock()

	go func() {
		time.Sleep(50 * time.Millisecond)
		st.mu.Lock()
		st.done = true
		st.closed = true
		close(st.progressCh)
		st.mu.Unlock()
	}()

	rec := doJSON(t, s, http.MethodGet, "/api/v1/chats/"+chatID+"/turn/progress", nil, cookies)
	body := rec.Body.String()

	if !strings.Contains(body, "event: done") {
		t.Errorf("a successful turn did not report done: %q", body)
	}
	if strings.Contains(body, "event: error") {
		t.Errorf("a successful turn reported an error: %q", body)
	}
}
