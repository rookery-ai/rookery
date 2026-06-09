// Package scheduler polls for due agent schedules and fires agent runs.
// It uses robfig/cron to parse cron expressions and compute next-run times.
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/ilijad1/simple-agents/internal/agentrunner"
	"github.com/ilijad1/simple-agents/internal/db"
)

const pollInterval = 60 * time.Second

// Scheduler polls for due agent_schedules and fires them via the runner.
type Scheduler struct {
	db     *db.DB
	runner *agentrunner.Runner
	parser cron.Parser
}

// New creates a Scheduler.
func New(database *db.DB, runner *agentrunner.Runner) *Scheduler {
	return &Scheduler{
		db:     database,
		runner: runner,
		// Standard 5-field cron: minute hour dom month dow
		parser: cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow),
	}
}

// Run starts the polling loop and blocks until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	slog.Info("scheduler: started", "interval", pollInterval)

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
	next, err := s.nextRun(sched.CronExpr, firedAt)
	if err != nil {
		slog.Error("scheduler: parse cron", "schedule_id", sched.ID, "expr", sched.CronExpr, "err", err)
		return
	}

	// Update run times immediately so a second tick doesn't double-fire.
	if err := s.db.UpdateScheduleRunTimes(sched.ID, firedAt, next); err != nil {
		slog.Error("scheduler: update schedule times", "schedule_id", sched.ID, "err", err)
	}

	slog.Info("scheduler: firing agent", "agent_id", sched.AgentID, "user_id", sched.UserID, "next_run", next)

	if err := s.runner.Run(ctx, agentrunner.RunInput{
		AgentID: sched.AgentID,
		UserID:  sched.UserID,
		Trigger: "cron",
		// MasterPw is empty for cron runs; agents using ${SECRETS} must have
		// their secrets pre-injected via env vars from the runner's env.
		// Full secret injection via master password requires Phase 8 session storage.
		MasterPw:   "",
		SendOutput: nil, // cron runs have no direct chat send target
	}); err != nil {
		slog.Error("scheduler: agent run failed", "agent_id", sched.AgentID, "err", err)
	}
}

func (s *Scheduler) nextRun(expr string, from time.Time) (time.Time, error) {
	schedule, err := s.parser.Parse(expr)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse cron %q: %w", expr, err)
	}
	return schedule.Next(from), nil
}
