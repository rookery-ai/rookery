package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// chatAPIFixture returns a server with an authenticated session, an entered
// workspace, and one chat — everything the guarded /api/v1/chats routes need.
func chatAPIFixture(t *testing.T) (*Server, []*http.Cookie, string) {
	t.Helper()
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/chats", map[string]string{"name": "chat"}, cookies)
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("create chat: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("decode created chat: %v (%s)", err, rec.Body.String())
	}
	return s, cookies, created.ID
}

// A client attaching MID-turn must receive the milestones it missed. Following
// only the live channel would show it an empty card on a busy turn, which is
// indistinguishable from nothing happening — the impression this whole change
// exists to remove.
func TestChatTurnProgressReplaysMissedMilestones(t *testing.T) {
	s, cookies, chatID := chatAPIFixture(t)

	st := &chatTurnState{
		id:         "t1",
		progressCh: make(chan string, 8),
		lines:      []string{"🔧 read_file(notes.md)", "🔧 search_files(dentist)"},
	}
	s.chatTurnsMu.Lock()
	s.chatTurns[chatID] = st
	s.chatTurnsMu.Unlock()

	go func() {
		time.Sleep(30 * time.Millisecond)
		close(st.progressCh)
	}()

	rec := doJSON(t, s, http.MethodGet, "/api/v1/chats/"+chatID+"/turn/progress", nil, cookies)
	body := rec.Body.String()

	for _, want := range []string{"read_file(notes.md)", "search_files(dentist)"} {
		if !strings.Contains(body, want) {
			t.Errorf("missed milestone %q was not replayed: %q", want, body)
		}
	}
	if !strings.Contains(body, "event: done") {
		t.Errorf("stream did not close with event: done: %q", body)
	}
}

// The done event must carry non-empty data or the browser never dispatches it,
// leaving the client to infer completion from a reconnect hitting a 404.
func TestChatTurnProgressDoneEventCarriesData(t *testing.T) {
	s, cookies, chatID := chatAPIFixture(t)

	st := &chatTurnState{id: "t1", progressCh: make(chan string)}
	s.chatTurnsMu.Lock()
	s.chatTurns[chatID] = st
	s.chatTurnsMu.Unlock()
	close(st.progressCh)
	st.mu.Lock()
	st.done = true
	st.mu.Unlock()

	rec := doJSON(t, s, http.MethodGet, "/api/v1/chats/"+chatID+"/turn/progress", nil, cookies)
	if !strings.Contains(rec.Body.String(), "event: done\ndata: 1") {
		t.Errorf("done event missing its data field: %q", rec.Body.String())
	}
}

// A turn with no tracker is a 404 rather than an empty stream, so the client
// can tell "already finished and evicted" from "connected, nothing yet".
func TestChatTurnProgressIs404WithNoActiveTurn(t *testing.T) {
	s, cookies, chatID := chatAPIFixture(t)
	rec := doJSON(t, s, http.MethodGet, "/api/v1/chats/"+chatID+"/turn/progress", nil, cookies)
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

// in_flight is what lets a returning client re-attach deterministically.
func TestGetChatReportsInFlight(t *testing.T) {
	s, cookies, chatID := chatAPIFixture(t)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/chats/"+chatID, nil, cookies)
	if !strings.Contains(rec.Body.String(), `"in_flight":false`) {
		t.Errorf("idle chat should report in_flight false: %s", rec.Body.String())
	}

	s.chatTurnsMu.Lock()
	s.chatTurns[chatID] = &chatTurnState{
		id: "t1", progressCh: make(chan string), lines: []string{"🔧 read_file(a.md)"},
	}
	s.chatTurnsMu.Unlock()

	rec = doJSON(t, s, http.MethodGet, "/api/v1/chats/"+chatID, nil, cookies)
	body := rec.Body.String()
	if !strings.Contains(body, `"in_flight":true`) {
		t.Errorf("live turn not reported: %s", body)
	}
	if !strings.Contains(body, "read_file(a.md)") {
		t.Errorf("turn_lines not served, so the card renders empty on first paint: %s", body)
	}
}

// A Go nil slice marshals to JSON null, and a TypeScript default parameter
// substitutes only for undefined — the shape that once unmounted a whole route.
// Asserted on the RAW bytes, since decoding into []string erases it.
func TestGetChatTurnLinesIsNeverNull(t *testing.T) {
	s, cookies, chatID := chatAPIFixture(t)
	rec := doJSON(t, s, http.MethodGet, "/api/v1/chats/"+chatID, nil, cookies)
	if strings.Contains(rec.Body.String(), `"turn_lines":null`) {
		t.Errorf("turn_lines marshalled to null: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"turn_lines":[]`) {
		t.Errorf("turn_lines missing or not an empty array: %s", rec.Body.String())
	}
}
