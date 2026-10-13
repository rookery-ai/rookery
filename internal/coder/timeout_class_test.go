package coder

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rookery-ai/rookery/internal/llm"
	"github.com/rookery-ai/rookery/internal/vault"
)

// The deadline usually expires DURING a provider call — that is where the time
// goes — so the loop's top-of-turn check never sees it. The raw
// context.DeadlineExceeded then escaped unwrapped: the designer classified it
// "canceled", told the user "the model provider dropped the connection", and the
// documented timeout retry never ran. A 30:00 build on the demo workspace ended
// exactly that way.
func TestAPIEngine_DeadlineDuringProviderCallIsATimeout(t *testing.T) {
	dir := t.TempDir()
	ws := "ws-timeout"
	if err := os.MkdirAll(filepath.Join(dir, "vaults", ws), 0o755); err != nil {
		t.Fatal(err)
	}
	c := New("claude", 150*time.Millisecond, "", dir).
		WithSandbox(false).
		WithVault(vault.New(dir)).
		WithAPIConfig("fake", "fake-model", "fake://test", "PROVIDER_KEY").
		WithSecretsLookup(func(_ context.Context, _, _ string) (string, error) { return "k", nil })

	testFake.calls = 0
	testFake.script = func(int, llm.Request) (*llm.Response, error) {
		time.Sleep(300 * time.Millisecond) // outlive the coder's deadline mid-request
		return nil, fmt.Errorf("Post \"https://provider\": %w", context.DeadlineExceeded)
	}
	_, err := c.Generate(context.Background(), ws, "build an agent")
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("deadline mid-request surfaced as %v, want ErrTimeout", err)
	}
}

// The other half: a caller's Cancel is NOT a timeout. Reporting it as one would
// make a deliberately cancelled build retry itself.
func TestAPIEngine_CallerCancelIsNotATimeout(t *testing.T) {
	dir := t.TempDir()
	ws := "ws-cancel"
	if err := os.MkdirAll(filepath.Join(dir, "vaults", ws), 0o755); err != nil {
		t.Fatal(err)
	}
	c := New("claude", time.Minute, "", dir).
		WithSandbox(false).
		WithVault(vault.New(dir)).
		WithAPIConfig("fake", "fake-model", "fake://test", "PROVIDER_KEY").
		WithSecretsLookup(func(_ context.Context, _, _ string) (string, error) { return "k", nil })

	ctx, cancel := context.WithCancel(context.Background())
	testFake.calls = 0
	testFake.script = func(int, llm.Request) (*llm.Response, error) {
		cancel()
		return nil, fmt.Errorf("Post \"https://provider\": %w", context.Canceled)
	}
	_, err := c.Generate(ctx, ws, "build an agent")
	if errors.Is(err, ErrTimeout) {
		t.Fatalf("a caller cancel was reported as a timeout: %v", err)
	}
}
