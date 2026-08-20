package profile

import (
	"testing"
	"time"
)

type fakeGetter map[string]string

func (f fakeGetter) GetSetting(workspaceID, key string) (string, error) { return f[key], nil }

// The pairing is the point: the stored name and the location used to compute the
// first next_run_at must always describe the same zone. A mismatch produces
// exactly one wrong run — the one right after saving — and then self-corrects,
// which is the hardest kind of bug to believe a report of.
func TestScheduleZoneReturnsAMatchingPair(t *testing.T) {
	tz, loc := ScheduleZone(fakeGetter{"profile_timezone": "Europe/Skopje"}, "ws")
	if tz != "Europe/Skopje" {
		t.Fatalf("timezone = %q", tz)
	}
	if loc.String() != tz {
		t.Fatalf("stored zone %q and location %q disagree", tz, loc)
	}
}

// An unset profile must yield host-local, NOT UTC. LoadLocation returns UTC for
// this case, and reusing it would silently re-time every agent on every install
// that never filled in a profile.
func TestUnsetProfileYieldsHostLocalNotUTC(t *testing.T) {
	tz, loc := ScheduleZone(fakeGetter{}, "ws")
	if tz != "" {
		t.Errorf("an unset profile must store the empty string (meaning host-local), got %q", tz)
	}
	if loc != time.Local {
		t.Errorf("an unset profile must compute in host-local, got %v — using UTC here "+
			"re-times every existing agent on every install with no profile timezone", loc)
	}
}

// A zone that does not load must not be persisted as typed: the scheduler would
// fall back to host-local on every tick while the row claimed otherwise, so the
// stored value would misdescribe what the schedule actually does.
func TestUnparseableZoneIsNotStored(t *testing.T) {
	tz, loc := ScheduleZone(fakeGetter{"profile_timezone": "Not/AZone"}, "ws")
	if tz != "" {
		t.Errorf("an unloadable zone must not be stored, got %q", tz)
	}
	if loc != time.Local {
		t.Errorf("and it must compute in host-local, got %v", loc)
	}
}
