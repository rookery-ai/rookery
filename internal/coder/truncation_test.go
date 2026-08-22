package coder

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/llm"
)

// A truncated answer and an empty one need OPPOSITE handling, and the engine
// treated them alike.
//
// A reasoning model spends its thinking from the same completion budget as its
// answer, so a hard synthesis can consume the whole cap before one content token
// is emitted: finish_reason "length", content "". The nudge built for a genuinely
// silent model is actively wrong here — it asks for the answer again under the
// SAME cap, so it truncates again, and the extra turn grows the context making
// truncation likelier still. Raise the cap and re-ask instead.
func TestTruncatedAnswerRetriesWithAHigherCapRatherThanNudging(t *testing.T) {
	dir := t.TempDir()
	ws := "ws1"
	c := newTestCoder(t, dir)
	mustMkdir(t, filepath.Join(dir, "vaults", ws))

	var capsSeen []int
	var sawNudge bool
	testFake.calls = 0
	testFake.script = func(call int, req llm.Request) (*llm.Response, error) {
		capsSeen = append(capsSeen, req.MaxTokens)
		for _, m := range req.Messages {
			if strings.Contains(m.Content, "You returned no text at all") {
				sawNudge = true
			}
		}
		if call == 0 {
			// Spent the whole budget thinking; the answer never started.
			return &llm.Response{Content: "", FinishReason: "length",
				Reasoning: "We need answer user. Need parse problem."}, nil
		}
		return &llm.Response{Content: "[CHAT] 12 merchants, EUR 4,210 total.",
			FinishReason: "stop"}, nil
	}

	res, err := c.Generate(context.Background(), ws, "go")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if sawNudge {
		t.Error("truncation took the nudge path; the nudge re-asks under the same cap and truncates again")
	}
	if len(capsSeen) < 2 {
		t.Fatalf("expected a retry, got %d call(s)", len(capsSeen))
	}
	if capsSeen[1] <= capsSeen[0] {
		t.Errorf("retry cap %d did not exceed the truncated call's %d — the retry has no more room than the call that failed",
			capsSeen[1], capsSeen[0])
	}
	if !strings.Contains(res.Text, "12 merchants") {
		t.Errorf("the retry's answer was lost: %q", res.Text)
	}
	// A run that recovered is a normal finish, not a cut-short one.
	if res.StopReason != "" {
		t.Errorf("StopReason = %q, want empty after a successful retry", res.StopReason)
	}
}

// When even the raised cap is not enough, the run must SAY it was truncated.
// The old single message asserted a cause — that the request needed more of a
// large file than fits — and that sentence, wrong for this failure, is what sent
// four separate investigations after file size. A message that guesses is worse
// than one that reports, because the guess is what gets believed.
func TestExhaustedTruncationReportsTruncationNotEmptiness(t *testing.T) {
	dir := t.TempDir()
	ws := "ws1"
	c := newTestCoder(t, dir)
	mustMkdir(t, filepath.Join(dir, "vaults", ws))

	testFake.calls = 0
	testFake.script = func(_ int, _ llm.Request) (*llm.Response, error) {
		return &llm.Response{Content: "", FinishReason: "length", Reasoning: "still thinking"}, nil
	}

	res, err := c.Generate(context.Background(), ws, "go")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if res.StopReason != "truncated" {
		t.Errorf("StopReason = %q, want truncated", res.StopReason)
	}
	if strings.Contains(res.Text, "large file") {
		t.Errorf("the message still blames file size for a truncated answer: %q", res.Text)
	}
	if !strings.Contains(res.Text, "output budget") {
		t.Errorf("the message does not name the real cause: %q", res.Text)
	}
}

// A model that simply returns nothing — no truncation — must keep the nudge it
// has always had. Reasoning-model truncation is a NEW branch beside that one,
// not a replacement for it.
func TestGenuinelyEmptyAnswerStillNudges(t *testing.T) {
	dir := t.TempDir()
	ws := "ws1"
	c := newTestCoder(t, dir)
	mustMkdir(t, filepath.Join(dir, "vaults", ws))

	var sawNudge bool
	testFake.calls = 0
	testFake.script = func(call int, req llm.Request) (*llm.Response, error) {
		for _, m := range req.Messages {
			if strings.Contains(m.Content, "You returned no text at all") {
				sawNudge = true
			}
		}
		if call == 0 {
			return &llm.Response{Content: "", FinishReason: "stop"}, nil
		}
		return &llm.Response{Content: "[CHAT] recovered.", FinishReason: "stop"}, nil
	}

	if _, err := c.Generate(context.Background(), ws, "go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !sawNudge {
		t.Error("a genuinely empty answer no longer gets the nudge")
	}
}
