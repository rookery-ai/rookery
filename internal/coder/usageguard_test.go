package coder

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rookery-ai/rookery/internal/llm"
)

func TestUsageGuardSeesEveryCompletion(t *testing.T) {
	dir := t.TempDir()
	ws := "ws1"
	mustMkdir(t, filepath.Join(dir, "vaults", ws))
	var seen []llm.Usage
	c := newTestCoder(t, dir).WithUsageGuard(func(d llm.Usage) { seen = append(seen, d) })

	testFake.calls = 0
	testFake.script = func(call int, _ llm.Request) (*llm.Response, error) {
		u := llm.Usage{TotalTokens: 10}
		if call < 3 {
			return &llm.Response{Usage: u, ToolCalls: []llm.ToolCall{
				toolCall("write_file", `{"path":"n.txt","content":"x"}`),
			}}, nil
		}
		return &llm.Response{Usage: u, Content: "[CHAT] done"}, nil
	}
	if _, err := c.Generate(context.Background(), ws, "p"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(seen) != 4 {
		t.Fatalf("guard called %d times, want 4 (3 tool turns + final)", len(seen))
	}
}

func TestUsageGuardCancelStopsBeforeToolExecution(t *testing.T) {
	dir := t.TempDir()
	ws := "ws1"
	mustMkdir(t, filepath.Join(dir, "vaults", ws))
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	c := newTestCoder(t, dir).WithUsageGuard(func(llm.Usage) { cancel(context.Canceled) })

	testFake.calls = 0
	testFake.script = func(int, llm.Request) (*llm.Response, error) {
		return &llm.Response{Usage: llm.Usage{TotalTokens: 42}, ToolCalls: []llm.ToolCall{
			toolCall("write_file", `{"path":"should-not-exist.txt","content":"x"}`),
		}}, nil
	}
	res, _ := c.Generate(ctx, ws, "p")
	if res == nil || res.Usage.TotalTokens != 42 {
		t.Fatalf("result must carry the spent usage, got %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "vaults", ws, "should-not-exist.txt")); err == nil {
		t.Fatal("tool call executed after the guard cancelled the run")
	}
	if testFake.calls != 1 {
		t.Fatalf("provider called %d times, want 1", testFake.calls)
	}
}

// A completion that crosses the ceiling while giving the final answer has already
// been paid for and finishes the run; discarding it would leave the owner with the
// cost and nothing to show for it. Only a completion asking for MORE tool turns is
// stopped.
func TestUsageGuardCancelOnFinalAnswerStillReturnsIt(t *testing.T) {
	dir := t.TempDir()
	ws := "ws1"
	mustMkdir(t, filepath.Join(dir, "vaults", ws))
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	c := newTestCoder(t, dir).WithUsageGuard(func(llm.Usage) { cancel(context.Canceled) })

	testFake.calls = 0
	testFake.script = func(int, llm.Request) (*llm.Response, error) {
		return &llm.Response{Usage: llm.Usage{TotalTokens: 42}, Content: "[CHAT] the answer"}, nil
	}
	res, err := c.Generate(ctx, ws, "p")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if res == nil || res.Text != "[CHAT] the answer" || res.Usage.TotalTokens != 42 {
		t.Fatalf("result = %+v", res)
	}
}

func TestProviderReportsCost(t *testing.T) {
	for name, want := range map[string]bool{"openrouter": true, "xai": true, "openai": false, "anthropic": false, "nope": false} {
		if got := ProviderReportsCost(name); got != want {
			t.Errorf("ProviderReportsCost(%q) = %v, want %v", name, got, want)
		}
	}
}
