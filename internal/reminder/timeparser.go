package reminder

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ParseNaturalTime parses human-written time expressions into an absolute time.
// now is the reference point; loc is the user's timezone (use time.Local if unknown).
//
// Supported patterns (case-insensitive):
//
//	in N minutes/hours/days/weeks    (also "a"/"an" and English word numbers)
//	N minutes/hours/days/weeks [from now]
//	tomorrow [at TIME]
//	today at TIME
//	next <weekday> [at TIME]
//	at TIME                          (today, or tomorrow if time has already passed)
//
// TIME formats: 3pm, 3:30pm, 15:30, noon, midnight
func ParseNaturalTime(text string, now time.Time, loc *time.Location) (time.Time, error) {
	if loc == nil {
		loc = time.UTC
	}
	s := strings.ToLower(strings.TrimSpace(text))

	// "in N unit" or "in a/an unit"
	if t, ok := tryInDuration(s, now); ok {
		return t, nil
	}

	// "N unit [from now]"
	if t, ok := tryBareUnit(s, now); ok {
		return t, nil
	}

	// "tomorrow [at TIME]"
	if t, ok := tryTomorrow(s, now, loc); ok {
		return t, nil
	}

	// "today at TIME"
	if t, ok := tryToday(s, now, loc); ok {
		return t, nil
	}

	// "next <weekday> [at TIME]"
	if t, ok := tryNextWeekday(s, now, loc); ok {
		return t, nil
	}

	// "at TIME" — today if future, tomorrow if past
	if t, ok := tryAtTime(s, now, loc); ok {
		return t, nil
	}

	return time.Time{}, fmt.Errorf(`unrecognized time expression %q; try: "in 10 minutes", "tomorrow at 3pm", "next Tuesday"`, text)
}

// ── pattern matchers ───────────────────────────────────────────────────────

var reInDuration = regexp.MustCompile(
	`^in\s+(a|an|\d+|[a-z]+)\s+(minute|minutes|min|mins|hour|hours|hr|hrs|day|days|week|weeks)`)

func tryInDuration(s string, now time.Time) (time.Time, bool) {
	m := reInDuration.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}
	n, ok := parseNumber(m[1])
	if !ok {
		return time.Time{}, false
	}
	d := unitDuration(m[2], n)
	if d == 0 {
		return time.Time{}, false
	}
	return now.Add(d), true
}

var reBareUnit = regexp.MustCompile(
	`^(\d+|[a-z]+)\s+(minute|minutes|min|mins|hour|hours|hr|hrs|day|days|week|weeks)(\s+from\s+now)?$`)

func tryBareUnit(s string, now time.Time) (time.Time, bool) {
	m := reBareUnit.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}
	n, ok := parseNumber(m[1])
	if !ok {
		return time.Time{}, false
	}
	d := unitDuration(m[2], n)
	if d == 0 {
		return time.Time{}, false
	}
	return now.Add(d), true
}

var reTomorrow = regexp.MustCompile(`^tomorrow(\s+at\s+(.+))?$`)

func tryTomorrow(s string, now time.Time, loc *time.Location) (time.Time, bool) {
	m := reTomorrow.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}
	base := midnight(now.In(loc).Add(24 * time.Hour))
	if m[2] != "" {
		h, min, ok := parseTimeOfDay(strings.TrimSpace(m[2]))
		if !ok {
			return time.Time{}, false
		}
		base = base.Add(time.Duration(h)*time.Hour + time.Duration(min)*time.Minute)
	}
	return base, true
}

var reToday = regexp.MustCompile(`^today\s+at\s+(.+)$`)

func tryToday(s string, now time.Time, loc *time.Location) (time.Time, bool) {
	m := reToday.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}
	h, min, ok := parseTimeOfDay(strings.TrimSpace(m[1]))
	if !ok {
		return time.Time{}, false
	}
	t := midnight(now.In(loc)).Add(time.Duration(h)*time.Hour + time.Duration(min)*time.Minute)
	if t.Before(now) {
		t = t.Add(24 * time.Hour)
	}
	return t, true
}

var weekdays = map[string]time.Weekday{
	"sunday":    time.Sunday,
	"monday":    time.Monday,
	"tuesday":   time.Tuesday,
	"wednesday": time.Wednesday,
	"thursday":  time.Thursday,
	"friday":    time.Friday,
	"saturday":  time.Saturday,
	"sun":       time.Sunday,
	"mon":       time.Monday,
	"tue":       time.Tuesday,
	"wed":       time.Wednesday,
	"thu":       time.Thursday,
	"fri":       time.Friday,
	"sat":       time.Saturday,
}

