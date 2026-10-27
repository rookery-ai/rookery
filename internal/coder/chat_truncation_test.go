package coder

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/llm"
)

// The text-only path (WithNoTools().Chat) is what the skill vetter and the design
// conversation run on, and it never had the tool loop's truncation retry. A reasoning
// model reviewing a large skill spent the default cap thinking and returned empty
// content with finish_reason "length" — four times in a row — so every
// trading-consultant rebuild was refused for a security review that "returned no
// verdict". Same remedy as the tool loop: re-ask once with more room.
func TestTextOnlyChatRetriesATruncatedAnswerWithAHigherCap(t *testing.T) {
	dir := t.TempDir()
	ws := "ws1"
	c := newTestCoder(t, dir)
	mustMkdir(t, filepath.Join(dir, "vaults", ws))

	var capsSeen []int
	testFake.calls = 0
	testFake.script = func(call int, req llm.Request) (*llm.Response, error) {
		capsSeen = append(capsSeen, req.MaxTokens)
		if call == 0 {
			return &llm.Response{Content: "", FinishReason: "length", Reasoning: "Audit the script line by line…"}, nil
		}
		return &llm.Response{Content: "SKILL VETTING REPORT\nVerdict: ✅ safe to save", FinishReason: "stop"}, nil
	}

	res, err := c.WithNoTools().Chat(context.Background(), ws, nil, "vetting protocol", "skill under review")
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if len(capsSeen) != 2 {
		t.Fatalf("calls = %d, want a single retry", len(capsSeen))
	}
	if capsSeen[1] <= capsSeen[0] {
		t.Errorf("retry cap %d did not exceed the first call's %d", capsSeen[1], capsSeen[0])
	}
	if !strings.Contains(res.Text, "Verdict:") {
		t.Errorf("the retry's answer was lost: %q", res.Text)
	}
}

// A genuinely finished empty reply is not truncation: re-asking under more room would
// spend tokens for nothing, so it returns once, as before.
func TestTextOnlyChatDoesNotRetryAnEmptyFinishedAnswer(t *testing.T) {
	dir := t.TempDir()
	ws := "ws1"
	c := newTestCoder(t, dir)
	mustMkdir(t, filepath.Join(dir, "vaults", ws))

	testFake.calls = 0
	calls := 0
	testFake.script = func(call int, req llm.Request) (*llm.Response, error) {
		calls++
		return &llm.Response{Content: "", FinishReason: "stop"}, nil
	}
	if _, err := c.WithNoTools().Chat(context.Background(), ws, nil, "sys", "user"); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}
