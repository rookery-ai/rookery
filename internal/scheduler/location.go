package scheduler

import (
	"log/slog"
	"time"
)

// scheduleLocation resolves the zone a schedule's cron expression is evaluated
// in.
//
// An EMPTY timezone means the host's local zone, and that choice is the entire
// safety of the per-schedule timezone column. Before it existed, tick() passed a
// bare time.Now() and every expression was read in whatever zone the host ran
// in. If "unset" resolved to UTC instead — the obvious default, and what
// profile.LoadLocation returns — then shipping this would silently re-time every
// schedule on every install that never filled in a profile timezone: a two-hour
// jump, arriving with no error and no log line, on agents that had been correct
// for months. time.Local reproduces today's behaviour exactly, so an install
// that does not opt in sees no change whatsoever.
//
// An unparseable zone falls back the same way, and for the same reason: the
// fallback must be the behaviour the schedule ALREADY had, so a typo in a
// profile cannot quietly move a working agent. It is logged rather than
// returned as an error because a scheduler that refuses to fire is a worse
// outcome than one that fires where it always did.
func scheduleLocation(tz string) *time.Location {
	if tz == "" {
		return time.Local
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		slog.Warn("scheduler: unknown schedule timezone, using host local",
			"timezone", tz, "err", err)
		return time.Local
	}
	return loc
}
