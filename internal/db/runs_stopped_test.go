package db_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/rookery-ai/rookery/internal/db"
)

func finishedRun(t *testing.T, database *db.DB, agentID, workspaceID string, out db.RunOutcome) string {
	t.Helper()
	id := uuid.New().String()
	if err := database.CreateAgentRun(&db.AgentRun{ID: id, AgentID: agentID, WorkspaceID: workspaceID, Trigger: "manual"}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := database.FinishAgentRun(id, out); err != nil {
		t.Fatalf("finish run: %v", err)
	}
	return id
}

// The flag has to survive all three read paths, because each one feeds a surface
// that would otherwise show a stopped run as failed: the detail panel, the agent's
// run list, and Home's recent activity.
func TestFinishAgentRunRecordsStopped(t *testing.T) {
	database, agentID, workspaceID := runsTestDB(t)
	id := finishedRun(t, database, agentID, workspaceID, db.RunOutcome{ExitCode: -1, Stopped: true})

	got, err := database.GetAgentRun(id)
	if err != nil || got == nil {
		t.Fatalf("GetAgentRun: %v, %v", got, err)
	}
	if !got.Stopped {
		t.Error("GetAgentRun lost the stopped flag")
	}

	listed, err := database.ListAgentRuns(agentID, 10)
	if err != nil || len(listed) != 1 {
		t.Fatalf("ListAgentRuns: %v, %v", listed, err)
	}
	if !listed[0].Stopped {
		t.Error("ListAgentRuns lost the stopped flag")
	}

	recent, err := database.RecentAgentRunsWithNames(workspaceID, 10)
	if err != nil || len(recent) != 1 {
		t.Fatalf("RecentAgentRunsWithNames: %v, %v", recent, err)
	}
	if !recent[0].Stopped {
		t.Error("RecentAgentRunsWithNames lost the stopped flag")
	}
}

func TestFinishAgentRunDefaultsToNotStopped(t *testing.T) {
	database, agentID, workspaceID := runsTestDB(t)
	id := finishedRun(t, database, agentID, workspaceID, db.RunOutcome{ExitCode: -1})
	got, err := database.GetAgentRun(id)
	if err != nil || got == nil {
		t.Fatalf("GetAgentRun: %v, %v", got, err)
	}
	if got.Stopped {
		t.Error("a failed run must not read as stopped")
	}
}

// started_at has one-second granularity, so these rows all share a timestamp in a
// fast test, exactly as a stop followed by an immediate re-run does in real use.
func TestLatestRunStopped(t *testing.T) {
	database, agentID, workspaceID := runsTestDB(t)

	check := func(want bool, when string) {
		t.Helper()
		got, err := database.LatestRunStopped(agentID)
		if err != nil {
			t.Fatalf("LatestRunStopped: %v", err)
		}
		if got != want {
			t.Fatalf("%s: LatestRunStopped = %v, want %v", when, got, want)
		}
	}

	check(false, "no runs")
	finishedRun(t, database, agentID, workspaceID, db.RunOutcome{ExitCode: -1, Stopped: true})
	check(true, "after a stopped run")
	finishedRun(t, database, agentID, workspaceID, db.RunOutcome{ExitCode: 0})
	check(false, "after a normal run followed it")
	finishedRun(t, database, agentID, workspaceID, db.RunOutcome{ExitCode: -1, Stopped: true})
	check(true, "after a later stop")
}
