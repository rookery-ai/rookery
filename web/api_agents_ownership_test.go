package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rookery-ai/rookery/internal/agentdesigner"
	"github.com/rookery-ai/rookery/internal/db"
)

// A note turn is the coder's steering context and must never reach the browser.
// Rendering it is what showed a generic "it did not succeed" in the web UI while
// the real explanation went to chat alone.
func TestDesignHistoryDTODropsNoteTurns(t *testing.T) {
	got := designHistoryDTO([]db.ChatMessage{
		{Role: "user", Content: "approve"},
		{Role: "assistant", Content: "here is the real reason"},
		{Role: agentdesigner.RoleNote, Content: "internal steering"},
	})

	if len(got) != 2 {
		t.Fatalf("entries = %d, want 2", len(got))
	}
	for _, e := range got {
		if strings.Contains(e.Content, "internal steering") {
			t.Errorf("note turn leaked to the browser: %+v", e)
		}
	}
	if got[1].Content != "here is the real reason" {
		t.Errorf("entry 1 = %q, want the user-facing message preserved", got[1].Content)
	}
}

// /design/state must tell the SPA who owns the session — that is the only
// signal it has for rendering itself as a read-only mirror rather than a
// driver, and a mirroring tab that thinks it drives can cancel a live build on
// the other surface.
func TestDesignStateCarriesOrigin(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	s.designFlow = agentdesigner.NewFlow(nil, nil).WithDB(s.db)
	if _, err := s.designFlow.Start(wsID, "TestAgent", agentdesigner.OriginChat); err != nil {
		t.Fatalf("start design session: %v", err)
	}

	rec := doJSON(t, s, http.MethodGet, "/api/v1/agents/design/state", nil, cookies)
	if rec.Code != 200 {
		t.Fatalf("design state: %d %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"origin":"chat"`) {
		t.Fatalf(`expected "origin":"chat" on the wire: %s`, rec.Body.String())
	}
}

// The web cancel endpoint must refuse a session it does not own. The SPA adopts
// whatever session exists on mount, so without this, opening the agent page
// while a Telegram build ran and clicking Cancel killed that build.
func TestCancelRefusesANonOwnedSession(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	s.designFlow = agentdesigner.NewFlow(nil, nil).WithDB(s.db)
	if _, err := s.designFlow.Start(wsID, "TestAgent", agentdesigner.OriginChat); err != nil {
		t.Fatalf("start design session: %v", err)
	}

	rec := doJSON(t, s, http.MethodPost, "/api/v1/agents/design/cancel", nil, cookies)
	if rec.Code != 200 {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), "not_owner") {
		t.Fatalf("expected a not_owner refusal: %s", rec.Body.String())
	}
	if s.designFlow.GetSession(wsID) == nil {
		t.Fatal("the chat-owned session was cancelled by the web surface")
	}
}

// The owner may still cancel.
func TestCancelAllowsTheOwningSurface(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	s.designFlow = agentdesigner.NewFlow(nil, nil).WithDB(s.db)
	if _, err := s.designFlow.Start(wsID, "TestAgent", agentdesigner.OriginWeb); err != nil {
		t.Fatalf("start design session: %v", err)
	}

	rec := doJSON(t, s, http.MethodPost, "/api/v1/agents/design/cancel", nil, cookies)
	if rec.Code != 200 {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body.String())
	}
	if s.designFlow.GetSession(wsID) != nil {
		t.Fatal("the owner's cancel did not take effect")
	}
}

// The design stream had no terminal event, so the browser could only infer
// completion from a 404 on the NEXT attach — after handleDesignProgress's 30s
// poll, and only because EventSource happened to reconnect. run_tracker.go has
// emitted a named done event all along; this closes the asymmetry.
func TestDesignProgressEmitsDoneBeforeClosing(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	s.designFlow = agentdesigner.NewFlow(nil, nil).WithDB(s.db)
	if _, err := s.designFlow.Start(wsID, "TestAgent", agentdesigner.OriginWeb); err != nil {
		t.Fatalf("start design session: %v", err)
	}
	s.designFlow.MarkGeneratingForTest(wsID)
	s.designFlow.PushProgressForTest(wsID, "milestone one")

	// End the fake build shortly after the handler attaches, so the stream sees
	// the milestone and then the close.
	go func() {
		time.Sleep(300 * time.Millisecond)
		s.designFlow.FinishGeneratingForTest(wsID)
	}()

	rec := doJSON(t, s, http.MethodGet, "/api/v1/agents/design/progress", nil, cookies)
	body := rec.Body.String()

	if !strings.Contains(body, "data: milestone one") {
		t.Errorf("body = %q, want the milestone line", body)
	}
	if !strings.Contains(body, "event: done") {
		t.Errorf("body = %q, want a terminating done event", body)
	}
}

// A user whose session was finished from the other surface used to get
// "name is required to start a new session" — an internal precondition that
// told them nothing about what had happened or what to do.
func TestEndedSessionGivesAnHonestError(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	s.designFlow = agentdesigner.NewFlow(nil, nil).WithDB(s.db)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/agents/design",
		map[string]string{"message": "approve"}, cookies)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["code"] != "session_ended" {
		t.Errorf("code = %q, want session_ended", body["code"])
	}
	if !strings.Contains(body["error"], "another surface") {
		t.Errorf("error = %q, want it to explain what happened", body["error"])
	}
}
