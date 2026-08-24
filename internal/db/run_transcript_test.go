package db_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/rookery-ai/rookery/internal/db"
)

// A [SILENT] run and a run that produced nothing because it broke were
// identically shaped in the database — exit 0, empty stdout — so the interface
// could only render the same empty row for both. The flag has to survive the
// round trip, and it has to be on the LIST query: the chip must not cost a
// per-row transcript fetch.
func TestSilentSurvivesTheRoundTripAndReachesTheList(t *testing.T) {
	database, agentID, workspaceID := runsTestDB(t)

	runID := uuid.New().String()
	if err := database.CreateAgentRun(&db.AgentRun{ID: runID, AgentID: agentID, WorkspaceID: workspaceID, Trigger: "cron"}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := database.FinishAgentRun(runID, db.RunOutcome{Silent: true}); err != nil {
		t.Fatalf("finish run: %v", err)
	}

	runs, err := database.ListAgentRuns(agentID, 10)
	if err != nil || len(runs) != 1 {
		t.Fatalf("list runs: %v (n=%d)", err, len(runs))
	}
	if !runs[0].Silent {
		t.Error("ListAgentRuns must report the silent flag")
	}

	got, err := database.GetAgentRun(runID)
	if err != nil || got == nil {
		t.Fatalf("get run: %v", err)
	}
	if !got.Silent {
		t.Error("GetAgentRun must report the silent flag")
	}
}

// A run that spoke is not silent — the flag must not simply track "empty
// stdout", which is the inference this column exists to replace.
func TestRunThatSpokeIsNotMarkedSilent(t *testing.T) {
	database, agentID, workspaceID := runsTestDB(t)

	runID := uuid.New().String()
	if err := database.CreateAgentRun(&db.AgentRun{ID: runID, AgentID: agentID, WorkspaceID: workspaceID, Trigger: "manual"}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := database.FinishAgentRun(runID, db.RunOutcome{Stdout: "23°C, clear"}); err != nil {
		t.Fatalf("finish run: %v", err)
	}

	got, err := database.GetAgentRun(runID)
	if err != nil || got == nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Silent {
		t.Error("a run with output must not be marked silent")
	}
}

// The transcript is the point of the whole change, and the list/detail split is
// what keeps it affordable: GetAgentRun returns it, ListAgentRuns must not.
func TestTranscriptIsReadBackByDetailAndOmittedFromTheList(t *testing.T) {
	database, agentID, workspaceID := runsTestDB(t)

	runID := uuid.New().String()
	if err := database.CreateAgentRun(&db.AgentRun{ID: runID, AgentID: agentID, WorkspaceID: workspaceID, Trigger: "manual"}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	const transcript = `[{"kind":"progress","at":"2026-08-24T09:00:00Z","text":"read_file(a)"}]`
	if err := database.FinishAgentRun(runID, db.RunOutcome{Stdout: "done", Transcript: transcript}); err != nil {
		t.Fatalf("finish run: %v", err)
	}

	got, err := database.GetAgentRun(runID)
	if err != nil || got == nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Transcript != transcript {
		t.Errorf("transcript = %q, want %q", got.Transcript, transcript)
	}

	runs, err := database.ListAgentRuns(agentID, 10)
	if err != nil || len(runs) != 1 {
		t.Fatalf("list runs: %v (n=%d)", err, len(runs))
	}
	if runs[0].Transcript != "" {
		t.Error("ListAgentRuns must not carry transcripts — that is the whole reason for the lazy detail endpoint")
	}
}

// A run recorded before the column existed reads back as an empty transcript
// and a non-silent run, rather than failing to scan.
func TestRunWithNoTranscriptReadsBackEmpty(t *testing.T) {
	database, agentID, workspaceID := runsTestDB(t)

	runID := uuid.New().String()
	if err := database.CreateAgentRun(&db.AgentRun{ID: runID, AgentID: agentID, WorkspaceID: workspaceID, Trigger: "manual"}); err != nil {
		t.Fatalf("create run: %v", err)
	}

	got, err := database.GetAgentRun(runID)
	if err != nil || got == nil {
		t.Fatalf("get run: %v", err)
	}
	if got.Transcript != "" || got.Silent {
		t.Errorf("unfinished run = transcript %q silent %v, want empty/false", got.Transcript, got.Silent)
	}
}

// A missing run is (nil, nil), not an error — the endpoint turns that into a
// 404 rather than a 500.
func TestGetAgentRunReturnsNilForAnUnknownID(t *testing.T) {
	database, _, _ := runsTestDB(t)

	got, err := database.GetAgentRun(uuid.New().String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}
