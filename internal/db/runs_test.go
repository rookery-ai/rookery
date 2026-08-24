package db_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
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

	if err := database.FinishAgentRun(runID, db.RunOutcome{Stdout: "ok", PromptTokens: 120, CompletionTokens: 80, TotalTokens: 200}); err != nil {
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
	if err := database.FinishAgentRun(doneID, db.RunOutcome{Stdout: "ok"}); err != nil {
		t.Fatalf("finish run: %v", err)
	}

	n, _, err := database.ReconcileStaleRuns()
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

// TestReconcileStaleRunsReportsInterruptedCronRuns is the crash-recovery half of
// reconcile: besides closing the row out, it reports which of the interrupted runs
// were CRON runs, so the scheduler can retry exactly those once on boot.
//
// The eligibility rule is the whole safety story, so it is asserted directly:
// only trigger='cron' is reported. A manual run has a human watching who can press
// the button again; a chat run's requester is long gone; and a 'cron-retry' row is
// itself the one retry a missed run gets — reporting it would let a run that
// crashes the server retry forever, once per boot.
func TestReconcileStaleRunsReportsInterruptedCronRuns(t *testing.T) {
	database, agentID, workspaceID := runsTestDB(t)

	open := func(trigger string) string {
		t.Helper()
		id := uuid.New().String()
		if err := database.CreateAgentRun(&db.AgentRun{ID: id, AgentID: agentID, WorkspaceID: workspaceID, Trigger: trigger}); err != nil {
			t.Fatalf("create %s run: %v", trigger, err)
		}
		return id
	}
	cronID := open("cron")
	open("manual")
	open("chat")
	open("cron-retry")

	// A cron run that FINISHED is not interrupted and must not be retried.
	finishedCron := open("cron")
	if err := database.FinishAgentRun(finishedCron, db.RunOutcome{Stdout: "ok"}); err != nil {
		t.Fatalf("finish run: %v", err)
	}

	n, interrupted, err := database.ReconcileStaleRuns()
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if n != 4 {
		t.Fatalf("expected 4 rows closed out (all open runs), got %d", n)
	}
	if len(interrupted) != 1 {
		t.Fatalf("expected exactly 1 retryable cron run, got %d: %+v", len(interrupted), interrupted)
	}
	got := interrupted[0]
	if got.RunID != cronID || got.AgentID != agentID || got.WorkspaceID != workspaceID {
		t.Fatalf("interrupted run = %+v, want run=%s agent=%s workspace=%s", got, cronID, agentID, workspaceID)
	}
}

// TestReconcileStaleRunsWithNothingOpenReportsNothing pins the ordinary boot: a
// clean shutdown leaves no open rows, so nothing is retried.
func TestReconcileStaleRunsWithNothingOpenReportsNothing(t *testing.T) {
	database, _, _ := runsTestDB(t)

	n, interrupted, err := database.ReconcileStaleRuns()
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if n != 0 || len(interrupted) != 0 {
		t.Fatalf("expected a no-op, got n=%d interrupted=%+v", n, interrupted)
	}
}

// TestConcurrentWritersDoNotHitSQLiteBusy pins the busy_timeout pragma.
//
// WAL permits exactly one writer at a time, and without busy_timeout the second
// one fails IMMEDIATELY with SQLITE_BUSY rather than waiting its turn. That is not
// a theoretical concern: the scheduler fires every overdue agent at once on boot,
// and each one writes its schedule times. The write failed, the error was only
// logged, and the run proceeded anyway — leaving the schedule still due, so the
// next poll ran the same agent all over again.
func TestConcurrentWritersDoNotHitSQLiteBusy(t *testing.T) {
	database, agentID, workspaceID := runsTestDB(t)

	const writers = 16
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	start := make(chan struct{})

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release them together, so the writes genuinely contend
			if err := database.CreateAgentRun(&db.AgentRun{
				ID: uuid.New().String(), AgentID: agentID, WorkspaceID: workspaceID, Trigger: "cron",
			}); err != nil {
				errs <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent write failed: %v", err)
	}

	runs, err := database.ListAgentRuns(agentID, writers+1)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != writers {
		t.Fatalf("expected %d rows written, got %d", writers, len(runs))
	}
}

// TestPragmasApplyToEveryPooledConnection is the reason the pragmas moved into
// the DSN. busy_timeout and foreign_keys are per-CONNECTION settings, and
// database/sql hands out a pool — so running them once via Exec configured
// whichever connection the pool happened to pick and left the rest at defaults.
// Foreign keys were therefore enforced only sometimes, silently. Held open
// together so the pool is forced to supply genuinely distinct connections.
func TestPragmasApplyToEveryPooledConnection(t *testing.T) {
	database, _, _ := runsTestDB(t)
	ctx := context.Background()

	const conns = 8
	held := make([]*sql.Conn, 0, conns)
	t.Cleanup(func() {
		for _, c := range held {
			c.Close()
		}
	})

	for i := 0; i < conns; i++ {
		conn, err := database.Conn(ctx)
		if err != nil {
			t.Fatalf("open connection %d: %v", i, err)
		}
		held = append(held, conn)

		var fk int
		if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
			t.Fatalf("read foreign_keys on connection %d: %v", i, err)
		}
		if fk != 1 {
			t.Fatalf("connection %d has foreign_keys=%d — FK enforcement is not pool-wide", i, fk)
		}

		var busy int
		if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busy); err != nil {
			t.Fatalf("read busy_timeout on connection %d: %v", i, err)
		}
		if busy == 0 {
			t.Fatalf("connection %d has busy_timeout=0 — a concurrent write there fails instead of waiting", i)
		}
	}
}
