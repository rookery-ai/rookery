package budget

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/rookery-ai/rookery/internal/db"
)

// ParseSpecLine reads the value of a `Limits:` line from a technical spec.
// "none" means the owner wants no limits (set=true, zero limits);
// "unchanged" or empty means leave them alone (set=false). Grammar:
//
//	per run: 50000 tokens, 20 turns, $0.50; per day: 500k tokens, $2; per month: $30
//
// ok=false on anything unrecognised, and the caller ignores the whole line: a
// half-read limit would silently be a different limit from the one approved.
func ParseSpecLine(v string) (l db.AgentLimits, set bool, ok bool) {
	l, _, set, ok = ParseSpecLineScopes(v)
	return l, set, ok
}

// ParseSpecLineScopes is ParseSpecLine that also reports which scopes the line
// named, in the order written. An edit needs it: a zero field in the parsed
// limits means both "this scope was not mentioned" and "this scope was mentioned
// without that unit", and only the second may overwrite what the agent has.
// A whole-line "none" names every scope; a scope written as "per day: none"
// names that scope with all its fields zero, which is how one period is removed
// without restating the others.
func ParseSpecLineScopes(v string) (l db.AgentLimits, named []Period, set bool, ok bool) {
	// A model often ends the line with a full stop; that is not part of a value.
	v = strings.ToLower(strings.TrimRight(strings.TrimSpace(v), ".,;! \t"))
	switch v {
	case "", "unchanged":
		return l, nil, false, true
	case "none":
		return l, []Period{PeriodRun, PeriodDay, PeriodMonth}, true, true
	}
	seen := map[string]bool{}
	for _, scope := range strings.Split(v, ";") {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		rawName, rest, found := strings.Cut(scope, ":")
		name := scopeName(rawName)
		if !found || name == "" || seen[name] {
			return db.AgentLimits{}, nil, false, false
		}
		seen[name] = true
		named = append(named, scopePeriod(name))
		if strings.TrimSpace(rest) == "none" {
			continue
		}
		units := map[string]bool{}
		// Split on comma+space only: "1,000,000" is a number, not a list.
		for _, item := range splitItems(rest) {
			if !applyItem(&l, name, item, units) {
				return db.AgentLimits{}, nil, false, false
			}
		}
	}
	if len(seen) == 0 {
		return db.AgentLimits{}, nil, false, false
	}
	return l, named, true, true
}

func scopePeriod(name string) Period {
	switch name {
	case "per run":
		return PeriodRun
	case "per day":
		return PeriodDay
	}
	return PeriodMonth
}

// MergeLimitScopes replaces, in cur, every field of each named scope with the
// value from next, and keeps every other scope as it was. It is the edit rule:
// a plan that restates only the daily limit must not erase the monthly one.
func MergeLimitScopes(cur, next db.AgentLimits, named []Period) db.AgentLimits {
	out := cur
	for _, p := range named {
		switch p {
		case PeriodRun:
			out.RunTokens, out.RunTurns, out.RunUSD = next.RunTokens, next.RunTurns, next.RunUSD
		case PeriodDay:
			out.DayTokens, out.DayUSD = next.DayTokens, next.DayUSD
		case PeriodMonth:
			out.MonthTokens, out.MonthUSD = next.MonthTokens, next.MonthUSD
		}
	}
	return out
}

// scopeName folds the spellings a model plausibly writes into the three
// canonical scopes, or "" when it is none of them.
func scopeName(raw string) string {
	r := strings.NewReplacer("-", " ", "_", " ")
	switch strings.Join(strings.Fields(r.Replace(raw)), " ") {
	case "per run", "run", "each run":
		return "per run"
	case "per day", "day", "daily", "each day":
		return "per day"
	case "per month", "month", "monthly", "each month":
		return "per month"
	}
	return ""
}

// Caps keep a typo from becoming a limit that can never fire.
const (
	maxCount = 1e12
	maxUSD   = 1e9
)

// MaxRunTurns is the highest per-run turn limit. The engine's hard ceiling is 150
// turns, so a limit of 150 or more could never be the thing that stops a run. The
// web API and this parser share it so the designer cannot save a limit the
// agent's page would then refuse.
const MaxRunTurns = 149

