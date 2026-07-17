package web

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/agentrunner"
	"github.com/ilijad1/simple-agents/internal/db"
)

func seedAgent(t *testing.T, s *Server, wsID string) *db.Agent {
	t.Helper()
	a := &db.Agent{ID: uuid.New().String(), WorkspaceID: wsID, Name: "Digest",
		Description: "daily digest", Active: true, CreatedAt: time.Now()}
	if err := s.db.CreateAgent(a); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	return a
}

func TestAPIAgentsListDetailSchedule(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a := seedAgent(t, s, wsID)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/agents", nil, cookies)
	if rec.Code != 200 || !contains(rec.Body.String(), `"name":"Digest"`) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/v1/agents/"+a.ID, nil, cookies)
	if rec.Code != 200 || !contains(rec.Body.String(), `"agent_md"`) {
		t.Fatalf("detail: %d %s", rec.Code, rec.Body.String())
	}

	// Schedule: bad cron → 400; good cron → 200.
	rec = doJSON(t, s, http.MethodPut, "/api/v1/agents/"+a.ID+"/schedule",
		map[string]string{"cron_expr": "not-a-cron"}, cookies)
	if rec.Code != http.StatusBadRequest || !contains(rec.Body.String(), "invalid_cron") {
		t.Fatalf("bad cron: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodPut, "/api/v1/agents/"+a.ID+"/schedule",
		map[string]string{"cron_expr": "*/10 * * * *"}, cookies)
	if rec.Code != 200 {
		t.Fatalf("good cron: %d %s", rec.Code, rec.Body.String())
	}

	// Foreign agent → 404.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/agents/"+uuid.New().String(), nil, cookies)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("foreign: %d", rec.Code)
	}
}

// TestAPIRunAgentAlreadyRunning verifies apiRunAgent honors startManualRun's
// bool: when a run for this agent is already in flight, the endpoint reports
// 202 {"status":"already_running"} instead of silently discarding the signal
// (previously the return value was ignored and the client had no way to tell
// a genuine new run from a no-op double-click).
func TestAPIRunAgentAlreadyRunning(t *testing.T) {
	s, _ := newAPITestServer(t)
	// apiRunAgent 503s ("not_configured") before ever reaching startManualRun
	// when s.runner is nil, and newAPITestServer wires no runner. Give it a
	// harmless non-nil Runner so the already-running branch is reachable — its
	// Run() is never actually invoked here: startManualRun's in-flight check
	// (primed below) returns false before any run goroutine is spawned, so no
	// real coder subprocess is started.
	s.runner = agentrunner.New(s.db, []byte(strings.Repeat("ab", 32)), t.TempDir(), t.TempDir(), t.TempDir(), nil, t.TempDir())

	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a := seedAgent(t, s, wsID)

	// Prime the in-flight tracker directly (rather than firing a real run) so
	// the very next POST hits startManualRun's "already running" branch.
	s.runsMu.Lock()
	s.runs[a.ID] = &agentRunState{progressCh: make(chan string, 1)}
	s.runsMu.Unlock()

	rec := doJSON(t, s, http.MethodPost, "/api/v1/agents/"+a.ID+"/run", nil, cookies)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 for an already-running run: %d %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"status":"already_running"`) {
		t.Fatalf("expected already_running status: %s", rec.Body.String())
	}
}
