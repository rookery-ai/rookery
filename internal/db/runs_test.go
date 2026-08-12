package db_test

import (
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/rookery-ai/rookery/internal/db"
)

// runsTestDB opens a fresh migrated DB with one user + one agent (FK targets) and
// returns the agentID and its workspaceID.
func runsTestDB(t *testing.T) (*db.DB, string, string) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	workspaceID := uuid.New().String()
	if err := database.CreateWorkspace(&db.Workspace{ID: workspaceID, Name: "tester"}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	agentID := "agent-1"
	if err := database.CreateAgent(&db.Agent{ID: agentID, WorkspaceID: workspaceID, Name: "A", Active: true}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return database, agentID, workspaceID
}

// TestGetUnfinishedAgentRun drives the durable "Running…" badge: an open run (no
// finished_at) is reported; once finished it no longer is.
func TestGetUnfinishedAgentRun(t *testing.T) {
	database, agentID, workspaceID := runsTestDB(t)

	if run, err := database.GetUnfinishedAgentRun(agentID); err != nil || run != nil {
		t.Fatalf("expected no unfinished run, got run=%v err=%v", run, err)
	}

	runID := uuid.New().String()
	if err := database.CreateAgentRun(&db.AgentRun{ID: runID, AgentID: agentID, WorkspaceID: workspaceID, Trigger: "manual"}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	run, err := database.GetUnfinishedAgentRun(agentID)
	if err != nil || run == nil || run.ID != runID {
		t.Fatalf("expected open run %s, got run=%v err=%v", runID, run, err)
	}

	if err := database.FinishAgentRun(runID, 0, "ok", "", 120, 80, 200); err != nil {
		t.Fatalf("finish run: %v", err)
	}
	// Token usage persisted by the API coder is read back on list/recent queries.
	runs, err := database.ListAgentRuns(agentID, 10)
	if err != nil || len(runs) != 1 {
		t.Fatalf("list runs: %v (n=%d)", err, len(runs))
	}
	if r := runs[0]; r.PromptTokens != 120 || r.CompletionTokens != 80 || r.TotalTokens != 200 {
		t.Fatalf("usage = %d/%d/%d, want 120/80/200", r.PromptTokens, r.CompletionTokens, r.TotalTokens)
	}
	if run, err := database.GetUnfinishedAgentRun(agentID); err != nil || run != nil {
		t.Fatalf("expected no unfinished run after finish, got run=%v err=%v", run, err)
	}
}

// TestReconcileStaleRuns proves a crash-leftover open run is closed out (exit -1) on
// boot so the badge can't stick on forever, while a finished run is left untouched.
func TestReconcileStaleRuns(t *testing.T) {
	database, agentID, workspaceID := runsTestDB(t)

	openID := uuid.New().String()
	doneID := uuid.New().String()
	for _, id := range []string{openID, doneID} {
		if err := database.CreateAgentRun(&db.AgentRun{ID: id, AgentID: agentID, WorkspaceID: workspaceID, Trigger: "manual"}); err != nil {
			t.Fatalf("create run: %v", err)
		}
	}
	if err := database.FinishAgentRun(doneID, 0, "ok", "", 0, 0, 0); err != nil {
		t.Fatalf("finish run: %v", err)
	}

	n, err := database.ReconcileStaleRuns()
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 reconciled run, got %d", n)
	}

	if run, err := database.GetUnfinishedAgentRun(agentID); err != nil || run != nil {
		t.Fatalf("expected no unfinished run after reconcile, got run=%v err=%v", run, err)
	}

	runs, err := database.ListAgentRuns(agentID, 10)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	for _, r := range runs {
		switch r.ID {
		case doneID:
			if r.ExitCode == nil || *r.ExitCode != 0 {
				t.Fatalf("finished run exit code was altered: %+v", r)
			}
		case openID:
			if r.ExitCode == nil || *r.ExitCode != -1 {
				t.Fatalf("reconciled run should have exit -1, got %+v", r)
			}
		}
	}
}
