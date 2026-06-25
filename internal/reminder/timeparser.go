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
//	[next|this] <weekday> [at TIME]
//	at TIME                          (today, or tomorrow if time has already passed)
//	morning / afternoon / evening / night / tonight  (time-of-day shortcuts)
//	[this] morning / afternoon / evening / tonight
//	next week                        (Monday 9am of next week)
//	end of the/this week             (Friday 5pm)
//	end of the/this month            (last day of month at 9am)
//	<Month> <day> [at TIME]          (e.g. "July 15", "July 15 at 3pm")
//	<day>th [of <Month>] [at TIME]   (e.g. "the 15th", "15th of July")
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

	// "[next|this] <weekday> [at TIME]" and "next week"
	if t, ok := tryNextWeekday(s, now, loc); ok {
		return t, nil
	}

	// "at TIME" — today if future, tomorrow if past
	if t, ok := tryAtTime(s, now, loc); ok {
		return t, nil
	}

	// "[this] morning / afternoon / evening / night / tonight"
	if t, ok := tryTimeOfDayShortcut(s, now, loc); ok {
		return t, nil
	}

	// "end of the/this week" or "end of the/this month"
	if t, ok := tryEndOf(s, now, loc); ok {
		return t, nil
	}

	// "<Month> <day> [at TIME]" — e.g. "July 15", "December 31 at midnight"
	if t, ok := trySpecificDate(s, now, loc); ok {
		return t, nil
	}

	// "the 15th", "15th", "15th of July" [at TIME]
	if t, ok := tryOrdinalDay(s, now, loc); ok {
		return t, nil
	}

	return time.Time{}, fmt.Errorf(`unrecognized time expression %q; try: "in 10 minutes", "tomorrow at 3pm", "next Tuesday", "July 15 at 2pm"`, text)
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

// months maps English month names (full and abbreviated) to time.Month values.
var months = map[string]time.Month{
	"january": time.January, "february": time.February, "march": time.March,
	"april": time.April, "may": time.May, "june": time.June,
	"july": time.July, "august": time.August, "september": time.September,
	"october": time.October, "november": time.November, "december": time.December,
	"jan": time.January, "feb": time.February, "mar": time.March,
	"apr": time.April, "jun": time.June, "jul": time.July,
	"aug": time.August, "sep": time.September, "oct": time.October,
	"nov": time.November, "dec": time.December,
}

// "[next|this] <weekday> [at TIME]" — also handles "next week"
var reNextWeekday = regexp.MustCompile(`^(?:(?:next|this)\s+)?([a-z]+)(\s+at\s+(.+))?$`)

func tryNextWeekday(s string, now time.Time, loc *time.Location) (time.Time, bool) {
	m := reNextWeekday.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}

	// "next week" → next Monday at 9am
	if m[1] == "week" {
		base := nextWeekday(now.In(loc), time.Monday)
		return base.Add(9 * time.Hour), true
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

// "[this] morning/afternoon/evening/night/tonight" — maps to default hours.
var reTimeOfDayShortcut = regexp.MustCompile(`^(?:this\s+)?(morning|afternoon|evening|night|tonight)$`)

var timeOfDayHours = map[string]int{
	"morning":   9,
	"afternoon": 14,
	"evening":   18,
	"night":     21,
	"tonight":   21,
}

func tryTimeOfDayShortcut(s string, now time.Time, loc *time.Location) (time.Time, bool) {
	m := reTimeOfDayShortcut.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}
	h := timeOfDayHours[m[1]]
	t := midnight(now.In(loc)).Add(time.Duration(h) * time.Hour)
	if !t.After(now) {
		t = t.Add(24 * time.Hour)
	}
	return t, true
}

// "end of the/this week" → Friday 5pm; "end of the/this month" → last day at 9am.
var reEndOf = regexp.MustCompile(`^end\s+of\s+(?:the\s+|this\s+)?(week|month)$`)