// plainNumber admits digits, thousands commas and one decimal point, and
// nothing else: no exponent, nan, inf or sign.
var plainNumber = regexp.MustCompile(`^(?:\d[\d,]*(?:\.\d+)?|\.\d+)$`)

func splitItems(s string) []string {
	var out []string
	cur := ""
	for i := 0; i < len(s); i++ {
		if s[i] == ',' && i+1 < len(s) && (s[i+1] == ' ' || s[i+1] == '\t') {
			out = append(out, strings.TrimSpace(cur))
			cur = ""
			continue
		}
		cur += string(s[i])
	}
	return append(out, strings.TrimSpace(cur))
}

func applyItem(l *db.AgentLimits, scope, item string, units map[string]bool) bool {
	item = strings.TrimSpace(item)
	if strings.HasPrefix(item, "$") {
		num := strings.TrimSpace(item[1:])
		if !plainNumber.MatchString(num) || units["usd"] {
			return false
		}
		f, err := strconv.ParseFloat(strings.ReplaceAll(num, ",", ""), 64)
		if err != nil || f <= 0 || f > maxUSD {
			return false
		}
		units["usd"] = true
		switch scope {
		case "per run":
			l.RunUSD = f
		case "per day":
			l.DayUSD = f
		default:
			l.MonthUSD = f
		}
		return true
	}
	fields := strings.Fields(item)
	if len(fields) != 2 {
		return false
	}
	unit := ""
	switch fields[1] {
	case "token", "tokens":
		unit = "tokens"
	case "turn", "turns":
		unit = "turns"
	default:
		return false
	}
	if units[unit] || (unit == "turns" && scope != "per run") {
		return false
	}
	num := fields[0]
	mult := 1.0
	switch {
	case strings.HasSuffix(num, "k"):
		mult, num = 1e3, strings.TrimSuffix(num, "k")
	case strings.HasSuffix(num, "m"):
		mult, num = 1e6, strings.TrimSuffix(num, "m")
	}
	if !plainNumber.MatchString(num) {
		return false
	}
	f, err := strconv.ParseFloat(strings.ReplaceAll(num, ",", ""), 64)
	if err != nil || f*mult < 0.5 || f*mult > maxCount {
		return false
	}
	units[unit] = true
	n := int64(f*mult + 0.5)
	if unit == "turns" {
		if n > MaxRunTurns {
			return false
		}
		l.RunTurns = n
		return true
	}
	switch scope {
	case "per run":
		l.RunTokens = n
	case "per day":
		l.DayTokens = n
	default:
		l.MonthTokens = n
	}
	return true
}

// FormatSpecLine is the inverse of ParseSpecLine, written without thousands
// separators so the value survives a naive comma split.
func FormatSpecLine(l db.AgentLimits) string {
	var scopes []string
	add := func(name string, parts ...string) {
		var kept []string
		for _, p := range parts {
			if p != "" {
				kept = append(kept, p)
			}
		}
		if len(kept) > 0 {
			scopes = append(scopes, name+": "+strings.Join(kept, ", "))
		}
	}
	tok := func(n int64) string {
		if n == 0 {
			return ""
		}
		return fmt.Sprintf("%d tokens", n)
	}
	usd := func(v float64) string {
		if v == 0 {
			return ""
		}
		// Trim to two decimals minimum so a sub-cent limit survives a round trip.
		t := strconv.FormatFloat(v, 'f', 4, 64)
		for strings.HasSuffix(t, "0") && !strings.HasSuffix(t[:len(t)-1], ".") && len(t)-strings.Index(t, ".") > 3 {
			t = t[:len(t)-1]
		}
		return "$" + t
	}
	turns := ""
	if l.RunTurns > 0 {
		turns = fmt.Sprintf("%d turns", l.RunTurns)
	}
	add("per run", tok(l.RunTokens), turns, usd(l.RunUSD))
	add("per day", tok(l.DayTokens), usd(l.DayUSD))
	add("per month", tok(l.MonthTokens), usd(l.MonthUSD))
	if len(scopes) == 0 {
		return "none"
	}
	return strings.Join(scopes, "; ")
}
