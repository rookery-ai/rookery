// Package scheduler polls for due agent schedules and fires agent runs.
// It uses robfig/cron to parse cron expressions and compute next-run times.
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/rookery-ai/rookery/internal/agentrunner"
	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/gateway"
	"github.com/rookery-ai/rookery/internal/secrets"
)

const pollInterval = 60 * time.Second

// maxConcurrentRuns bounds how many agent runs the scheduler may have in flight
// at once. Rookery is meant to run all day but is mostly installed on a laptop,
// so the common case is not a steady trickle: it is a machine opened after days
// offline, finding every schedule overdue in a single tick. Firing them all in
// parallel launches one coder subprocess per agent at exactly the moment the user
// has just opened their laptop. Capping delays the backlog; it never drops it.
//
// The cap is scheduler-local on purpose. A manual run from the web and a chat run
// are both a human waiting on a result, and neither arrives in a herd.
const maxConcurrentRuns = 3

// retryTrigger is the trigger recorded for the one retry a run interrupted
// mid-flight is given. It is load-bearing, not cosmetic: ReconcileStaleRuns
// reports only trigger='cron' rows, so a retry that is ITSELF interrupted is
// never retried again. Without that distinction an agent that takes the server
// down with it would be retried on every boot, forever.
const retryTrigger = "cron-retry"

// Sender delivers messages to users (satisfied by gateway.GatewayManager).
type Sender interface {
	SendToUser(workspaceID, text string) error
}

// AgentRunner runs one agent. Narrowed from *agentrunner.Runner so the scheduler's
// own logic — the concurrency cap, the retry pass — is testable without a coder,
// a vault or a subprocess. *agentrunner.Runner satisfies it.
type AgentRunner interface {
	Run(ctx context.Context, input agentrunner.RunInput) error
}

// Scheduler polls for due agent_schedules and fires them via the runner.
type Scheduler struct {
	db        *db.DB
	runner    AgentRunner
	sender    Sender // may be nil (cron output not delivered)
	systemKey []byte
	parser    cron.Parser
	sem       chan struct{}
	recovered []db.InterruptedRun
}

// New creates a Scheduler.
func New(database *db.DB, runner AgentRunner, systemKey []byte) *Scheduler {
	return &Scheduler{
		db:        database,
		runner:    runner,
		systemKey: systemKey,
		// Standard 5-field cron: minute hour dom month dow
		parser: cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow),
		sem:    make(chan struct{}, maxConcurrentRuns),
	}
}

// WithRecovered supplies the cron runs that were cut off mid-flight by the last
// shutdown, as reported by db.ReconcileStaleRuns at startup. Run retries them
// once before its first poll.
func (s *Scheduler) WithRecovered(runs []db.InterruptedRun) *Scheduler {
	s.recovered = runs
	return s
}

// WithSender attaches a Sender so cron-triggered agents can deliver output to users.
func (s *Scheduler) WithSender(sender Sender) *Scheduler {
	s.sender = sender
	return s
}

