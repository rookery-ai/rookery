package reminder

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// TimeParserFunc is an LLM-backed time + message extractor.
// It receives the user's raw input (may contain both time expression and message)
// and returns:
//   - when: the parsed absolute time (zero value if no time was found in the input)
//   - message: the reminder text with the time expression stripped (same as input if no time found)
//   - err: non-nil only when the LLM call itself failed; zero time with nil err means "no time in input"
type TimeParserFunc func(ctx context.Context, userID, input string, now time.Time, loc *time.Location) (when time.Time, message string, err error)

// ParseNaturalTimeFull tries the fast regex parser first, then falls back to llm.
// It returns (parsedTime, cleanedMessage, error) where:
//   - parsedTime is zero when no time was found (llm returned null "when")
//   - cleanedMessage is the input unchanged when regex succeeds, or the LLM-extracted message
//   - error is non-nil only on a hard failure (LLM call error or both parsers failed with no llm)
func ParseNaturalTimeFull(ctx context.Context, text string, now time.Time, loc *time.Location, llm TimeParserFunc, userID string) (time.Time, string, error) {
	// Fast path: pure regex — zero cost, no network call.
	if t, err := ParseNaturalTime(text, now, loc); err == nil {
		return t, text, nil
	}

	// Slow path: LLM fallback.
	if llm == nil {
		return time.Time{}, text, fmt.Errorf("could not parse time expression %q", text)
	}

	when, msg, err := llm(ctx, userID, text, now, loc)
	if err != nil {
		return time.Time{}, text, err
	}
	if msg == "" {
		msg = text
	}
	// Zero time means the LLM found no time expression — caller decides what to do.
	return when, msg, nil
}

// llmReminderResponse is the expected JSON returned by BuildReminderParsePrompt.
type llmReminderResponse struct {
	When    *string `json:"when"`    // ISO 8601 UTC timestamp, or null/omitted
	Message string  `json:"message"` // cleaned reminder text (time expression removed)
}

// ParseLLMReminderJSON parses the JSON output from a BuildReminderParsePrompt call.
// It extracts the JSON object even when the LLM wraps it in markdown code fences or prose.
// Returns zero time (with nil error) when "when" is null — caller should ask user for a time.
func ParseLLMReminderJSON(raw string, now time.Time) (when time.Time, message string, err error) {
	// Strip markdown code fences and surrounding prose to get at the JSON.
	raw = strings.TrimSpace(raw)
	if idx := strings.Index(raw, "{"); idx >= 0 {
		raw = raw[idx:]
	}
	if idx := strings.LastIndex(raw, "}"); idx >= 0 {
		raw = raw[:idx+1]
	}

	var resp llmReminderResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return time.Time{}, "", fmt.Errorf("llm returned non-JSON: %w", err)
	}

	message = strings.TrimSpace(resp.Message)

	if resp.When == nil || *resp.When == "" || strings.EqualFold(*resp.When, "null") {
		return time.Time{}, message, nil // no time found — not an error
	}

	t, err := time.Parse(time.RFC3339, *resp.When)
	if err != nil {
		// Try without seconds
		t, err = time.Parse("2006-01-02T15:04Z", *resp.When)
		if err != nil {
			return time.Time{}, message, fmt.Errorf("llm returned invalid timestamp %q: %w", *resp.When, err)
		}
	}

	// Sanity: must be in the future and within 5 years.
	if t.Before(now.Add(-time.Hour)) {
		return time.Time{}, message, fmt.Errorf("llm returned a past time: %s", t.Format(time.RFC3339))
	}
	if t.After(now.Add(5 * 365 * 24 * time.Hour)) {
		return time.Time{}, message, fmt.Errorf("llm returned a time too far in the future: %s", t.Format(time.RFC3339))
	}

	return t, message, nil
}
