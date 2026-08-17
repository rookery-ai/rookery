package scheduler

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rookery-ai/rookery/internal/agentrunner"
	"github.com/rookery-ai/rookery/internal/db"
)

// fakeRunner stands in for *agentrunner.Runner. It records every RunInput it is
// handed, tracks how many runs are in flight at once, and blocks in Run until
// release is closed — which is what lets a test observe the concurrency cap
// holding rather than merely inferring it from a finished batch.
type fakeRunner struct {
	mu       sync.Mutex
	started  []agentrunner.RunInput
	inFlight int
	peak     int
	release  chan struct{} // nil means "return immediately"
}

func (f *fakeRunner) Run(ctx context.Context, in agentrunner.RunInput) error {
	f.mu.Lock()
	f.started = append(f.started, in)
	f.inFlight++
	if f.inFlight > f.peak {
		f.peak = f.inFlight
	}
	release := f.release
	f.mu.Unlock()

	if release != nil {
		<-release
	}

	f.mu.Lock()
	f.inFlight--
	f.mu.Unlock()
	return nil
}

func (f *fakeRunner) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.started)
}

func (f *fakeRunner) peakConcurrency() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.peak
}

func (f *fakeRunner) triggers() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.started))
	for _, in := range f.started {
		out = append(out, in.Trigger)
	}
	return out
}

// waitFor polls cond until it holds or the deadline passes. Used instead of a
// fixed sleep so a slow machine does not turn into a flaky test.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// schedulerTestDB opens a migrated DB with one workspace and returns its id.
func schedulerTestDB(t *testing.T) (*db.DB, string) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	workspaceID := uuid.New().String()
	if err := database.CreateWorkspace(&db.Workspace{ID: workspaceID, Name: "tester"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	return database, workspaceID
}

// seedDueAgents creates n active agents each with an enabled schedule already due.
func seedDueAgents(t *testing.T, database *db.DB, workspaceID string, n int) []string {
	t.Helper()
	past := time.Now().Add(-time.Hour)
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		agentID := uuid.New().String()
		if err := database.CreateAgent(&db.Agent{ID: agentID, WorkspaceID: workspaceID, Name: "agent-" + agentID[:8], Active: true}); err != nil {
			t.Fatalf("create agent: %v", err)
		}
		if err := database.UpsertAgentSchedule(&db.AgentSchedule{
			ID: uuid.New().String(), AgentID: agentID, WorkspaceID: workspaceID,
			CronExpr: "*/5 * * * *", NextRunAt: &past,
		}); err != nil {
			t.Fatalf("upsert schedule: %v", err)
		}
		ids = append(ids, agentID)
	}
	return ids
}

// TestTickCapsConcurrentRuns is the boot-herd fix. A laptop opened after days
// offline finds every schedule overdue at once; firing them all in parallel
// launches one coder subprocess per agent on a machine the user has just opened.
// The cap holds the rest in a queue instead — and every one of them still runs.
func TestTickCapsConcurrentRuns(t *testing.T) {
	database, workspaceID := schedulerTestDB(t)
	total := maxConcurrentRuns + 5
	seedDueAgents(t, database, workspaceID, total)

	runner := &fakeRunner{release: make(chan struct{})}
	s := New(database, runner, nil)

	s.tick(context.Background())

	// Fill the cap, then hold: peak must never exceed it while work is queued.
	waitFor(t, "the cap to fill", func() bool { return runner.count() >= maxConcurrentRuns })
	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
		if got := runner.peakConcurrency(); got > maxConcurrentRuns {
			t.Fatalf("peak concurrency %d exceeded the cap of %d", got, maxConcurrentRuns)
		}
		time.Sleep(2 * time.Millisecond)
	}
	if got := runner.count(); got > maxConcurrentRuns {
		t.Fatalf("%d runs started while only %d may run at once", got, maxConcurrentRuns)
	}

	// Draining the queue must still run every overdue agent — capping delays
	// work, it never drops it.
	close(runner.release)
	waitFor(t, "every queued run to finish", func() bool { return runner.count() >= total })
	if got := runner.count(); got != total {
		t.Fatalf("%d runs started for %d schedules", got, total)
	}
	if got := runner.peakConcurrency(); got > maxConcurrentRuns {
		t.Fatalf("peak concurrency %d exceeded the cap of %d", got, maxConcurrentRuns)
	}
}

