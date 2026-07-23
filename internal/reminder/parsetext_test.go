package reminder

import (
	"context"
	"testing"
	"time"
)

func TestParseReminderText_Deterministic(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, loc)
	cases := []struct {
		name     string
		in       string
		wantMsg  string
		wantZero bool
		checkAt  func(time.Time) bool
	}{
		{"remind-me-to-split", "remind me in 10 minutes to call the doctor", "call the doctor", false,
			func(at time.Time) bool { return at.Equal(now.Add(10 * time.Minute)) }},
		{"filler-reminder-no-splittable-time", "reminder to buy milk in 2 hours", "", true, nil},
		{"bare-to-split", "in 1 hour to submit invoice", "submit invoice", false,
			func(at time.Time) bool { return at.Equal(now.Add(time.Hour)) }},
		{"legacy-duration", "30m stretch break", "stretch break", false,
			func(at time.Time) bool { return at.Equal(now.Add(30 * time.Minute)) }},
		{"no-time-no-llm", "call the doctor", "call the doctor", true, nil},
		{"meeting-not-stripped", "in 5 minutes to prep the meeting", "prep the meeting", false,
			func(at time.Time) bool { return at.Equal(now.Add(5 * time.Minute)) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			at, msg, err := ParseReminderText(context.Background(), tc.in, now, loc, nil, "w1")
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if tc.wantZero && !at.IsZero() {
				t.Fatalf("expected zero time, got %v", at)
			}
			if !tc.wantZero {
				if at.IsZero() {
					t.Fatalf("expected a time, got zero")
				}
				if tc.checkAt != nil && !tc.checkAt(at) {
					t.Fatalf("time mismatch: got %v", at)
				}
			}
			if tc.wantMsg != "" && msg != tc.wantMsg {
				t.Fatalf("message: got %q want %q", msg, tc.wantMsg)
			}
		})
	}
}

func TestStripReminderFiller(t *testing.T) {
	cases := map[string]string{
		"remind me in 10 minutes": "in 10 minutes",
		"reminder to buy milk":    "buy milk",
		"remind buy milk":         "buy milk",
		"me buy milk":             "buy milk",
		"meeting at 3pm":          "meeting at 3pm", // NOT stripped — substring guard
		"reminder about taxes":    "about taxes",    // "reminder " prefix only
		"buy milk":                "buy milk",
	}
	for in, want := range cases {
		if got := stripReminderFiller(in); got != want {
			t.Errorf("stripReminderFiller(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestParseReminderText_LLMFallback exercises the branch where the " to " split
// finds no time and the LLM extracts both time and cleaned message.
func TestParseReminderText_LLMFallback(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, loc)
	fired := false
	llm := func(_ context.Context, _ string, input string, _ time.Time, _ *time.Location) (time.Time, string, error) {
		fired = true
		// The stripped arg must reach the LLM without the "remind me " filler.
		if input != "buy groceries tomorrow morning" {
			t.Fatalf("llm got unstripped input %q", input)
		}
		return now.Add(21 * time.Hour), "buy groceries", nil
	}
	at, msg, err := ParseReminderText(context.Background(), "remind me buy groceries tomorrow morning", now, loc, llm, "w1")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !fired {
		t.Fatal("llm fallback not invoked")
	}
	if at.IsZero() || msg != "buy groceries" {
		t.Fatalf("got at=%v msg=%q", at, msg)
	}
}