// Run starts the polling loop and blocks until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	slog.Info("scheduler: started", "interval", pollInterval)

	// Retry runs the last shutdown killed mid-flight, before the catch-up tick.
	// These are invisible to the tick below: fire() advances next_run_at before
	// the run executes, so an interrupted run's slot is already spent.
	s.RecoverInterrupted(ctx)

	// Fire immediately on start to catch any schedules that were due while
	// the server was down.
	s.tick(ctx)

	for {
		select {
		case <-ctx.Done():
			slog.Info("scheduler: stopped")
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Scheduler) tick(ctx context.Context) {
	now := time.Now()
	schedules, err := s.db.ListDueSchedules(now)
	if err != nil {
		slog.Error("scheduler: list due schedules", "err", err)
		return
	}

	for _, sched := range schedules {
		go s.fire(ctx, sched, now)
	}
}

func (s *Scheduler) fire(ctx context.Context, sched *db.AgentSchedule, firedAt time.Time) {
	// The expression is read in the SCHEDULE's zone, not the host's. An empty
	// Timezone (every pre-migration row, and every workspace with no profile
	// timezone) resolves to time.Local, which is exactly what this line did
	// before — see scheduleLocation.
	next, err := s.nextRunIn(sched.CronExpr, firedAt, scheduleLocation(sched.Timezone))
	if err != nil {
		slog.Error("scheduler: parse cron", "schedule_id", sched.ID, "expr", sched.CronExpr, "err", err)
		return
	}

	// Claim the slot by advancing the run times, so a second tick doesn't
	// double-fire.
	//
	// This MUST stay ahead of the semaphore acquire in execute(): under the
	// concurrency cap a schedule can sit in the queue across several ticks, and
	// a schedule whose times had not yet been advanced would be selected again
	// by every one of them.
	//
	// A FAILED claim means the slot was not taken, so running now guarantees the
	// next poll runs this same agent again — the schedule is still due. Skipping
	// leaves it due for that poll to pick up: late, rather than twice. This used
	// to log and run the agent anyway, which is how a transient SQLITE_BUSY
	// turned into a duplicate run.
	if err := s.db.UpdateScheduleRunTimes(sched.ID, firedAt, next); err != nil {
		slog.Error("scheduler: could not claim schedule slot — skipping this run, the next poll will retry",
			"schedule_id", sched.ID, "agent_id", sched.AgentID, "err", err)
		return
	}

	// A missing chat platform is NOT a reason to skip the run. This used to return early
	// on the rationale that "the agent cannot deliver output and running it wastes API
	// quota" — true when written, false since the inbox landed: the runner records every
	// delivered notification to the inbox (recordInbox), which has its own UI, unread
	// badge, and vault reflection. Skipping meant a workspace with no chat app connected
	// had NO scheduled agents run at all, which reads as the scheduler being broken.
	// The chat send remains best-effort inside the runner; the inbox always gets it.
	slog.Info("scheduler: firing agent", "agent_id", sched.AgentID, "user_id", sched.WorkspaceID, "next_run", next)

	s.execute(ctx, sched.AgentID, sched.WorkspaceID, "cron")
}

// RecoverInterrupted retries, once each, the cron runs the previous shutdown
// killed mid-flight (see db.ReconcileStaleRuns). It blocks until they finish.
//
// These runs are unreachable by the ordinary due query: fire() advances
// next_run_at BEFORE the run starts, so a run interrupted at 09:02 has already
// had its slot spent and the schedule now points at the next one. Without this
// pass the work is simply lost — the very case a laptop hits most, since closing
// the lid mid-run is far more likely than being off across a whole slot.
//
// The retry runs under the same concurrency cap as everything else: a boot that
// recovers a backlog is precisely the herd the cap exists to prevent. It is one
// attempt, never a loop — the retryTrigger comment explains what enforces that.
func (s *Scheduler) RecoverInterrupted(ctx context.Context) {
	runs := s.recovered
	s.recovered = nil
	if len(runs) == 0 {
		return
	}

	slog.Info("scheduler: retrying runs interrupted by the last shutdown", "count", len(runs))

	var wg sync.WaitGroup
	for _, r := range runs {
		wg.Add(1)
		go func(r db.InterruptedRun) {
			defer wg.Done()
			// Mirror the `a.active = 1` join in ListDueSchedules. An agent paused
			// or deleted since the interrupted run must stay stopped: a retry is
			// still a run, and pausing is the only control a user has to stop one.
			a, err := s.db.GetAgent(r.AgentID)
			if err != nil || a == nil || !a.Active {
				slog.Info("scheduler: skipping retry — agent is paused or gone",
					"agent_id", r.AgentID, "interrupted_run_id", r.RunID)
				return
			}
			slog.Info("scheduler: retrying interrupted run",
				"agent_id", r.AgentID, "user_id", r.WorkspaceID, "interrupted_run_id", r.RunID)
			s.execute(ctx, r.AgentID, r.WorkspaceID, retryTrigger)
		}(r)
	}
	wg.Wait()
}

// execute resolves the workspace's secrets and delivery hook, waits for a slot
// under the concurrency cap, and runs the agent.
//
// Callers must have already recorded whatever state stops the run being started
// a second time — the wait here can span several poll intervals.
func (s *Scheduler) execute(ctx context.Context, agentID, workspaceID, trigger string) {
	// Decrypt the user's stored master password so secrets are injected at run time.
	masterPw := ""
	if len(s.systemKey) > 0 {
		u, err := s.db.GetWorkspaceByID(workspaceID)
		if err == nil && u.EncryptedMasterPassword != "" {
			if pw, err := secrets.DecryptMasterPassword(u.EncryptedMasterPassword, s.systemKey); err == nil {
				masterPw = pw
			}
		}
	}
	if masterPw == "" {
		slog.Warn("scheduler: running agent without secrets — system key not configured or user has no master password",
			"agent_id", agentID, "user_id", workspaceID)
	}

	var sendFn agentrunner.SendFunc
	if s.sender != nil {
		sender := s.sender
		// Label the notification with the agent that produced it. A workspace
		// with several scheduled agents otherwise receives a stream of messages
		// with nothing to say which one is speaking. Looked up here rather than
		// inside the runner because the runner reuses SendOutput as a collector
		// for child-agent recursion, whose text goes into an LLM prompt.
		agentName := ""
		if a, err := s.db.GetAgent(agentID); err == nil && a != nil {
			agentName = a.Name
		}
		sendFn = func(msg string) {
			_ = sender.SendToUser(workspaceID, gateway.AgentPrefixed(agentName, msg))
		}
	}

	// Wait for a slot. A shutdown while queued drops the run rather than starting
	// it on the way out — it would only be killed mid-flight seconds later, and
	// that is the failure this whole change exists to avoid manufacturing.
	//
	// Cancellation is checked on BOTH sides of the acquire, and neither check is
	// redundant. A bare two-case select is not enough: once the last in-flight run
	// frees a slot, a cancelled context leaves both cases ready at the same time
	// and select chooses uniformly at random, so roughly half the queued backlog
	// starts anyway. Measured, not theorised — the test caught exactly that.
	if ctx.Err() != nil {
		return
	}
	select {
	case s.sem <- struct{}{}:
		if ctx.Err() != nil {
			<-s.sem
			slog.Info("scheduler: dropping queued run — shutting down", "agent_id", agentID, "trigger", trigger)
			return
		}
	case <-ctx.Done():
		slog.Info("scheduler: dropping queued run — shutting down", "agent_id", agentID, "trigger", trigger)
		return
	}
	defer func() { <-s.sem }()

	if err := s.runner.Run(ctx, agentrunner.RunInput{
		AgentID:     agentID,
		WorkspaceID: workspaceID,
		Trigger:     trigger,
		MasterPw:    masterPw,
		SendOutput:  sendFn,
	}); err != nil {
		slog.Error("scheduler: agent run failed", "agent_id", agentID, "trigger", trigger, "err", err)
	}
}

func (s *Scheduler) nextRun(expr string, from time.Time) (time.Time, error) {
	return s.nextRunIn(expr, from, time.Local)
}

// nextRunIn computes the next firing instant with the expression read in loc.
//
// The zone is applied by converting `from` into it before asking the parsed
// schedule for its next slot: cron.Schedule.Next derives wall-clock fields from
// the location of the time it is handed, so passing a UTC instant reads "0 8"
// as 08:00 UTC no matter what the schedule intended. Converting first is what
// makes "08:00" mean 08:00 where the OWNER lives.
//
// The returned instant is absolute, so callers may store it in UTC as before —
// only the interpretation of the expression changes, never the representation.
func (s *Scheduler) nextRunIn(expr string, from time.Time, loc *time.Location) (time.Time, error) {
	schedule, err := s.parser.Parse(expr)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse cron %q: %w", expr, err)
	}
	if loc == nil {
		loc = time.Local
	}
	return schedule.Next(from.In(loc)), nil
}
