package gateway

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// runHarness drives handleRun with the two closures GatewayManager.dispatch
// supplies, recording which one carried what. Keeping them apart is the point:
// the running notice must go through sendProgress (which owns the editable
// placeholder) while the agent's own output goes through send.
type runHarness struct {
	sent          []string
	progressLines []string
}

func newRunRouter(onRun AgentRunHandler) *Router {
	// db and designFlow stay nil: unsavedDraftHint returns "" whenever it cannot
	// PROVE the agent is absent, so the normal run path proceeds.
	return NewRouter(nil, nil, onRun, nil, nil)
}

func (h *runHarness) send(s string)     { h.sent = append(h.sent, s) }
func (h *runHarness) progress(s string) { h.progressLines = append(h.progressLines, s) }

func (h *runHarness) all() string {
	return strings.Join(append(append([]string{}, h.sent...), h.progressLines...), " ~ ")
}

// A [SILENT] run never reaches a SendOutput site, so before this fix the user
// was left on "Running agent…" forever with no signal the run had finished.
func TestSilentRunReportsCompletion(t *testing.T) {
	h := &runHarness{}
	r := newRunRouter(func(ctx context.Context, workspaceID, agentName string, send func(string)) error {
		return nil // silent: delivers nothing, returns no error
	})

	if err := r.handleRun(context.Background(), Message{WorkspaceID: "ws1"}, "weather",
		h.send, h.progress); err != nil {
		t.Fatalf("handleRun: %v", err)
	}

	joined := h.all()
	if !strings.Contains(joined, "Running agent") {
		t.Errorf("the running notice never appeared: %s", joined)
	}
	if !strings.Contains(joined, "finished") || !strings.Contains(joined, "no notification") {
		t.Errorf("a silent run must report completion, got: %s", joined)
	}
	if !strings.Contains(joined, "weather") {
		t.Errorf("the completion notice must name the agent, got: %s", joined)
	}
	// Both lines ride the placeholder channel so the second EDITS the first.
	if len(h.progressLines) != 2 {
		t.Errorf("expected the notice and the completion on sendProgress, got %v", h.progressLines)
	}
}

// A failed run used to be posted twice: once by the runner via SendOutput, then
// again by GatewayManager.dispatch appending "An error occurred: …" to the
// error handleRun returned.
func TestFailedRunIsReportedOnlyOnce(t *testing.T) {
	h := &runHarness{}
	r := newRunRouter(func(ctx context.Context, workspaceID, agentName string, send func(string)) error {
		send("⚠️ This agent run failed: quota exhausted.")
		return errors.New("⚠️ This agent run failed: quota exhausted.")
	})

	err := r.handleRun(context.Background(), Message{WorkspaceID: "ws1"}, "weather",
		h.send, h.progress)
	if err != nil {
		t.Fatalf("an already-delivered failure must not propagate (dispatch would post it again): %v", err)
	}
	if len(h.sent) != 1 {
		t.Errorf("expected exactly one failure message, got %v", h.sent)
	}
}

// A failure that never reached the user must still propagate, or it would be
// swallowed entirely.
func TestUndeliveredFailurePropagates(t *testing.T) {
	h := &runHarness{}
	r := newRunRouter(func(ctx context.Context, workspaceID, agentName string, send func(string)) error {
		return errors.New("boom")
	})

	if err := r.handleRun(context.Background(), Message{WorkspaceID: "ws1"}, "weather",
		h.send, h.progress); err == nil {
		t.Fatal("an undelivered failure must propagate so dispatch can report it")
	}
}

// A run that produced output must not also get a "finished — no notification"
// line, and must not leave a second stray notice behind.
func TestProducingRunDoesNotReportSilence(t *testing.T) {
	h := &runHarness{}
	r := newRunRouter(func(ctx context.Context, workspaceID, agentName string, send func(string)) error {
		send("25°C, clear sky")
		return nil
	})

	if err := r.handleRun(context.Background(), Message{WorkspaceID: "ws1"}, "weather",
		h.send, h.progress); err != nil {
		t.Fatalf("handleRun: %v", err)
	}
	if strings.Contains(h.all(), "no notification") {
		t.Errorf("a producing run must not report silence: %s", h.all())
	}
	if len(h.progressLines) != 1 {
		t.Errorf("expected only the running notice on sendProgress, got %v", h.progressLines)
	}
}
