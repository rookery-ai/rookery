package web

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
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
