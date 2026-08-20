package profile

import "time"

// ScheduleZone resolves the timezone an agent schedule should be STORED with,
// together with the matching location for computing its first next_run_at.
//
// It returns both so the two can never disagree. Storing a zone name while
// computing the first firing instant in a different location is the sort of
// mismatch that produces one wrong run — the one immediately after the schedule
// is saved — and then corrects itself, which makes it very hard to see and very
// easy to dismiss as a fluke.
//
// A workspace with no profile timezone yields ("", time.Local): the empty string
// is not "unknown", it is an explicit "evaluate in the host's local zone", which
// is exactly what every cron expression did before schedules carried a zone at
// all. Deliberately NOT LoadLocation, which returns UTC for an unset profile —
// using that here would silently re-time every agent on every install that never
// filled in a profile.
//
// An unparseable zone also yields ("", time.Local) rather than being stored as
// typed. Persisting a name that fails to load would leave the scheduler falling
// back to host-local on every tick while the row claimed otherwise, so the
// stored value would be a lie about what the schedule actually does.
func ScheduleZone(g Getter, workspaceID string) (string, *time.Location) {
	tz := Load(g, workspaceID).Timezone
	if tz == "" {
		return "", time.Local
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return "", time.Local
	}
	return tz, loc
}
