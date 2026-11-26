package db_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/rookery-ai/rookery/internal/db"
)

// seedRuns finishes n runs for the agent, the i-th started i minutes after a fixed
// base, each carrying a transcript, output, error text and usage.
func seedRuns(t *testing.T, database *db.DB, agentID, workspaceID string, n int) []string {
	t.Helper()
	base := time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		ids[i] = finishedRun(t, database, agentID, workspaceID, db.RunOutcome{
			Stdout: fmt.Sprintf("out %d", i), Stderr: "warn", Transcript: `[{"kind":"progress"}]`,
			TotalTokens: 100, CostUSD: 0.01, CostReported: true,
		})
		at := base.Add(time.Duration(i) * time.Minute).Format("2006-01-02 15:04:05")
		if _, err := database.Exec(`UPDATE agent_runs SET started_at=? WHERE id=?`, at, ids[i]); err != nil {
			t.Fatalf("set started_at: %v", err)
		}
	}
	return ids
}

func TestPruneAgentRunsKeepsNewestAndSlimsTheRest(t *testing.T) {
	database, agentID, workspaceID := runsTestDB(t)
	ids := seedRuns(t, database, agentID, workspaceID, 20)

	kept, err := database.PruneAgentRuns(agentID, 15)
	if err != nil {
		t.Fatalf("PruneAgentRuns: %v", err)
	}
	if len(kept) != 15 {
		t.Fatalf("kept %d ids, want 15", len(kept))
	}
	keptSet := map[string]bool{}
	for _, id := range kept {
		keptSet[id] = true
	}
	for i, id := range ids {
		r, err := database.GetAgentRun(id)
		if err != nil || r == nil {
			t.Fatalf("GetAgentRun(%d): %v", i, err)
		}
		if i >= 5 {
			if !keptSet[id] {
				t.Errorf("run %d is among the newest 15 but was not reported kept", i)
			}
			if r.Transcript == "" || r.Stdout == "" || r.Stderr == "" {
				t.Errorf("run %d is among the newest 15 but lost its record", i)
			}
			continue
		}
		if keptSet[id] {
			t.Errorf("run %d is older than the newest 15 but was reported kept", i)
		}
		if r.Transcript != "" || r.Stdout != "" || r.Stderr != "" {
			t.Errorf("run %d should be slimmed, got transcript=%q stdout=%q stderr=%q", i, r.Transcript, r.Stdout, r.Stderr)
		}
		if r.TotalTokens != 100 || r.CostUSD != 0.01 {
			t.Errorf("run %d lost its usage: tokens=%d cost=%v", i, r.TotalTokens, r.CostUSD)
		}
	}
}

func TestPruneAgentRunsLeavesInProgressAndFewer(t *testing.T) {
	database, agentID, workspaceID := runsTestDB(t)
	ids := seedRuns(t, database, agentID, workspaceID, 16)
	// The oldest run never finished: it must not be touched.
	if _, err := database.Exec(`UPDATE agent_runs SET finished_at=NULL WHERE id=?`, ids[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := database.PruneAgentRuns(agentID, 15); err != nil {
		t.Fatalf("PruneAgentRuns: %v", err)
	}
	if r, _ := database.GetAgentRun(ids[0]); r == nil || r.Transcript == "" || r.Stdout == "" {
		t.Errorf("an in-progress run was slimmed: %+v", r)
	}

	// A second agent with fewer runs than the cap is left exactly as it was.
	if err := database.CreateAgent(&db.Agent{ID: "agent-2", WorkspaceID: workspaceID, Name: "B", Active: true}); err != nil {
		t.Fatal(err)
	}
	few := seedRuns(t, database, "agent-2", workspaceID, 3)
	kept, err := database.PruneAgentRuns("agent-2", 15)
	if err != nil || len(kept) != 3 {
		t.Fatalf("kept=%v err=%v, want all 3", kept, err)
	}
	for _, id := range few {
		if r, _ := database.GetAgentRun(id); r == nil || r.Transcript == "" {
			t.Errorf("run %s of an agent under the cap was slimmed", id)
		}
	}
}

// Runs started in the same second (a stop followed by an immediate re-run) tie on
// started_at; the insertion order decides, so exactly the cap survives.
func TestPruneAgentRunsSameSecondTieBreak(t *testing.T) {
	database, agentID, workspaceID := runsTestDB(t)
	var ids []string
	for i := 0; i < 17; i++ {
		ids = append(ids, finishedRun(t, database, agentID, workspaceID, db.RunOutcome{Stdout: "x", Transcript: "[]"}))
	}
	if _, err := database.Exec(`UPDATE agent_runs SET started_at='2026-01-01 08:00:00' WHERE agent_id=?`, agentID); err != nil {
		t.Fatal(err)
	}
	kept, err := database.PruneAgentRuns(agentID, 15)
	if err != nil || len(kept) != 15 {
		t.Fatalf("kept %d (err %v), want 15", len(kept), err)
	}
	for _, id := range ids[:2] {
		if r, _ := database.GetAgentRun(id); r.Stdout != "" {
			t.Errorf("one of the two first-inserted runs survived a same-second tie: %s", id)
		}
	}
}

// Day and month limits sum agent_runs usage, so slimming must not change it.
func TestPruneKeepsSpendExact(t *testing.T) {
	database, agentID, workspaceID := runsTestDB(t)
	seedRuns(t, database, agentID, workspaceID, 20)
	since := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	before, err := database.AgentSpendSince(agentID, since)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.PruneAgentRuns(agentID, 15); err != nil {
		t.Fatal(err)
	}
	after, err := database.AgentSpendSince(agentID, since)
	if err != nil {
		t.Fatal(err)
	}
	if before != after || after.Tokens != 2000 {
		t.Errorf("spend changed: before=%+v after=%+v", before, after)
	}
}

func TestListAllAgentRefs(t *testing.T) {
	database, agentID, workspaceID := runsTestDB(t)
	refs, err := database.ListAllAgentRefs()
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].ID != agentID || refs[0].WorkspaceID != workspaceID {
		t.Errorf("refs = %+v", refs)
	}
}
