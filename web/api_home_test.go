package web

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/ilijad1/rookery/internal/db"
)

// ── Reminders ────────────────────────────────────────────────────────────────

func TestAPIRemindersCRUD(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	// Missing message.
	rec := doJSON(t, s, http.MethodPost, "/api/v1/reminders", map[string]string{"when": "in 10 minutes"}, cookies)
	if rec.Code != http.StatusBadRequest || !contains(rec.Body.String(), "missing_field") {
		t.Fatalf("missing message: %d %s", rec.Code, rec.Body.String())
	}

	// Missing when.
	rec = doJSON(t, s, http.MethodPost, "/api/v1/reminders", map[string]string{"message": "take out trash"}, cookies)
	if rec.Code != http.StatusBadRequest || !contains(rec.Body.String(), "missing_field") {
		t.Fatalf("missing when: %d %s", rec.Code, rec.Body.String())
	}

	// Whitespace-only when is trimmed and treated as missing (matches the
	// template's strings.TrimSpace(c.FormValue("when"))).
	rec = doJSON(t, s, http.MethodPost, "/api/v1/reminders", map[string]string{"message": "x", "when": "   "}, cookies)
	if rec.Code != http.StatusBadRequest || !contains(rec.Body.String(), "missing_field") {
		t.Fatalf("whitespace when: %d %s", rec.Code, rec.Body.String())
	}

	// Deterministic create — fast regex path, no LLM call.
	rec = doJSON(t, s, http.MethodPost, "/api/v1/reminders",
		map[string]string{"message": "take out trash", "when": "in 10 minutes"}, cookies)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID       string `json:"id"`
		Message  string `json:"message"`
		RemindAt string `json:"remind_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.ID == "" || created.Message != "take out trash" || created.RemindAt == "" {
		t.Fatalf("unexpected create response: %s", rec.Body.String())
	}

	// List shows it, with sent=false.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/reminders", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), created.ID) || !contains(rec.Body.String(), `"sent":false`) {
		t.Fatalf("list missing created reminder or sent field: %s", rec.Body.String())
	}

	// Delete.
	rec = doJSON(t, s, http.MethodDelete, "/api/v1/reminders/"+created.ID, nil, cookies)
	if rec.Code != http.StatusOK || !contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}

	// Foreign/missing id → 404.
	rec = doJSON(t, s, http.MethodDelete, "/api/v1/reminders/does-not-exist", nil, cookies)
	if rec.Code != http.StatusNotFound || !contains(rec.Body.String(), "not_found") {
		t.Fatalf("delete foreign: %d %s", rec.Code, rec.Body.String())
	}
}

// TestAPIRemindersUnparseableTime forces the LLM fallback to fail hermetically
// (fast, no network, no real coder call) by pointing the workspace's local
// coder at a nonexistent binary before posting a non-regex-parseable "when".
// This mirrors the real "coder unconfigured/broken" case the task brief
// describes, without ever invoking a real LLM (which this dev environment
// otherwise has authenticated and reachable — verified empirically to take
// ~4s per call and actually complete a real API round trip for "banana",
// which is unacceptable in a hermetic unit test).
func TestAPIRemindersUnparseableTime(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	if err := s.db.UpdateWorkspaceCoder(wsID, "local", "/nonexistent/bogus-coder-bin", 5, "", "", "", "", ""); err != nil {
		t.Fatalf("force bogus coder bin: %v", err)
	}

	rec := doJSON(t, s, http.MethodPost, "/api/v1/reminders",
		map[string]string{"message": "hi", "when": "banana"}, cookies)
	if rec.Code != http.StatusBadRequest || !contains(rec.Body.String(), "unparseable_time") {
		t.Fatalf("expected 400 unparseable_time, got: %d %s", rec.Code, rec.Body.String())
	}
}

// TestAPIRemindersSingleField covers the one-field natural-language create path
// (regex-deterministic — no LLM): the sentence carries both time and message.
func TestAPIRemindersSingleField(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/reminders",
		map[string]string{"text": "in 10 minutes to call the doctor"}, cookies)
	if rec.Code != http.StatusCreated {
		t.Fatalf("single-field create: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Message  string `json:"message"`
		RemindAt string `json:"remind_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Message != "call the doctor" || created.RemindAt == "" {
		t.Fatalf("unexpected create response: %s", rec.Body.String())
	}
}

// ── Inbox ────────────────────────────────────────────────────────────────────