func tryEndOf(s string, now time.Time, loc *time.Location) (time.Time, bool) {
	m := reEndOf.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}
	local := now.In(loc)
	switch m[1] {
	case "week":
		// Next Friday 5pm (or this Friday if it's in the future).
		base := nextWeekday(local, time.Friday)
		return midnight(base).Add(17 * time.Hour), true
	case "month":
		// Last day of the current month at 9am.
		y, mo, _ := local.Date()
		lastDay := time.Date(y, mo+1, 0, 9, 0, 0, 0, loc)
		if lastDay.Before(now) {
			// Already past end of month — use next month.
			lastDay = time.Date(y, mo+2, 0, 9, 0, 0, 0, loc)
		}
		return lastDay, true
	}
	return time.Time{}, false
}

// "<Month> <day> [, year] [at TIME]" — e.g. "July 15", "July 15 at 3pm", "Dec 31, 2027".
var reMonthDay = regexp.MustCompile(
	`^([a-z]+)\s+(\d{1,2})(?:st|nd|rd|th)?(?:,?\s+(\d{4}))?(?:\s+at\s+(.+))?$`)

func trySpecificDate(s string, now time.Time, loc *time.Location) (time.Time, bool) {
	m := reMonthDay.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}
	mo, ok := months[m[1]]
	if !ok {
		return time.Time{}, false
	}
	day, err := strconv.Atoi(m[2])
	if err != nil || day < 1 || day > 31 {
		return time.Time{}, false
	}

	local := now.In(loc)
	year := local.Year()
	if m[3] != "" {
		if y, err := strconv.Atoi(m[3]); err == nil {
			year = y
		}
	}

	h, min := 9, 0 // default 9am
	if m[4] != "" {
		hh, mm, ok := parseTimeOfDay(strings.TrimSpace(m[4]))
		if !ok {
			return time.Time{}, false
		}
		h, min = hh, mm
	}

	t := time.Date(year, mo, day, h, min, 0, 0, loc)
	// If the date is in the past and no year was specified, advance to next year.
	if t.Before(now) && m[3] == "" {
		t = time.Date(year+1, mo, day, h, min, 0, 0, loc)
	}
	if t.Before(now) {
		return time.Time{}, false // year was explicit but still past
	}
	return t, true
}

// "[the] <ordinal> [of <month>] [at TIME]" — e.g. "the 15th", "15th of July at 3pm".
var reOrdinalDay = regexp.MustCompile(
	`^(?:the\s+)?(\d{1,2})(?:st|nd|rd|th)(?:\s+(?:of\s+)?([a-z]+))?(?:\s+at\s+(.+))?$`)

func tryOrdinalDay(s string, now time.Time, loc *time.Location) (time.Time, bool) {
	m := reOrdinalDay.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}
	day, err := strconv.Atoi(m[1])
	if err != nil || day < 1 || day > 31 {
		return time.Time{}, false
	}

	local := now.In(loc)
	year, curMonth, _ := local.Date()

	var mo time.Month
	if m[2] != "" {
		var ok bool
		mo, ok = months[m[2]]
		if !ok {
			return time.Time{}, false
		}
	} else {
		mo = curMonth
	}

	h, min := 9, 0
	if m[3] != "" {
		hh, mm, ok := parseTimeOfDay(strings.TrimSpace(m[3]))
		if !ok {
			return time.Time{}, false
		}
		h, min = hh, mm
	}

	t := time.Date(year, mo, day, h, min, 0, 0, loc)
	if t.Before(now) {
		// If no month specified: try next month.
		if m[2] == "" {
			t = time.Date(year, mo+1, day, h, min, 0, 0, loc)
		} else {
			// Month was specified but date is past → advance to next year.
			t = time.Date(year+1, mo, day, h, min, 0, 0, loc)
		}
	}
	if t.Before(now) {
		return time.Time{}, false
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
