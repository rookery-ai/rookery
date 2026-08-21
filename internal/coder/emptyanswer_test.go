package coder

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/llm"
)

// A model that stops calling tools AND says nothing has not answered.
//
// The final-answer branch returned Result{Text: resp.Content} with
// StopReason:"" — explicitly "was not cut short" — so an empty completion was
// recorded as a finished turn. That is how two dead chat turns on a 155 KB
// table were classified as successes, with nothing in the server log and only a
// placeholder shown to the owner.
func TestEmptyFinalAnswerIsNotTreatedAsSuccess(t *testing.T) {
	dir := t.TempDir()
	ws := "ws1"
	c := newTestCoder(t, dir)
	mustMkdir(t, filepath.Join(dir, "vaults", ws))

	testFake.calls = 0
	testFake.script = func(call int, _ llm.Request) (*llm.Response, error) {
		// Never any tool calls, never any text — the shape a small model falls
		// into once its context is full of paged file contents.
		return &llm.Response{Content: ""}, nil
	}

	res, err := c.Generate(context.Background(), ws, "go")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if strings.TrimSpace(res.Text) == "" {
		t.Errorf("an empty completion was returned as a finished answer")
	}
	if res.StopReason != "empty" {
		t.Errorf("StopReason = %q, want %q", res.StopReason, "empty")
	}
	if testFake.calls < 2 {
		t.Errorf("the model was never nudged to answer in words (calls=%d)", testFake.calls)
	}
}

// The nudge is bounded. A model in this state has proven it will not produce
// text, and every extra round costs a turn from the budget that is usually why
// it went quiet in the first place.
func TestEmptyAnswerIsNudgedOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	ws := "ws1"
	c := newTestCoder(t, dir)
	mustMkdir(t, filepath.Join(dir, "vaults", ws))

	testFake.calls = 0
	testFake.script = func(call int, _ llm.Request) (*llm.Response, error) {
		return &llm.Response{Content: ""}, nil
	}

	if _, err := c.Generate(context.Background(), ws, "go"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if testFake.calls != 2 {
		t.Errorf("provider called %d times, want exactly 2 (one answer + one nudge)", testFake.calls)
	}
}

// A model that answers properly after the nudge keeps its own words — the
// fallback is for the case where nothing works, not a replacement for a late
// answer.
func TestEmptyAnswerNudgeAcceptsARecoveredReply(t *testing.T) {
	dir := t.TempDir()
	ws := "ws1"
	c := newTestCoder(t, dir)
	mustMkdir(t, filepath.Join(dir, "vaults", ws))

	testFake.calls = 0
	testFake.script = func(call int, _ llm.Request) (*llm.Response, error) {
		if call == 0 {
			return &llm.Response{Content: ""}, nil
		}
		return &llm.Response{Content: "Sorry — the file was too large to read in full."}, nil
	}

	res, err := c.Generate(context.Background(), ws, "go")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(res.Text, "too large to read") {
		t.Errorf("the recovered reply was discarded: %q", res.Text)
	}
	if res.StopReason != "" {
		t.Errorf("StopReason = %q, want empty — the model did answer", res.StopReason)
	}
}