// TestQueuedSchedulesDoNotDoubleFire pins the ORDER of the two steps in fire():
// run times are advanced before the semaphore is acquired, so a schedule waiting
// in the queue is no longer due and the next tick cannot pick it up again.
// Acquiring first would make every 60s tick re-enqueue the whole backlog.
func TestQueuedSchedulesDoNotDoubleFire(t *testing.T) {
	database, workspaceID := schedulerTestDB(t)
	total := maxConcurrentRuns + 3
	seedDueAgents(t, database, workspaceID, total)

	runner := &fakeRunner{release: make(chan struct{})}
	s := New(database, runner, nil)

	s.tick(context.Background())
	waitFor(t, "the cap to fill", func() bool { return runner.count() >= maxConcurrentRuns })

	// The property itself: once a tick has claimed a schedule, that schedule is no
	// longer due — even though most of them are still sitting in the queue unrun.
	// Waited for rather than slept on, because fire() advances the times from its
	// own goroutine and the test must not race it.
	waitFor(t, "every claimed schedule to stop being due", func() bool {
		due, err := database.ListDueSchedules(time.Now())
		return err == nil && len(due) == 0
	})

	// So a second tick while the backlog is queued finds nothing to do.
	s.tick(context.Background())

	close(runner.release)
	waitFor(t, "the backlog to drain", func() bool { return runner.count() >= total })
	time.Sleep(100 * time.Millisecond)
	if got := runner.count(); got != total {
		t.Fatalf("%d runs started for %d schedules — a queued schedule fired twice", got, total)
	}
}