var reNextWeekday = regexp.MustCompile(`^(?:next\s+)?([a-z]+)(\s+at\s+(.+))?$`)

func tryNextWeekday(s string, now time.Time, loc *time.Location) (time.Time, bool) {
	m := reNextWeekday.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}
	wd, ok := weekdays[m[1]]
	if !ok {
		return time.Time{}, false
	}
	base := nextWeekday(now.In(loc), wd)
	if m[3] != "" {
		h, min, ok := parseTimeOfDay(strings.TrimSpace(m[3]))
		if !ok {
			return time.Time{}, false
		}
		base = midnight(base).Add(time.Duration(h)*time.Hour + time.Duration(min)*time.Minute)
	}
	return base, true
}

var reAtTime = regexp.MustCompile(`^at\s+(.+)$`)

func tryAtTime(s string, now time.Time, loc *time.Location) (time.Time, bool) {
	m := reAtTime.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}
	h, min, ok := parseTimeOfDay(strings.TrimSpace(m[1]))
	if !ok {
		return time.Time{}, false
	}
	t := midnight(now.In(loc)).Add(time.Duration(h)*time.Hour + time.Duration(min)*time.Minute)
	if !t.After(now) {
		t = t.Add(24 * time.Hour)
	}
	return t, true
}

// ── helpers ────────────────────────────────────────────────────────────────

// parseTimeOfDay parses strings like "3pm", "3:30pm", "15:30", "noon", "midnight".
func parseTimeOfDay(s string) (hour, min int, ok bool) {
	switch s {
	case "noon":
		return 12, 0, true
	case "midnight":
		return 0, 0, true
	}

	// Try HH:MM[am/pm]
	re12 := regexp.MustCompile(`^(\d{1,2})(?::(\d{2}))?\s*(am|pm)$`)
	if m := re12.FindStringSubmatch(s); m != nil {
		h, _ := strconv.Atoi(m[1])
		mn := 0
		if m[2] != "" {
			mn, _ = strconv.Atoi(m[2])
		}
		if m[3] == "pm" && h != 12 {
			h += 12
		}
		if m[3] == "am" && h == 12 {
			h = 0
		}
		if h > 23 || mn > 59 {
			return 0, 0, false
		}
		return h, mn, true
	}

	// Try 24h HH:MM
	re24 := regexp.MustCompile(`^(\d{1,2}):(\d{2})$`)
	if m := re24.FindStringSubmatch(s); m != nil {
		h, _ := strconv.Atoi(m[1])
		mn, _ := strconv.Atoi(m[2])
		if h > 23 || mn > 59 {
			return 0, 0, false
		}
		return h, mn, true
	}

	return 0, 0, false
}

// midnight returns the start of the day containing t, in t's location.
func midnight(t time.Time) time.Time {
	y, mo, d := t.Date()
	return time.Date(y, mo, d, 0, 0, 0, 0, t.Location())
}

// nextWeekday returns the next occurrence of wd after now (same day counts as next week).
func nextWeekday(now time.Time, wd time.Weekday) time.Time {
	days := int(wd) - int(now.Weekday())
	if days <= 0 {
		days += 7
	}
	return midnight(now.Add(time.Duration(days) * 24 * time.Hour))
}

// unitDuration converts a unit name + count to a time.Duration.
func unitDuration(unit string, n int) time.Duration {
	switch unit {
	case "minute", "minutes", "min", "mins":
		return time.Duration(n) * time.Minute
	case "hour", "hours", "hr", "hrs":
		return time.Duration(n) * time.Hour
	case "day", "days":
		return time.Duration(n) * 24 * time.Hour
	case "week", "weeks":
		return time.Duration(n) * 7 * 24 * time.Hour
	}
	return 0
}

// parseNumber parses "a", "an", digit strings, or English word numbers.
func parseNumber(s string) (int, bool) {
	if s == "a" || s == "an" {
		return 1, true
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n, true
	}
	words := map[string]int{
		"one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
		"six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
		"eleven": 11, "twelve": 12, "thirteen": 13, "fourteen": 14, "fifteen": 15,
		"sixteen": 16, "seventeen": 17, "eighteen": 18, "nineteen": 19, "twenty": 20,
		"thirty": 30, "forty": 40, "fifty": 50, "sixty": 60,
		"half": 30, // "half hour" → treated as 30
	}
	if n, ok := words[s]; ok {
		return n, true
	}
	return 0, false
}
