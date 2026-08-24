package web

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rookery-ai/rookery/internal/db"
)

type runDetailBody struct {
	ID         string `json:"id"`
	Silent     bool   `json:"silent"`
	Stdout     string `json:"stdout"`
	Transcript []struct {
		Kind string `json:"kind"`
		Text string `json:"text"`
	} `json:"transcript"`
}

func seedFinishedRun(t *testing.T, s *Server, wsID, agentID string, out db.RunOutcome) string {
	t.Helper()
	runID := uuid.New().String()
	if err := s.db.CreateAgentRun(&db.AgentRun{
		ID: runID, AgentID: agentID, WorkspaceID: wsID, Trigger: "cron",
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := s.db.FinishAgentRun(runID, out); err != nil {
		t.Fatalf("finish run: %v", err)
	}
	return runID
}

// The endpoint behind an expanded run row: it serves the transcript the agent
// detail response deliberately omits.
func TestAgentRunDetailServesTheTranscript(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a := seedAgent(t, s, wsID)

	runID := seedFinishedRun(t, s, wsID, a.ID, db.RunOutcome{
		Stdout:     "23°C, clear",
		Transcript: `[{"kind":"progress","at":"2026-08-24T09:00:00Z","text":"web_fetch(weather)"}]`,
	})

	rec := doJSON(t, s, http.MethodGet, "/api/v1/agents/"+a.ID+"/runs/"+runID, nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET run detail = %d: %s", rec.Code, rec.Body.String())
	}
	var body runDetailBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Stdout != "23°C, clear" {
		t.Errorf("stdout = %q", body.Stdout)
	}
	if len(body.Transcript) != 1 || body.Transcript[0].Text != "web_fetch(weather)" {
		t.Errorf("transcript = %+v, want the one event", body.Transcript)
	}
}

// A slice field must never marshal to null: a TypeScript default parameter
// substitutes only for undefined, so `transcript.length` on a null would
// unmount the panel. Asserted on the RAW bytes, since decoding erases the
// distinction.
func TestAgentRunDetailTranscriptIsNeverNull(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a := seedAgent(t, s, wsID)

	// A run recorded before the column existed, and one whose stored transcript
	// is unparseable — both must still answer with an empty array.
	for name, out := range map[string]db.RunOutcome{
		"no transcript":  {Stdout: "ok"},
		"bad transcript": {Stdout: "ok", Transcript: "{not json"},
	} {
		runID := seedFinishedRun(t, s, wsID, a.ID, out)
		rec := doJSON(t, s, http.MethodGet, "/api/v1/agents/"+a.ID+"/runs/"+runID, nil, cookies)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: GET = %d: %s", name, rec.Code, rec.Body.String())
		}
		if !contains(rec.Body.String(), `"transcript":[]`) {
			t.Errorf("%s: want an empty array, got %s", name, rec.Body.String())
		}
	}
}

// The silent flag rides the detail response too, so an expanded row agrees with
// the chip in the list.
func TestAgentRunDetailReportsSilent(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a := seedAgent(t, s, wsID)

	runID := seedFinishedRun(t, s, wsID, a.ID, db.RunOutcome{Silent: true})

	rec := doJSON(t, s, http.MethodGet, "/api/v1/agents/"+a.ID+"/runs/"+runID, nil, cookies)
	var body runDetailBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Silent {
		t.Error("expected silent:true")
	}
}

// The run id arrives in the URL, so resolving it without confirming it belongs
// to THIS workspace's agent would let a guessed id read another tenant's run.
func TestAgentRunDetailRefusesARunFromAnotherAgent(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a := seedAgent(t, s, wsID)
	// Not seedAgent: agent names are unique per workspace, and this test needs
	// a SECOND agent in the same workspace.
	other := &db.Agent{ID: uuid.New().String(), WorkspaceID: wsID, Name: "Other",
		Description: "another agent", Active: true, CreatedAt: time.Now()}
	if err := s.db.CreateAgent(other); err != nil {
		t.Fatalf("seed other agent: %v", err)
	}

	runID := seedFinishedRun(t, s, wsID, other.ID, db.RunOutcome{Stdout: "secret"})

	rec := doJSON(t, s, http.MethodGet, "/api/v1/agents/"+a.ID+"/runs/"+runID, nil, cookies)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for another agent's run, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAgentRunDetailUnknownRunIs404(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a := seedAgent(t, s, wsID)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/agents/"+a.ID+"/runs/"+uuid.New().String(), nil, cookies)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}
