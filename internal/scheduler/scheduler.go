// Package scheduler polls for due agent schedules and fires agent runs.
// It uses robfig/cron to parse cron expressions and compute next-run times.
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ilijad1/rookery/internal/agentrunner"
	"github.com/ilijad1/rookery/internal/db"
	"github.com/ilijad1/rookery/internal/secrets"
	"github.com/robfig/cron/v3"
)

const pollInterval = 60 * time.Second

// Sender delivers messages to users (satisfied by gateway.GatewayManager).
type Sender interface {
	SendToUser(workspaceID, text string) error
}

// Scheduler polls for due agent_schedules and fires them via the runner.
type Scheduler struct {
	db        *db.DB
	runner    *agentrunner.Runner
	sender    Sender // may be nil (cron output not delivered)
	systemKey []byte
	parser    cron.Parser
}

// New creates a Scheduler.
func New(database *db.DB, runner *agentrunner.Runner, systemKey []byte) *Scheduler {
	return &Scheduler{
		db:        database,
		runner:    runner,
		systemKey: systemKey,
		// Standard 5-field cron: minute hour dom month dow
		parser: cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow),
	}
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

	// A missing chat platform is NOT a reason to skip the run. This used to return early
	// on the rationale that "the agent cannot deliver output and running it wastes API
	// quota" — true when written, false since the inbox landed: the runner records every
	// delivered notification to the inbox (recordInbox), which has its own UI, unread
	// badge, and vault reflection. Skipping meant a workspace with no chat app connected
	// had NO scheduled agents run at all, which reads as the scheduler being broken.
	// The chat send remains best-effort inside the runner; the inbox always gets it.
	slog.Info("scheduler: firing agent", "agent_id", sched.AgentID, "user_id", sched.WorkspaceID, "next_run", next)

	// Decrypt the user's stored master password so secrets are injected at run time.
	masterPw := ""
	if len(s.systemKey) > 0 {
		u, err := s.db.GetWorkspaceByID(sched.WorkspaceID)
		if err == nil && u.EncryptedMasterPassword != "" {
			if pw, err := secrets.DecryptMasterPassword(u.EncryptedMasterPassword, s.systemKey); err == nil {
				masterPw = pw
			}
		}
	}
	if masterPw == "" {
		slog.Warn("scheduler: running agent without secrets — system key not configured or user has no master password",
			"agent_id", sched.AgentID, "user_id", sched.WorkspaceID)
	}

	var sendFn agentrunner.SendFunc
	if s.sender != nil {
		sender := s.sender
		workspaceID := sched.WorkspaceID
		sendFn = func(msg string) {
			_ = sender.SendToUser(workspaceID, msg)
		}
	}

	if err := s.runner.Run(ctx, agentrunner.RunInput{
		AgentID:     sched.AgentID,
		WorkspaceID: sched.WorkspaceID,
		Trigger:     "cron",
		MasterPw:    masterPw,
		SendOutput:  sendFn,
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
