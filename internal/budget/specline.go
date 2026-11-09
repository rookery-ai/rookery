package budget

import (
	"fmt"
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
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "", "unchanged":
		return l, false, true
	case "none":
		return l, true, true
	}
	seen := map[string]bool{}
	for _, scope := range strings.Split(v, ";") {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		name, rest, found := strings.Cut(scope, ":")
		name = strings.Join(strings.Fields(name), " ")
		if !found || seen[name] {
			return db.AgentLimits{}, false, false
		}
		seen[name] = true
		// Split on comma+space only: "1,000,000" is a number, not a list.
		for _, item := range splitItems(rest) {
			if !applyItem(&l, name, item) {
				return db.AgentLimits{}, false, false
			}
		}
	}
	if len(seen) == 0 {
		return db.AgentLimits{}, false, false
	}
	return l, true, true
}

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

func applyItem(l *db.AgentLimits, scope, item string) bool {
	item = strings.TrimSpace(item)
	if strings.HasPrefix(item, "$") {
		f, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(item[1:]), ",", ""), 64)
		if err != nil || f <= 0 {
			return false
		}
		switch scope {
		case "per run":
			l.RunUSD = f
		case "per day":
			l.DayUSD = f
		case "per month":
			l.MonthUSD = f
		default:
			return false
		}
		return true
	}
	fields := strings.Fields(item)
	if len(fields) != 2 || !strings.HasPrefix(fields[1], "token") && !strings.HasPrefix(fields[1], "turn") {
		return false
	}
	num := strings.ReplaceAll(fields[0], ",", "")
	mult := 1.0
	switch {
	case strings.HasSuffix(num, "k"):
		mult, num = 1e3, strings.TrimSuffix(num, "k")
	case strings.HasSuffix(num, "m"):
		mult, num = 1e6, strings.TrimSuffix(num, "m")
	}
	f, err := strconv.ParseFloat(num, 64)
	if err != nil || f <= 0 {
		return false
	}
	n := int64(f*mult + 0.5)
	if strings.HasPrefix(fields[1], "turn") {
		if scope != "per run" {
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
	case "per month":
		l.MonthTokens = n
	default:
		return false
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
		return fmt.Sprintf("$%.2f", v)
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
