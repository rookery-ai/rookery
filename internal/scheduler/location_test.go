package scheduler

import (
	"testing"
	"time"
)

// The empty-string case is the entire safety of the per-schedule timezone.
//
// Before this, cron was evaluated in the HOST's zone. If an unset timezone
// resolved to UTC — the obvious choice, and what profile.LoadLocation returns —
// then every schedule on every install that never filled in a profile would
// silently shift the moment this shipped: a two-hour jump on agents that had
// been correct for months, with no error and no log line. time.Local reproduces
// today's behaviour exactly, so not opting in costs nothing.
func TestEmptyTimezoneMeansHostLocal(t *testing.T) {
	if got := scheduleLocation(""); got != time.Local {
		t.Fatalf("an unset schedule timezone must mean the host's local zone (today's\n"+
			"behaviour), not UTC — otherwise shipping this re-times every existing\n"+
			"schedule on every install that never set a profile timezone. got %v", got)
	}
}

func TestExplicitTimezoneIsHonoured(t *testing.T) {
	loc := scheduleLocation("Europe/Skopje")
	if loc == time.Local && time.Local.String() != "Europe/Skopje" {
		t.Fatal("an explicit timezone must be loaded, not ignored")
	}
	if loc.String() != "Europe/Skopje" {
		t.Fatalf("scheduleLocation(Europe/Skopje) = %v", loc)
	}
}

// An unparseable zone falls back to host-local rather than erroring or using
// UTC: the fallback must be the behaviour the schedule ALREADY had, or a typo
// in a profile silently re-times a working agent.
func TestUnparseableTimezoneFallsBackToHostLocal(t *testing.T) {
	if got := scheduleLocation("Not/AZone"); got != time.Local {
		t.Fatalf("a bad zone must fall back to the host's local zone, got %v", got)
	}
}

// The behavioural claim the column exists for: the SAME expression resolves to
// different instants in different zones, and the empty case matches the bare
// time.Now() the scheduler used before.
func TestNextRunHonoursTheScheduleZone(t *testing.T) {
	s := New(nil, nil, nil)

	// 08:00 daily. Evaluate from a fixed instant.
	from := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)

	utcNext, err := s.nextRunIn("0 8 * * *", from, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	skopje, err := time.LoadLocation("Europe/Skopje")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	skNext, err := s.nextRunIn("0 8 * * *", from, skopje)
	if err != nil {
		t.Fatal(err)
	}

	if utcNext.Equal(skNext) {
		t.Fatal("08:00 in UTC and 08:00 in Europe/Skopje are different instants; " +
			"if these match, the zone is being ignored and the column does nothing")
	}
	if h := skNext.In(skopje).Hour(); h != 8 {
		t.Errorf("a schedule with an explicit zone must fire at 08:00 IN THAT ZONE, got %d", h)
	}
	if h := utcNext.In(time.UTC).Hour(); h != 8 {
		t.Errorf("UTC schedule should fire at 08:00 UTC, got %d", h)
	}
}

// nextRun (the no-zone entry point the scheduler still uses for its own
// bookkeeping) must agree with nextRunIn(time.Local) — one behaviour, two
// spellings, so a future edit cannot make them disagree.
func TestNextRunMatchesExplicitHostLocal(t *testing.T) {
	s := New(nil, nil, nil)
	from := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)

	a, err := s.nextRunIn("*/15 * * * *", from, scheduleLocation(""))
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.nextRunIn("*/15 * * * *", from, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Equal(b) {
		t.Fatalf("empty zone must be identical to host-local: %v vs %v", a, b)
	}
}
