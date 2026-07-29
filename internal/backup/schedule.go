package backup

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

// pollInterval is how often the ticker checks whether a run is due. One minute
// is plenty for a daily/weekly cadence and keeps the goroutine cheap.
const pollInterval = time.Minute

// Scheduler fires backup runs on the owner's cadence.
//
// It is deliberately NOT part of internal/scheduler: that one polls
// agent_schedules, whose rows are foreign-keyed to a workspace. Backup is
// owner-level and belongs to no workspace, so it gets its own ticker rather
// than a fake workspace row.
type Scheduler struct {
	store     SettingStore
	db        *sql.DB
	dataDir   string
	systemKey []byte
}

func NewScheduler(store SettingStore, database *sql.DB, dataDir string, systemKey []byte) *Scheduler {
	return &Scheduler{store: store, db: database, dataDir: dataDir, systemKey: systemKey}
}

// NextRun computes the next scheduled time strictly after from, in server local
// time. The owner has no timezone in the schema — workspaces have profiles, the
// owner does not — so server local time is the honest choice, and the UI says so.
func NextRun(c *Config, from time.Time) time.Time {
	loc := from.Location()
	candidate := time.Date(from.Year(), from.Month(), from.Day(), c.Hour, 0, 0, 0, loc)

	switch c.Schedule {
	case ScheduleWeekly:
		delta := (c.Weekday - int(candidate.Weekday()) + 7) % 7
		candidate = candidate.AddDate(0, 0, delta)
		if !candidate.After(from) {
			candidate = candidate.AddDate(0, 0, 7)
		}
	default: // daily
		if !candidate.After(from) {
			candidate = candidate.AddDate(0, 0, 1)
		}
	}
	return candidate
}

// Run polls until ctx is cancelled, firing when a run is due.
//
// Missed runs collapse: a server that was down across several scheduled times
// finds NextRunAt in the past on boot, runs ONCE, and reschedules forward from
// now. It never replays every missed slot.
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c, err := LoadConfig(s.store, s.systemKey)
			if err != nil || !c.Enabled {
				continue
			}
			now := time.Now()
			if c.NextRunAt.IsZero() {
				c.NextRunAt = NextRun(c, now)
				_ = SaveConfig(s.store, s.systemKey, c)
				continue
			}
			if now.Before(c.NextRunAt) {
				continue
			}
			if _, err := s.RunOnce(ctx); err != nil {
				slog.Error("scheduled backup failed", "error", err)
			}
		}
	}
}

// RunOnce takes one snapshot immediately, applies retention, and records the
// outcome. It is shared by the ticker and the "Back up now" button.
func (s *Scheduler) RunOnce(ctx context.Context) (string, error) {
	c, err := LoadConfig(s.store, s.systemKey)
	if err != nil {
		return "", err
	}

	name, runErr := s.run(ctx, c)

	c.LastRunAt = time.Now()
	c.NextRunAt = NextRun(c, time.Now())
	if runErr != nil {
		c.LastStatus = "error"
		c.LastError = runErr.Error()
	} else {
		c.LastStatus = "ok"
		c.LastError = ""
	}
	if err := SaveConfig(s.store, s.systemKey, c); err != nil {
		slog.Error("could not record backup status", "error", err)
	}
	return name, runErr
}

func (s *Scheduler) run(ctx context.Context, c *Config) (string, error) {
	pass, err := c.Passphrase(s.systemKey)
	if err != nil {
		return "", fmt.Errorf("backup: %w", err)
	}
	dest, err := c.BuildDestination(s.systemKey)
	if err != nil {
		return "", err
	}

	name, err := Snapshot(ctx, Options{
		DB: s.db, DataDir: s.dataDir,
		SystemKey: s.systemKey, Passphrase: pass, Destination: dest,
	})
	if err != nil {
		return "", err
	}

	// Record the size for the settings banner before pruning.
	if entries, err := dest.List(ctx); err == nil {
		for _, e := range entries {
			if e.Name == name {
				c.LastSize = e.Size
			}
		}
	}

	if deleted, err := Prune(ctx, dest, c.Retention); err != nil {
		// Retention failing does not invalidate a snapshot that was written.
		slog.Warn("backup retention failed", "error", err)
	} else if len(deleted) > 0 {
		slog.Info("pruned old snapshots", "count", len(deleted))
	}
	return name, nil
}
