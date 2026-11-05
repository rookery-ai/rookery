package agentrunner

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rookery-ai/rookery/internal/coder"
	"github.com/rookery-ai/rookery/internal/llm"
)

// onStepProvider asks for one tool call, then answers with a [CHAT] line.
type onStepProvider struct{ calls atomic.Int32 }

func (p *onStepProvider) Name() string { return "onstep-fake" }

func (p *onStepProvider) Complete(context.Context, llm.Request) (*llm.Response, error) {
	if p.calls.Add(1) == 1 {
		return &llm.Response{ToolCalls: []llm.ToolCall{{
			ID: "t1", Name: "list_dir", Args: json.RawMessage(`{"path":"."}`),
		}}}, nil
	}
	return &llm.Response{Content: "[CHAT] the agent's real message"}, nil
}

var onStepFake = &onStepProvider{}

func init() {
	llm.RegisterProvider("onstep-fake", func(llm.Config) (llm.Provider, error) { return onStepFake, nil })
}

// OnProgress also carries [CHAT] lines and the final output. A list of steps fed
// from it would present the agent's message as one of its steps.
func TestOnStepReceivesOnlyMilestones(t *testing.T) {
	h := newStopHarness(t)
	api := coder.New("claude", time.Minute, filepath.Join(h.dir, "homes"), h.dir).
		WithSandbox(false).
		WithVault(h.v).
		WithAPIConfig("onstep-fake", "fake-model", "fake://test", "KEY").
		WithSecretsLookup(func(context.Context, string, string) (string, error) { return "k", nil })
	h.runner.WithCoderFactory(func(string) *coder.Coder { return api })

	a := h.agent("Stepper", "Do the thing.")
	var steps, progress, sent collector
	err := h.runner.Run(context.Background(), RunInput{
		AgentID: a.ID, WorkspaceID: h.workspaceID, Trigger: "chat",
		SendOutput: sent.add, OnProgress: progress.add, OnStep: steps.add,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	got := steps.all()
	if len(got) != 1 || !strings.HasPrefix(got[0], "🔧 list_dir") {
		t.Errorf("steps = %q, want the one tool milestone", got)
	}
	if !strings.Contains(strings.Join(progress.all(), "\n"), "list_dir") {
		t.Errorf("OnProgress lost the milestone: %q", progress.all())
	}
	if !strings.Contains(strings.Join(sent.all(), "\n"), "real message") {
		t.Errorf("the message was not delivered: %q", sent.all())
	}
}
