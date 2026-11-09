package web

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rookery-ai/rookery/internal/agentrunner"
	"github.com/rookery-ai/rookery/internal/coder"
	"github.com/rookery-ai/rookery/internal/db"
)

// enforcedRunner is a runner whose coder is the API engine, the only kind whose
// agents' limits are enforced (and so the only kind that can be paused or busy).
func enforcedRunner(t *testing.T, s *Server) {
	t.Helper()
	r := agentrunner.New(s.db, []byte(strings.Repeat("ab", 32)), t.TempDir(), t.TempDir(), t.TempDir(), nil, t.TempDir())
	r.WithCoderFactory(func(string) *coder.Coder {
		return coder.New("", time.Minute, t.TempDir(), t.TempDir()).WithAPIConfig("openrouter", "m", "", "K")
	})
	s.runner = r
}

func TestGetLimitsShape(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a := seedAgent(t, s, wsID)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/agents/"+a.ID+"/limits", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"paused_until":null`, `"spend"`, `"pause_reason":""`, `"enforced":false`, `"usd_reported":true`, `"run":{"tokens":0,"turns":0,"usd":0}`} {
		if !contains(body, want) {
			t.Errorf("missing %s in %s", want, body)
		}
	}
	// The agent detail carries the same object.
	det := doJSON(t, s, http.MethodGet, "/api/v1/agents/"+a.ID, nil, cookies)
	if !contains(det.Body.String(), `"limits":{`) {
		t.Errorf("detail lacks limits: %s", det.Body.String())
	}
}

func TestPutLimitsClearsPause(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a := seedAgent(t, s, wsID)
	if _, err := s.db.SetAgentBudgetPause(a.ID, time.Now().Add(time.Hour), "daily limit"); err != nil {
		t.Fatal(err)
	}
	got := doJSON(t, s, http.MethodGet, "/api/v1/agents/"+a.ID+"/limits", nil, cookies)
	if !contains(got.Body.String(), `"pause_reason":"daily limit"`) || contains(got.Body.String(), `"paused_until":null`) {
		t.Fatalf("pause not shown: %s", got.Body.String())
	}
	rec := doJSON(t, s, http.MethodPut, "/api/v1/agents/"+a.ID+"/limits",
		map[string]any{"run": map[string]any{"tokens": 1000, "turns": 10}, "day": map[string]any{"tokens": 5000}}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rec.Code, rec.Body.String())
	}
	b := rec.Body.String()
	if !contains(b, `"paused_until":null`) || !contains(b, `"tokens":1000`) || !contains(b, `"turns":10`) {
		t.Errorf("unexpected body %s", b)
	}
	if l, _ := s.db.GetAgentLimits(a.ID); l.DayTokens != 5000 {
		t.Errorf("not saved: %+v", l)
	}
}

func TestPutLimitsRejectsNegativeAndTurnsOver149(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a := seedAgent(t, s, wsID)
	for name, body := range map[string]map[string]any{
		"neg tokens": {"day": map[string]any{"tokens": -1}},
		"neg usd":    {"month": map[string]any{"usd": -0.5}},
		"turns 150":  {"run": map[string]any{"turns": 150}},
	} {
		rec := doJSON(t, s, http.MethodPut, "/api/v1/agents/"+a.ID+"/limits", body, cookies)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d %s", name, rec.Code, rec.Body.String())
		}
	}
	ok := doJSON(t, s, http.MethodPut, "/api/v1/agents/"+a.ID+"/limits", map[string]any{"run": map[string]any{"turns": 149}}, cookies)
	if ok.Code != http.StatusOK {
		t.Errorf("149 turns should pass: %d %s", ok.Code, ok.Body.String())
	}
}

func TestRunAgentReturns409WhenPaused(t *testing.T) {
	s, _ := newAPITestServer(t)
	enforcedRunner(t, s)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a := seedAgent(t, s, wsID)
	if _, err := s.db.SetAgentBudgetPause(a.ID, time.Now().Add(time.Hour), "daily limit"); err != nil {
		t.Fatal(err)
	}
	rec := doJSON(t, s, http.MethodPost, "/api/v1/agents/"+a.ID+"/run", nil, cookies)
	if rec.Code != http.StatusConflict || !contains(rec.Body.String(), `"code":"budget_paused"`) || !contains(rec.Body.String(), `"message"`) {
		t.Fatalf("want 409 budget_paused: %d %s", rec.Code, rec.Body.String())
	}
}

func TestRunListShowsLimitedStatus(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a := seedAgent(t, s, wsID)
	run := &db.AgentRun{ID: uuid.New().String(), AgentID: a.ID, WorkspaceID: wsID, Trigger: "cron"}
	if err := s.db.CreateAgentRun(run); err != nil {
		t.Fatal(err)
	}
	code := -1
	if err := s.db.FinishAgentRun(run.ID, db.RunOutcome{ExitCode: code, Limited: "run token limit"}); err != nil {
		t.Fatal(err)
	}
	det := doJSON(t, s, http.MethodGet, "/api/v1/agents/"+a.ID, nil, cookies).Body.String()
	if !contains(det, `"status":"limited"`) || !contains(det, `"limited":"run token limit"`) {
		t.Errorf("agent runs: %s", det)
	}
	dash := doJSON(t, s, http.MethodGet, "/api/v1/dashboard", nil, cookies).Body.String()
	if !contains(dash, `"status":"limited"`) {
		t.Errorf("dashboard runs: %s", dash)
	}
}

func TestDashboardPausedAgentsNeverNull(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	if body := doJSON(t, s, http.MethodGet, "/api/v1/dashboard", nil, cookies).Body.String(); !contains(body, `"paused_agents":[]`) {
		t.Fatalf("want empty array: %s", body)
	}
	a := seedAgent(t, s, wsID)
	if _, err := s.db.SetAgentBudgetPause(a.ID, time.Now().Add(time.Hour), "daily limit"); err != nil {
		t.Fatal(err)
	}
	body := doJSON(t, s, http.MethodGet, "/api/v1/dashboard", nil, cookies).Body.String()
	if !contains(body, `"agent_name":"Digest"`) || !contains(body, `"reason":"daily limit"`) {
		t.Errorf("paused agent missing: %s", body)
	}
}
