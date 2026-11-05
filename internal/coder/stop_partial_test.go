package coder

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rookery-ai/rookery/internal/llm"
	"github.com/rookery-ai/rookery/internal/vault"
)

// A stopped run keeps what it spent. Whoever stops a looping agent wants to know what
// the loop cost, so a cancel must not discard the usage and tool calls already made.
func TestCancelledRunKeepsUsageAndToolTrace(t *testing.T) {
	dir := t.TempDir()
	ws := "ws-partial"
	if err := os.MkdirAll(filepath.Join(dir, "vaults", ws, "agents", "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := New("claude", time.Minute, "", dir).
		WithSandbox(false).
		WithVault(vault.New(dir)).
		WithDir(filepath.Join(dir, "vaults", ws, "agents", "a")).
		WithAPIConfig("fake", "fake-model", "fake://test", "PROVIDER_KEY").
		WithSecretsLookup(func(_ context.Context, _, _ string) (string, error) { return "k", nil })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	testFake.calls = 0
	testFake.script = func(call int, _ llm.Request) (*llm.Response, error) {
		if call == 0 {
			return &llm.Response{
				Usage:     llm.Usage{TotalTokens: 100},
				ToolCalls: []llm.ToolCall{{ID: "1", Name: "list_dir", Args: json.RawMessage(`{"path":"."}`)}},
			}, nil
		}
		cancel()
		return nil, fmt.Errorf("provider: %w", context.Canceled)
	}

	res, err := c.Generate(ctx, ws, "loop forever")
	if err == nil {
		t.Fatal("a cancelled run must report an error")
	}
	if res == nil {
		t.Fatal("a cancelled run should hand back what it had spent")
	}
	if res.Usage.TotalTokens != 100 {
		t.Errorf("usage = %d, want 100", res.Usage.TotalTokens)
	}
	if len(res.ToolTrace) != 1 || res.ToolTrace[0].Name != "list_dir" {
		t.Errorf("tool trace = %+v", res.ToolTrace)
	}
}

// Everything except a caller's cancel still returns no result, so no other caller has
// to reason about a partial one.
func TestProviderFailureStillReturnsNoResult(t *testing.T) {
	dir := t.TempDir()
	ws := "ws-fail"
	if err := os.MkdirAll(filepath.Join(dir, "vaults", ws), 0o755); err != nil {
		t.Fatal(err)
	}
	c := New("claude", time.Minute, "", dir).
		WithSandbox(false).
		WithVault(vault.New(dir)).
		WithAPIConfig("fake", "fake-model", "fake://test", "PROVIDER_KEY").
		WithSecretsLookup(func(_ context.Context, _, _ string) (string, error) { return "k", nil })
	testFake.calls = 0
	testFake.script = func(int, llm.Request) (*llm.Response, error) {
		return nil, fmt.Errorf("provider down")
	}
	res, err := c.Generate(context.Background(), ws, "anything")
	if err == nil || res != nil {
		t.Fatalf("got res=%v err=%v, want no result and an error", res, err)
	}
}
