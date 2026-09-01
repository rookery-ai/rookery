package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rookery-ai/rookery/internal/db"
)

// seedNamedAgent creates an agent with a chosen name and creation time, so a
// test can make alphabetical order and creation order disagree.
func seedNamedAgent(t *testing.T, s *Server, wsID, name string, created time.Time) *db.Agent {
	t.Helper()
	a := &db.Agent{ID: uuid.New().String(), WorkspaceID: wsID, Name: name,
		Description: name, Active: true, CreatedAt: created}
	if err := s.db.CreateAgent(a); err != nil {
		t.Fatalf("seed agent %s: %v", name, err)
	}
	return a
}

func listedAgentNames(t *testing.T, body string) []string {
	t.Helper()
	var out struct {
		Agents []struct {
			Name string `json:"name"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode list: %v (%s)", err, body)
	}
	names := make([]string, 0, len(out.Agents))
	for _, a := range out.Agents {
		names = append(names, a.Name)
	}
	return names
}

// The list page shows the newest agent first — the one you just built is the
// one you came back to look at. The names here are deliberately in the
// OPPOSITE alphabetical order from their creation order, because db.ListAgents
// sorts by name and is shared with five other callers that want it that way:
// if the ordering were (wrongly) applied to that query instead of to this
// handler, this test would still pass while quietly re-ordering the chat
// context builder, the gateway's /run listing, the dashboard, the KB and
// global search. The companion test below is what pins that.
func TestAPIAgentsListIsNewestFirst(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	seedNamedAgent(t, s, wsID, "Zulu", base)                   // oldest, last alphabetically
	seedNamedAgent(t, s, wsID, "Mike", base.Add(1*time.Hour))  // middle
	seedNamedAgent(t, s, wsID, "Alpha", base.Add(2*time.Hour)) // newest, first alphabetically

	rec := doJSON(t, s, http.MethodGet, "/api/v1/agents", nil, cookies)
	if rec.Code != 200 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	got := listedAgentNames(t, rec.Body.String())
	want := []string{"Alpha", "Mike", "Zulu"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order: got %v, want %v", got, want)
		}
	}
}

// db.ListAgents must keep its name ordering. Its five other callers depend on
// it, and none of them is exercised by the list-page test above — so without
// this, moving the ORDER BY into the shared query looks like a clean fix and
// silently re-orders every one of them.
func TestListAgentsQueryStaysNameOrdered(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	_, wsID := createAndEnterWorkspace(t, s, cookies)

	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	seedNamedAgent(t, s, wsID, "Zulu", base)
	seedNamedAgent(t, s, wsID, "Mike", base.Add(1*time.Hour))
	seedNamedAgent(t, s, wsID, "Alpha", base.Add(2*time.Hour))

	agents, err := s.db.ListAgents(wsID)
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	want := []string{"Alpha", "Mike", "Zulu"}
	for i, w := range want {
		if agents[i].Name != w {
			t.Fatalf("ListAgents order: got %s at %d, want %s", agents[i].Name, i, w)
		}
	}
}

// A card says when the agent last ran. The field must be PRESENT and null for
// an agent that has never run — a Go nil pointer serializes as null, but an
// omitted key decodes to undefined, and the SPA's `?? fallback` substitutes
// only for undefined. This repository has shipped that exact bug twice
// (flattenRequires, plan_ready), so the assertion is on raw response bytes:
// decoding into a struct erases the distinction being tested.
func TestAPIAgentsListCarriesLastRunAt(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	ran := seedNamedAgent(t, s, wsID, "Ran", time.Now())
	seedNamedAgent(t, s, wsID, "NeverRan", time.Now())

	if err := s.db.CreateAgentRun(&db.AgentRun{
		ID: uuid.New().String(), AgentID: ran.ID, WorkspaceID: wsID, Trigger: "manual",
	}); err != nil {
		t.Fatalf("seed run: %v", err)
	}

	rec := doJSON(t, s, http.MethodGet, "/api/v1/agents", nil, cookies)
	if rec.Code != 200 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	// The never-run agent reports null rather than omitting the key.
	if !strings.Contains(body, `"last_run_at":null`) {
		t.Fatalf("expected a null last_run_at in the raw body, got %s", body)
	}

	var out struct {
		Agents []struct {
			Name      string     `json:"name"`
			LastRunAt *time.Time `json:"last_run_at"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, a := range out.Agents {
		switch a.Name {
		case "Ran":
			if a.LastRunAt == nil {
				t.Fatal("agent with a run reported no last_run_at")
			}
		case "NeverRan":
			if a.LastRunAt != nil {
				t.Fatalf("agent with no runs reported last_run_at %v", a.LastRunAt)
			}
		}
	}
}

// "Last run" is the most recently STARTED run, not the most recently finished
// one: a run in flight has no finished_at, and an agent that is running right
// now has most certainly run. Two runs, the later one still unfinished — the
// answer must be the later one.
func TestLastRunAtCountsAnUnfinishedRun(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	_, wsID := createAndEnterWorkspace(t, s, cookies)
	a := seedNamedAgent(t, s, wsID, "Busy", time.Now())

	older := &db.AgentRun{ID: uuid.New().String(), AgentID: a.ID, WorkspaceID: wsID, Trigger: "cron"}
	if err := s.db.CreateAgentRun(older); err != nil {
		t.Fatalf("seed older run: %v", err)
	}
	if err := s.db.FinishAgentRun(older.ID, db.RunOutcome{ExitCode: 0}); err != nil {
		t.Fatalf("finish older run: %v", err)
	}
	// datetime('now') has one-second granularity, so the second run needs a
	// distinct second or MAX() cannot tell them apart.
	time.Sleep(1100 * time.Millisecond)
	if err := s.db.CreateAgentRun(&db.AgentRun{
		ID: uuid.New().String(), AgentID: a.ID, WorkspaceID: wsID, Trigger: "manual",
	}); err != nil {
		t.Fatalf("seed running run: %v", err)
	}

	last, err := s.db.LastRunTimes(wsID)
	if err != nil {
		t.Fatalf("LastRunTimes: %v", err)
	}
	got, ok := last[a.ID]
	if !ok {
		t.Fatal("no last run recorded for an agent with two runs")
	}
	runs, err := s.db.ListAgentRuns(a.ID, 10)
	if err != nil {
		t.Fatalf("ListAgentRuns: %v", err)
	}
	// ListAgentRuns is started_at DESC, so runs[0] is the unfinished one.
	if !got.Equal(runs[0].StartedAt) {
		t.Fatalf("last run %v, want the newest run's start %v", got, runs[0].StartedAt)
	}
	if runs[0].FinishedAt != nil {
		t.Fatal("fixture wrong: the newest run should still be unfinished")
	}
}
