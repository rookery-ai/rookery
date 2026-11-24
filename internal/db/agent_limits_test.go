package db_test

import (
	"testing"
	"time"

	"github.com/rookery-ai/rookery/internal/db"
)

func TestSaveAndGetAgentLimits(t *testing.T) {
	database, agentID, _ := runsTestDB(t)
	got, err := database.GetAgentLimits(agentID)
	if err != nil || got.AgentID != agentID || got.RunTokens != 0 {
		t.Fatalf("empty get = %+v, %v", got, err)
	}
	want := db.AgentLimits{AgentID: agentID, RunTokens: 1000, RunTurns: 5, DayTokens: 5000, MonthTokens: 90000, RunUSD: 0.5, DayUSD: 2, MonthUSD: 20}
	if err := database.SaveAgentLimits(want); err != nil {
		t.Fatal(err)
	}
	if got, _ = database.GetAgentLimits(agentID); got != want {
		t.Errorf("got %+v want %+v", got, want)
	}
	want.RunTokens = 7
	_ = database.SaveAgentLimits(want)
	if got, _ = database.GetAgentLimits(agentID); got.RunTokens != 7 {
		t.Errorf("upsert did not update: %+v", got)
	}
	if err := database.SaveAgentLimits(db.AgentLimits{AgentID: agentID}); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = database.QueryRow(`SELECT COUNT(*) FROM agent_limits WHERE agent_id=?`, agentID).Scan(&n)
	if n != 0 {
		t.Error("all-zero save should delete the row")
	}
}

func TestAgentSpendSince(t *testing.T) {
	database, agentID, ws := runsTestDB(t)
	a := finishedRun(t, database, agentID, ws, db.RunOutcome{TotalTokens: 100, CostUSD: 0.25, CostReported: true})
	b := finishedRun(t, database, agentID, ws, db.RunOutcome{TotalTokens: 50, CostUSD: 0.1, CostReported: true})
	c := finishedRun(t, database, agentID, ws, db.RunOutcome{TotalTokens: 10})
	old := time.Now().UTC().Add(-48 * time.Hour).Format("2006-01-02 15:04:05")
	if _, err := database.Exec(`UPDATE agent_runs SET started_at=? WHERE id=?`, old, c); err != nil {
		t.Fatal(err)
	}
	_ = a
	_ = b
	since := time.Now().UTC().Add(-24 * time.Hour)
	s, err := database.AgentSpendSince(agentID, since)
	if err != nil {
		t.Fatal(err)
	}
	if s.Tokens != 150 || s.USD < 0.349 || s.USD > 0.351 || s.CostUnreported {
		t.Errorf("recent spend = %+v", s)
	}
	s, _ = database.AgentSpendSince(agentID, since.Add(-48*time.Hour))
	if s.Tokens != 160 || !s.CostUnreported {
		t.Errorf("full spend = %+v", s)
	}
}

func TestBudgetPauseChangedOnlyOnce(t *testing.T) {
	database, agentID, _ := runsTestDB(t)
	until := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	changed, err := database.SetAgentBudgetPause(agentID, until, "daily token limit")
	if err != nil || !changed {
		t.Fatalf("first = %v, %v", changed, err)
	}
	if changed, _ = database.SetAgentBudgetPause(agentID, until, "daily token limit"); changed {
		t.Error("identical pause reported changed")
	}
	a, err := database.GetAgent(agentID)
	if err != nil || a.BudgetPausedUntil == nil || !a.BudgetPausedUntil.Equal(until) || a.BudgetPauseReason != "daily token limit" {
		t.Fatalf("agent = %+v, %v", a, err)
	}
	if err := database.ClearAgentBudgetPause(agentID); err != nil {
		t.Fatal(err)
	}
	a, _ = database.GetAgent(agentID)
	if a.BudgetPausedUntil != nil || a.BudgetPauseReason != "" {
		t.Errorf("not cleared: %+v", a)
	}
}

func TestMarkLimitNoticeSentOncePerDay(t *testing.T) {
	database, agentID, _ := runsTestDB(t)
	if first, err := database.MarkLimitNoticeSent(agentID, "2026-11-09"); err != nil || !first {
		t.Fatalf("first = %v, %v", first, err)
	}
	if first, _ := database.MarkLimitNoticeSent(agentID, "2026-11-09"); first {
		t.Error("second same-day notice reported first")
	}
	if first, _ := database.MarkLimitNoticeSent(agentID, "2026-11-10"); !first {
		t.Error("next day should be first again")
	}
}

func TestFinishAgentRunRecordsLimited(t *testing.T) {
	database, agentID, ws := runsTestDB(t)
	id := finishedRun(t, database, agentID, ws, db.RunOutcome{ExitCode: -1, Limited: "run token limit"})
	got, err := database.GetAgentRun(id)
	if err != nil || got == nil || got.Limited != "run token limit" {
		t.Fatalf("GetAgentRun = %+v, %v", got, err)
	}
	listed, _ := database.ListAgentRuns(agentID, 10)
	if len(listed) != 1 || listed[0].Limited != "run token limit" {
		t.Errorf("ListAgentRuns lost limited: %+v", listed)
	}
}

func TestDeletingAgentCascadesLimits(t *testing.T) {
	database, agentID, _ := runsTestDB(t)
	_ = database.SaveAgentLimits(db.AgentLimits{AgentID: agentID, RunTokens: 1})
	if err := database.DeleteAgent(agentID); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = database.QueryRow(`SELECT COUNT(*) FROM agent_limits WHERE agent_id=?`, agentID).Scan(&n)
	if n != 0 {
		t.Error("limits survived agent delete")
	}
}
