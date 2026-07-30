package web

import (
	"net/http"
	"testing"
	"time"

	"github.com/ilijad1/rookery/internal/db"
)

// TestAPIDashboardEmpty proves a brand-new workspace with no agents, runs,
// or schedules gets the "everything empty" shape: arrays marshal to `[]`
// (never `null`), counts are 0, has_connector is false, and display_name
// falls back to the workspace name (no profile display_name saved yet).
func TestAPIDashboardEmpty(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/dashboard", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("get dashboard: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !contains(body, `"display_name":"ws1"`) {
		t.Fatalf("expected display_name fallback to workspace name, got: %s", body)
	}
	if !contains(body, `"agent_count":0`) || !contains(body, `"active_agent_count":0`) {
		t.Fatalf("expected zero agent counts, got: %s", body)
	}
	if !contains(body, `"recent_runs":[]`) {
		t.Fatalf("expected recent_runs:[] , got: %s", body)
	}
	if !contains(body, `"upcoming":[]`) {
		t.Fatalf("expected upcoming:[] , got: %s", body)
	}
	if !contains(body, `"has_connector":false`) {
		t.Fatalf("expected has_connector:false, got: %s", body)
	}
}

// TestAPIDashboardSeededShape seeds an agent, a run, and an enabled schedule
// (plus a disabled second agent to prove active_agent_count only counts
// active agents) and asserts the returned shape carries the joined names,
// derived run status, and schedule fields through correctly.
func TestAPIDashboardSeededShape(t *testing.T) {
	s, database := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	if err := database.SetSetting(wsID, "display_name", "Ilija"); err != nil {
		t.Fatalf("save display_name: %v", err)
	}

	agentID := "agent-1"
	if err := database.CreateAgent(&db.Agent{ID: agentID, WorkspaceID: wsID, Name: "Digest Bot"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	pausedID := "agent-2"
	if err := database.CreateAgent(&db.Agent{ID: pausedID, WorkspaceID: wsID, Name: "Paused Bot"}); err != nil {
		t.Fatalf("create paused agent: %v", err)
	}
	if err := database.SetAgentActive(pausedID, false); err != nil {
		t.Fatalf("pause agent: %v", err)
	}

	runID := "run-1"
	if err := database.CreateAgentRun(&db.AgentRun{ID: runID, AgentID: agentID, WorkspaceID: wsID, Trigger: "manual"}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := database.FinishAgentRun(runID, 1, "", "boom", 0, 0, 0); err != nil {
		t.Fatalf("finish run: %v", err)
	}

	next := time.Now().UTC().Add(time.Hour)
	if err := database.UpsertAgentSchedule(&db.AgentSchedule{
		ID: "sched-1", AgentID: agentID, WorkspaceID: wsID, CronExpr: "0 8 * * *", NextRunAt: &next, Enabled: true,
	}); err != nil {
		t.Fatalf("upsert schedule: %v", err)
	}

	if err := database.UpsertPlatformConnection(&db.PlatformConnection{
		ID: "conn-1", WorkspaceID: wsID, Platform: "telegram", EncryptedToken: "x", Active: true,
	}); err != nil {
		t.Fatalf("upsert platform connection: %v", err)
	}

	rec := doJSON(t, s, http.MethodGet, "/api/v1/dashboard", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("get dashboard: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !contains(body, `"display_name":"Ilija"`) {
		t.Fatalf("expected saved display_name, got: %s", body)
	}
	if !contains(body, `"agent_count":2`) || !contains(body, `"active_agent_count":1`) {
		t.Fatalf("expected agent_count=2 active_agent_count=1, got: %s", body)
	}
	if !contains(body, `"id":"run-1"`) || !contains(body, `"agent_id":"agent-1"`) ||
		!contains(body, `"agent_name":"Digest Bot"`) || !contains(body, `"status":"failed"`) ||
		!contains(body, `"trigger":"manual"`) {
		t.Fatalf("expected seeded run in recent_runs with joined name + failed status, got: %s", body)
	}
	if !contains(body, `"cron_expr":"0 8 * * *"`) || !contains(body, `"agent_name":"Digest Bot"`) {
		t.Fatalf("expected seeded schedule in upcoming with joined name, got: %s", body)
	}
	if !contains(body, `"has_connector":true`) {
		t.Fatalf("expected has_connector:true, got: %s", body)
	}
}
