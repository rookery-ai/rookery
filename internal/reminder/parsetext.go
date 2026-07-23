package reminder

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// fillerPrefixes are stripped from the FRONT of a web reminder phrase, longest
// first, matched only as a prefix followed by a space (or the bare verb) —
// never a substring, so "meeting" and "reminder about X" survive intact.
var fillerPrefixes = []string{
	"remind me to ",
	"remind me ",
	"reminder to ",
	"reminder ",
	"remind me",
	"remind ",
	"me ",
}

// stripReminderFiller removes a leading "remind me"/"reminder"/"me" verb phrase
// that the web field receives but the Telegram /remind command already consumed.
func stripReminderFiller(s string) string {
	s = strings.TrimSpace(s)
	lower := strings.ToLower(s)
	for _, p := range fillerPrefixes {
		if strings.HasPrefix(lower, p) {
			return strings.TrimSpace(s[len(p):])
		}
	}
	return s
}

var reDurationExpr = regexp.MustCompile(`^(\d+)\s*(m|min|mins|minute|minutes|h|hr|hrs|hour|hours|d|day|days|w|week|weeks)$`)

// parseDurationExpr parses a compact duration token like "30m", "2h", "1d".
// The gateway package has its own parseDuration; this reimplements the minimal
// form locally so internal/reminder stays free of a gateway import.
func parseDurationExpr(s string) (time.Duration, bool) {
	m := reDurationExpr.FindStringSubmatch(strings.ToLower(strings.TrimSpace(s)))
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	switch m[2][:1] {
	case "m":
		return time.Duration(n) * time.Minute, true
	case "h":
		return time.Duration(n) * time.Hour, true
	case "d":
		return time.Duration(n) * 24 * time.Hour, true
	case "w":
		return time.Duration(n) * 7 * 24 * time.Hour, true
	}
	return 0, false
}

// ParseReminderText extracts (when, message) from one natural-language string
// like "remind me in 10 minutes to call the doctor" or "buy milk tomorrow 9am".
//
// It is pure: no state, no prompting. The resolution strategy is (in order):
//  1. strip a leading "remind me"/"reminder"/"me" filler prefix;
//  2. a " to " split → regex/duration parse of the left, message = the right;
//  3. the LLM parser on the whole (stripped) string;
//  4. a legacy first-word-duration fallback ("30m stretch break").
//
// Returns:
//   - when:    the resolved time; ZERO when no time was found.
//   - message: the cleaned reminder text (time expression removed).
//   - err:     non-nil ONLY when the LLM call itself fails.
//
// A zero `when` with a non-empty `message` and nil err means "understood the
// message but found no time" — the caller decides whether to prompt for a time
// (Telegram) or reject with a 400 (web).
func ParseReminderText(ctx context.Context, text string, now time.Time, loc *time.Location, llm TimeParserFunc, workspaceID string) (time.Time, string, error) {
	if loc == nil {
		loc = time.UTC
	}
	arg := stripReminderFiller(text)

	var message string
	var remindAt time.Time

	// 1. " to " split → regex / duration on the left part.
	if idx := strings.Index(arg, " to "); idx >= 0 {
		timeExpr := strings.TrimSpace(arg[:idx])
		message = strings.TrimSpace(arg[idx+4:])
		if t, err := ParseNaturalTime(timeExpr, now, loc); err == nil {
			remindAt = t
		} else if d, ok := parseDurationExpr(timeExpr); ok {
			remindAt = now.Add(d)
		}
	}

	// 2. LLM on the whole (stripped) string.
	if remindAt.IsZero() && llm != nil {
		when, extractedMsg, err := llm(ctx, workspaceID, arg, now, loc)
		if err != nil {
			return time.Time{}, "", err
		}
		if !when.IsZero() {
			remindAt = when
		}
		if extractedMsg != "" {
			message = extractedMsg
		}
	}

	// 3. Legacy first-word-duration fallback: "30m stretch break".
	if remindAt.IsZero() {
		if parts := strings.SplitN(arg, " ", 2); len(parts) == 2 {
			if d, ok := parseDurationExpr(parts[0]); ok {
				remindAt = now.Add(d)
				if message == "" {
					message = strings.TrimSpace(parts[1])
				}
			}
		}
	}

	if message == "" {
		message = arg
	}
	return remindAt, message, nil
}