// TestTickStopsWhenContextIsCancelled proves a shutdown while the queue is full
// drops the queued work rather than running it on the way out. A laptop lid
// closing is exactly this case.
func TestTickStopsWhenContextIsCancelled(t *testing.T) {
	database, workspaceID := schedulerTestDB(t)
	seedDueAgents(t, database, workspaceID, maxConcurrentRuns+4)

	runner := &fakeRunner{release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	s := New(database, runner, nil)

	s.tick(ctx)
	waitFor(t, "the cap to fill", func() bool { return runner.count() >= maxConcurrentRuns })

	cancel()
	close(runner.release)
	time.Sleep(150 * time.Millisecond)

	if got := runner.count(); got > maxConcurrentRuns {
		t.Fatalf("%d runs started after cancellation; queued work should have been dropped at %d", got, maxConcurrentRuns)
	}
}

// TestRecoverInterruptedRetriesEachRunOnce is the mid-run-loss fix. fire()
// advances next_run_at before the run executes, so a run killed mid-flight is
// never picked up by the ordinary due query — it has to be retried explicitly.
func TestRecoverInterruptedRetriesEachRunOnce(t *testing.T) {
	database, workspaceID := schedulerTestDB(t)
	agents := seedDueAgents(t, database, workspaceID, 2)

	runner := &fakeRunner{}
	s := New(database, runner, nil).WithRecovered([]db.InterruptedRun{
		{RunID: uuid.New().String(), AgentID: agents[0], WorkspaceID: workspaceID},
		{RunID: uuid.New().String(), AgentID: agents[1], WorkspaceID: workspaceID},
	})

	s.RecoverInterrupted(context.Background())

	if got := runner.count(); got != 2 {
		t.Fatalf("expected 2 retries, got %d", got)
	}
	for _, tr := range runner.triggers() {
		if tr != "cron-retry" {
			t.Fatalf("retry ran with trigger %q, want %q — the trigger IS the "+
				"once-only guard, so losing it lets a crashing agent retry forever", tr, "cron-retry")
		}
	}

	// Retries are one-shot: a second call has nothing left to do.
	s.RecoverInterrupted(context.Background())
	if got := runner.count(); got != 2 {
		t.Fatalf("recovery repeated itself: %d runs after a second call, want 2", got)
	}
}

// TestRecoverInterruptedSkipsPausedAndDeletedAgents keeps the retry pass in step
// with the ordinary due query, which joins `agents a ON ... AND a.active = 1`.
// An agent paused (or deleted) between the interrupted run and the next boot must
// stay paused — a retry is still a run, and pausing an agent is the one control a
// user has for stopping one.
func TestRecoverInterruptedSkipsPausedAndDeletedAgents(t *testing.T) {
	database, workspaceID := schedulerTestDB(t)
	agents := seedDueAgents(t, database, workspaceID, 2)
	active, paused := agents[0], agents[1]

	if err := database.SetAgentActive(paused, false); err != nil {
		t.Fatalf("pause agent: %v", err)
	}

	runner := &fakeRunner{}
	s := New(database, runner, nil).WithRecovered([]db.InterruptedRun{
		{RunID: uuid.New().String(), AgentID: active, WorkspaceID: workspaceID},
		{RunID: uuid.New().String(), AgentID: paused, WorkspaceID: workspaceID},
		{RunID: uuid.New().String(), AgentID: uuid.New().String(), WorkspaceID: workspaceID}, // deleted
	})

	s.RecoverInterrupted(context.Background())

	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.started) != 1 {
		t.Fatalf("expected only the active agent retried, got %d runs", len(runner.started))
	}
	if got := runner.started[0].AgentID; got != active {
		t.Fatalf("retried agent %s, want the active one %s", got, active)
	}
}

// TestRecoverInterruptedRespectsTheConcurrencyCap keeps the two fixes from
// undoing each other: a boot recovering a backlog of interrupted runs is the
// same herd the cap exists to prevent.
func TestRecoverInterruptedRespectsTheConcurrencyCap(t *testing.T) {
	database, workspaceID := schedulerTestDB(t)
	total := maxConcurrentRuns + 4
	agents := seedDueAgents(t, database, workspaceID, total)

	recovered := make([]db.InterruptedRun, 0, total)
	for _, agentID := range agents {
		recovered = append(recovered, db.InterruptedRun{
			RunID: uuid.New().String(), AgentID: agentID, WorkspaceID: workspaceID,
		})
	}

	runner := &fakeRunner{release: make(chan struct{})}
	s := New(database, runner, nil).WithRecovered(recovered)

	done := make(chan struct{})
	go func() { s.RecoverInterrupted(context.Background()); close(done) }()

	waitFor(t, "the cap to fill", func() bool { return runner.count() >= maxConcurrentRuns })
	time.Sleep(100 * time.Millisecond)
	if got := runner.peakConcurrency(); got > maxConcurrentRuns {
		t.Fatalf("recovery peak concurrency %d exceeded the cap of %d", got, maxConcurrentRuns)
	}

	close(runner.release)
	<-done
	if got := runner.count(); got != total {
		t.Fatalf("expected all %d interrupted runs retried, got %d", total, got)
	}
}

// TestRecoverInterruptedWithNothingToDo pins the ordinary boot: a clean shutdown
// recovers nothing and starts no runs.
func TestRecoverInterruptedWithNothingToDo(t *testing.T) {
	database, workspaceID := schedulerTestDB(t)
	seedDueAgents(t, database, workspaceID, 1)

	runner := &fakeRunner{}
	New(database, runner, nil).RecoverInterrupted(context.Background())

	if got := runner.count(); got != 0 {
		t.Fatalf("expected no retries on a clean boot, got %d", got)
	}
}