func TestAPIInboxCRUD(t *testing.T) {
	s, database := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	// Empty inbox → messages:[] not null, unread:0.
	rec := doJSON(t, s, http.MethodGet, "/api/v1/inbox", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty list: %d %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"messages":[]`) || !contains(rec.Body.String(), `"unread":0`) {
		t.Fatalf("expected empty inbox, got: %s", rec.Body.String())
	}

	// Seed a row directly.
	msg := &db.InboxMessage{
		ID:          uuid.New().String(),
		WorkspaceID: wsID,
		Source:      "reminder",
		AgentName:   "",
		RefID:       "some-reminder-id",
		Trigger:     "",
		Body:        "This is the full body of the notification, long enough that a 160-char preview would truncate it if this endpoint used the preview shape instead of the full body — but it should not.",
		Status:      "ok",
	}
	if err := database.CreateInboxMessage(msg); err != nil {
		t.Fatalf("seed inbox message: %v", err)
	}

	// List shows full body + unread=1, read=false.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/inbox", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), msg.ID) || !contains(rec.Body.String(), msg.Body) {
		t.Fatalf("expected full body in list, got: %s", rec.Body.String())
	}
	if !contains(rec.Body.String(), `"unread":1`) || !contains(rec.Body.String(), `"read":false`) {
		t.Fatalf("expected unread=1 read=false, got: %s", rec.Body.String())
	}

	// limit/offset params accepted.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/inbox?limit=1&offset=0", nil, cookies)
	if rec.Code != http.StatusOK || !contains(rec.Body.String(), msg.ID) {
		t.Fatalf("list with limit/offset: %d %s", rec.Code, rec.Body.String())
	}

	// Mark read.
	rec = doJSON(t, s, http.MethodPost, "/api/v1/inbox/"+msg.ID+"/read", nil, cookies)
	if rec.Code != http.StatusOK || !contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("mark read: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodGet, "/api/v1/inbox", nil, cookies)
	if !contains(rec.Body.String(), `"unread":0`) || !contains(rec.Body.String(), `"read":true`) {
		t.Fatalf("expected read=true unread=0 after mark read, got: %s", rec.Body.String())
	}

	// Seed a second message, then mark-all-read.
	agentID := uuid.New().String()
	if err := database.CreateAgent(&db.Agent{ID: agentID, WorkspaceID: wsID, Name: "my-agent"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	msg2 := &db.InboxMessage{
		ID: uuid.New().String(), WorkspaceID: wsID, Source: "agent_run",
		AgentID: agentID, AgentName: "my-agent", RefID: "run-1", Trigger: "cron",
		Body: "second message", Status: "ok",
	}
	if err := database.CreateInboxMessage(msg2); err != nil {
		t.Fatalf("seed msg2: %v", err)
	}
	rec = doJSON(t, s, http.MethodGet, "/api/v1/inbox", nil, cookies)
	if !contains(rec.Body.String(), `"unread":1`) {
		t.Fatalf("expected unread=1 before read-all, got: %s", rec.Body.String())
	}
	// Deep-linking to the agent (spec §5.3): agent_id must be exposed for an
	// agent-sourced message, and empty for the reminder-sourced msg above.
	if !contains(rec.Body.String(), `"agent_id":"`+agentID+`"`) {
		t.Fatalf("expected agent_id %q in agent-sourced message, got: %s", agentID, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"agent_id":""`) {
		t.Fatalf("expected empty agent_id for reminder-sourced message, got: %s", rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodPost, "/api/v1/inbox/read-all", nil, cookies)
	if rec.Code != http.StatusOK || !contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("read-all: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodGet, "/api/v1/inbox", nil, cookies)
	if !contains(rec.Body.String(), `"unread":0`) {
		t.Fatalf("expected unread=0 after read-all, got: %s", rec.Body.String())
	}

	// Delete both, empty list again.
	rec = doJSON(t, s, http.MethodDelete, "/api/v1/inbox/"+msg.ID, nil, cookies)
	if rec.Code != http.StatusOK || !contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("delete msg: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodDelete, "/api/v1/inbox/"+msg2.ID, nil, cookies)
	if rec.Code != http.StatusOK || !contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("delete msg2: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodGet, "/api/v1/inbox", nil, cookies)
	if !contains(rec.Body.String(), `"messages":[]`) {
		t.Fatalf("expected empty messages after delete, got: %s", rec.Body.String())
	}
}

// ── Poll re-registration (unchanged legacy handlers) ────────────────────────

func TestAPIRemindersPollAndInboxPollUnchanged(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/reminders/poll", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("reminders poll: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/v1/inbox/poll", nil, cookies)
	if rec.Code != http.StatusOK || !contains(rec.Body.String(), `"unread"`) || !contains(rec.Body.String(), `"recent"`) {
		t.Fatalf("inbox poll: %d %s", rec.Code, rec.Body.String())
	}
}
