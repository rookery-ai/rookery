package coder

import (
	"context"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/llm"
)

// A reasoning model spends a tiny budget thinking before it writes a word, so
// with MaxTokens 16 the connection test passed and showed "()" — an empty reply
// that reads as broken. The ping must leave room for an answer, and a reply that
// is still empty must say why instead of rendering nothing.
func TestPingLeavesRoomForAReasoningModel(t *testing.T) {
	c := newTestCoder(t, t.TempDir())
	var gotMax int
	testFake.calls = 0
	testFake.script = func(_ int, req llm.Request) (*llm.Response, error) {
		gotMax = req.MaxTokens
		return &llm.Response{Content: "ok"}, nil
	}
	if _, err := c.Ping(context.Background(), "ws"); err != nil {
		t.Fatal(err)
	}
	if gotMax < 256 {
		t.Fatalf("ping MaxTokens = %d, too small for a reasoning model to answer", gotMax)
	}
}

func TestPingExplainsAReasoningOnlyReply(t *testing.T) {
	c := newTestCoder(t, t.TempDir())
	testFake.calls = 0
	testFake.script = func(int, llm.Request) (*llm.Response, error) {
		return &llm.Response{Content: "", Reasoning: "thinking…", FinishReason: "length"}, nil
	}
	reply, err := c.Ping(context.Background(), "ws")
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(reply, "()") || !strings.Contains(reply, "reasoning") {
		t.Fatalf("an empty reply must be explained, got %q", reply)
	}
}
