package agentdesigner

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// The chat surface had no way to receive a build result that arrived after the
// turn which started it. runGeneration was synchronous inside Step, and chat's
// only delivery was the send() closure of that turn — so a build outliving its
// deadline was simply lost, and the user saw a progress line then silence.
//
// The web surface never had that problem: the build is detached and the result
// reaches the browser via SSE plus GET /design/state regardless. Detaching
// generation for BOTH surfaces and adding a completion hook is what closes the
// gap, and these tests pin the three properties that make it safe.

// slowCoderScript writes AGENT.md after a delay, so the test can observe the
// window in which a build is running.
const slowCoderScript = `
import sys, time, os
time.sleep(0.4)
open("AGENT.md", "w").write("# Suggested schedule: none\n# Skills: none\nDo the thing.\n")
print("[TEST_OUTPUT]ran fine[/TEST_OUTPUT]")
`

func startedSession(t *testing.T, flow *Flow, workspaceID string) {
	t.Helper()
	flow.mu.Lock()
	flow.sessions[workspaceID] = &DesignSession{
		AgentName: "price-tracker",
		State:     StateDesigning,
	}
	flow.mu.Unlock()
}

// TestStartGenerationReturnsImmediately: the whole point. A turn that starts a
// build must not block for the length of the build.
func TestStartGenerationReturnsImmediately(t *testing.T) {
	flow, workspaceID, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))
	startedSession(t, flow, workspaceID)

	done := make(chan struct{})
	flow.OnBuildComplete(func(string, string, bool, string, error) { close(done) })

	start := time.Now()
	resp, isDone, agentID, err := flow.startGeneration(workspaceID)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("startGeneration: %v", err)
	}
	if isDone || agentID != "" {
		t.Errorf("a build that just started is not done: (%v, %q)", isDone, agentID)
	}
	if !strings.Contains(resp, "Building") {
		t.Errorf("response = %q, want the building placeholder", resp)
	}
	if elapsed > 300*time.Millisecond {
		t.Errorf("startGeneration blocked for %v — it must detach", elapsed)
	}

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the completion hook never fired — a detached build with no hook is a lost build")
	}
}

// TestBuildCompletionHookFiresOnce is the delivery guarantee. Firing twice would
// double-post the agent overview to chat; firing zero times reproduces exactly
// the silence this change exists to remove.
func TestBuildCompletionHookFiresOnce(t *testing.T) {
	flow, workspaceID, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))
	startedSession(t, flow, workspaceID)

	var (
		mu    sync.Mutex
		calls []string
	)
	fired := make(chan struct{}, 4)
	flow.OnBuildComplete(func(ws, response string, _ bool, _ string, _ error) {
		mu.Lock()
		calls = append(calls, ws+"|"+response)
		mu.Unlock()
		fired <- struct{}{}
	})

	if _, _, _, err := flow.startGeneration(workspaceID); err != nil {
		t.Fatalf("startGeneration: %v", err)
	}

	select {
	case <-fired:
	case <-time.After(30 * time.Second):
		t.Fatal("completion hook never fired")
	}
	// Nothing more should arrive.
	select {
	case <-fired:
		t.Fatal("completion hook fired more than once — chat would show the result twice")
	case <-time.After(500 * time.Millisecond):
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("hook calls = %d, want 1", len(calls))
	}
	if !strings.HasPrefix(calls[0], workspaceID+"|") {
		t.Errorf("hook received %q, want it scoped to workspace %q", calls[0], workspaceID)
	}
	if strings.TrimSpace(strings.TrimPrefix(calls[0], workspaceID+"|")) == "" {
		t.Error("hook delivered an empty response — nothing would reach the user")
	}
}

// TestIsGeneratingIsTrueBeforeStartGenerationReturns is the subtle one.
//
// progressCh is created inside startGeneration UNDER THE LOCK, before the
// goroutine starts. Creating it inside the goroutine would leave a window where
// a build is running and IsGenerating still reports false — and the router's new
// concurrency guard reads exactly that signal, so the guard would let a message
// step the FSM mid-build. That is the bug the guard exists to prevent.
func TestIsGeneratingIsTrueBeforeStartGenerationReturns(t *testing.T) {
	flow, workspaceID, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))
	startedSession(t, flow, workspaceID)

	done := make(chan struct{})
	flow.OnBuildComplete(func(string, string, bool, string, error) { close(done) })

	if flow.IsGenerating(workspaceID) {
		t.Fatal("IsGenerating must be false before a build starts")
	}
	if _, _, _, err := flow.startGeneration(workspaceID); err != nil {
		t.Fatalf("startGeneration: %v", err)
	}
	// No sleep: the point is that it is ALREADY true on return.
	if !flow.IsGenerating(workspaceID) {
		t.Fatal("IsGenerating must be true the moment startGeneration returns")
	}

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("build never completed")
	}
}

// TestStartGenerationRefusesASecondConcurrentBuild: two coder runs on one session
// would interleave writes to the same agent directory and the same session
// fields.
func TestStartGenerationRefusesASecondConcurrentBuild(t *testing.T) {
	flow, workspaceID, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))
	startedSession(t, flow, workspaceID)

	var count int
	var mu sync.Mutex
	done := make(chan struct{}, 4)
	flow.OnBuildComplete(func(string, string, bool, string, error) {
		mu.Lock()
		count++
		mu.Unlock()
		done <- struct{}{}
	})

	if _, _, _, err := flow.startGeneration(workspaceID); err != nil {
		t.Fatalf("first startGeneration: %v", err)
	}
	resp, _, _, err := flow.startGeneration(workspaceID)
	if err != nil {
		t.Fatalf("second startGeneration: %v", err)
	}
	if !strings.Contains(resp, "Building") {
		t.Errorf("second call = %q, want the building placeholder", resp)
	}

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("build never completed")
	}
	select {
	case <-done:
		t.Fatal("a second build ran concurrently on one session")
	case <-time.After(500 * time.Millisecond):
	}

	mu.Lock()
	defer mu.Unlock()
	if count != 1 {
		t.Errorf("%d builds ran, want 1", count)
	}
}

// A missing session must be reported rather than panicking on a nil map entry.
func TestStartGenerationWithoutASessionErrors(t *testing.T) {
	flow, _, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))
	if _, _, _, err := flow.startGeneration("no-such-workspace"); err == nil {
		t.Error("expected an error when no design session exists")
	}
}

// A nil hook is the web surface's configuration (it polls /state instead), so it
// must not panic.
func TestDetachedBuildWithNoHookDoesNotPanic(t *testing.T) {
	flow, workspaceID, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))
	startedSession(t, flow, workspaceID)

	if _, _, _, err := flow.startGeneration(workspaceID); err != nil {
		t.Fatalf("startGeneration: %v", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for flow.IsGenerating(workspaceID) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if flow.IsGenerating(workspaceID) {
		t.Fatal("build did not finish")
	}
}
