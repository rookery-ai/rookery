package agentrunner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rookery-ai/rookery/internal/agentdesigner"
	"github.com/rookery-ai/rookery/internal/coder"
	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/vault"
)

// stopHarness drives real runs through a real subprocess, so a Stop has to kill an
// actual process. The fake coder records every prompt it receives, then:
//   - __STOPTEST_HANG__ in the prompt: reports it started and sleeps until killed;
//   - __STOPTEST_CALLCHILD__ in the prompt: asks to call the agent named "Child";
//   - otherwise: answers with one [CHAT] line.
type stopHarness struct {
	t           *testing.T
	db          *db.DB
	runner      *Runner
	v           *vault.Vault
	workspaceID string
	dir         string
}

func newStopHarness(t *testing.T) *stopHarness {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	workspaceID := uuid.New().String()
	if err := database.CreateWorkspace(&db.Workspace{ID: workspaceID, Name: "tester"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "fake-coder")
	body := fmt.Sprintf(`#!/bin/sh
prompt="$2"
printf '%%s' "$prompt" > %[1]q/prompt.$$
case "$prompt" in
  *__STOPTEST_HANG__*) touch %[1]q/started; exec sleep 300 ;;
esac
case "$prompt" in
  *__STOPTEST_CALLCHILD__*) echo '[CALL: Child]'; exit 0 ;;
esac
echo '[CHAT] all done'
`, dir)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	vaults := filepath.Join(dir, "vaults")
	c := coder.New(script, time.Minute, filepath.Join(dir, "homes"), dir).WithBackendType("generic").WithSandbox(false)
	v := vault.New(dir)
	r := New(database, nil, vaults, filepath.Join(dir, "homes"), dir, c, vaults).WithVault(v)
	return &stopHarness{t: t, db: database, runner: r, v: v, workspaceID: workspaceID, dir: dir}
}

func (h *stopHarness) agent(name, instructions string) *db.Agent {
	h.t.Helper()
	a := &db.Agent{ID: uuid.New().String(), WorkspaceID: h.workspaceID, Name: name, Active: true}
	if err := h.db.CreateAgent(a); err != nil {
		h.t.Fatalf("create agent: %v", err)
	}
	path := agentdesigner.AgentDescPath(h.runner.agentsDir, h.workspaceID, a.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(instructions), 0o644); err != nil {
		h.t.Fatal(err)
	}
	return a
}

// collector stands in for SendOutput and OnProgress.
type collector struct {
	mu    sync.Mutex
	lines []string
}

func (c *collector) add(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, s)
}

func (c *collector) all() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.lines...)
}

func (h *stopHarness) start(ctx context.Context, a *db.Agent, sent, progress *collector) <-chan error {
	done := make(chan error, 1)
	go func() {
		done <- h.runner.Run(ctx, RunInput{
			AgentID: a.ID, WorkspaceID: h.workspaceID, Trigger: "manual",
			SendOutput: sent.add, OnProgress: progress.add,
		})
	}()
	return done
}

func (h *stopHarness) waitForHang() {
	h.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(h.dir, "started")); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.t.Fatal("the fake coder never started")
}

func waitRun(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(30 * time.Second):
		t.Fatal("the run did not end; a stop must kill the coder")
		return nil
	}
}

func (h *stopHarness) latest(a *db.Agent) *db.AgentRun {
	h.t.Helper()
	runs, err := h.db.ListAgentRuns(a.ID, 1)
	if err != nil || len(runs) != 1 {
		h.t.Fatalf("ListAgentRuns: %v, %v", runs, err)
	}
	full, err := h.db.GetAgentRun(runs[0].ID)
	if err != nil || full == nil {
		h.t.Fatalf("GetAgentRun: %v, %v", full, err)
	}
	return full
}

func TestStopEndsARunAndRecordsItAsStopped(t *testing.T) {
	h := newStopHarness(t)
	a := h.agent("Looper", "__STOPTEST_HANG__")
	sent, progress := &collector{}, &collector{}
	done := h.start(context.Background(), a, sent, progress)
	h.waitForHang()

	if got := h.runner.RunningAgentNames(h.workspaceID); !reflect.DeepEqual(got, []string{"Looper"}) {
		t.Errorf("RunningAgentNames = %v", got)
	}
	if !h.runner.StopAgent(h.workspaceID, a.ID) {
		t.Fatal("StopAgent reported nothing to stop")
	}
	if err := waitRun(t, done); !errors.Is(err, ErrRunStopped) {
		t.Fatalf("Run returned %v, want ErrRunStopped", err)
	}

	run := h.latest(a)
	if !run.Stopped || run.FinishedAt == nil {
		t.Errorf("run should be finished and stopped: %+v", run)
	}
	if !strings.Contains(run.Transcript, "Stopped by the owner") {
		t.Errorf("transcript should carry the notice: %s", run.Transcript)
	}

	// "Just stop it and nothing else."
	if s := sent.all(); len(s) != 0 {
		t.Errorf("a stopped run must send nothing, sent %v", s)
	}
	if msgs, err := h.db.ListInboxMessages(h.workspaceID, 10, 0); err != nil || len(msgs) != 0 {
		t.Errorf("a stopped run must add no inbox row: %v, %v", msgs, err)
	}
	if p := progress.all(); len(p) == 0 || p[len(p)-1] != runStoppedNotice {
		t.Errorf("the live view should end with the notice: %v", p)
	}
	if got := h.runner.RunningAgentNames(h.workspaceID); len(got) != 0 {
		t.Errorf("a stopped run must leave the registry: %v", got)
	}
	if h.runner.StopAgent(h.workspaceID, a.ID) {
		t.Error("stopping a finished run should report nothing to stop")
	}
}

func TestStoppingAParentStopsItsChild(t *testing.T) {
	h := newStopHarness(t)
	parent := h.agent("Parent", "__STOPTEST_CALLCHILD__")
	child := h.agent("Child", "__STOPTEST_HANG__")
	done := h.start(context.Background(), parent, &collector{}, &collector{})
	h.waitForHang()

	// A bare /agent stop must see one agent here, not two.
	if got := h.runner.RunningAgentNames(h.workspaceID); !reflect.DeepEqual(got, []string{"Parent"}) {
		t.Errorf("RunningAgentNames = %v, want only the parent", got)
	}
	if !h.runner.StopAgent(h.workspaceID, parent.ID) {
		t.Fatal("StopAgent reported nothing to stop")
	}
	if err := waitRun(t, done); !errors.Is(err, ErrRunStopped) {
		t.Fatalf("Run returned %v, want ErrRunStopped", err)
	}
	if !h.latest(parent).Stopped {
		t.Error("the parent should be recorded as stopped")
	}
	if !h.latest(child).Stopped {
		t.Error("the child should be recorded as stopped")
	}
}

func TestStoppingOnlyAChildLeavesItsParentRunning(t *testing.T) {
	h := newStopHarness(t)
	parent := h.agent("Parent", "__STOPTEST_CALLCHILD__")
	child := h.agent("Child", "__STOPTEST_HANG__")
	done := h.start(context.Background(), parent, &collector{}, &collector{})
	h.waitForHang()

	if !h.runner.StopAgentByName(h.workspaceID, "child") {
		t.Fatal("StopAgentByName should ignore case and find the child")
	}
	// The parent carries on with a warning, and finishes on its own terms.
	if err := waitRun(t, done); errors.Is(err, ErrRunStopped) {
		t.Fatalf("the parent was not stopped, got %v", err)
	}
	if h.latest(parent).Stopped {
		t.Error("the parent should not be recorded as stopped")
	}
	if !h.latest(child).Stopped {
		t.Error("the child should be recorded as stopped")
	}
}

// A server shutdown cancels the same context. It must stay an interrupted run,
// which the boot path retries, and never read as the owner's decision.
func TestShutdownIsNotAStop(t *testing.T) {
	h := newStopHarness(t)
	a := h.agent("Looper", "__STOPTEST_HANG__")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := h.start(ctx, a, &collector{}, &collector{})
	h.waitForHang()
	cancel()
	if err := waitRun(t, done); errors.Is(err, ErrRunStopped) {
		t.Fatalf("a shutdown was reported as a stop: %v", err)
	}
	if h.latest(a).Stopped {
		t.Error("a shutdown must not be recorded as stopped")
	}
}

func TestStopIsScopedToTheWorkspace(t *testing.T) {
	h := newStopHarness(t)
	a := h.agent("Looper", "__STOPTEST_HANG__")
	done := h.start(context.Background(), a, &collector{}, &collector{})
	h.waitForHang()
	defer func() {
		h.runner.StopAgent(h.workspaceID, a.ID)
		_ = waitRun(t, done)
	}()

	if h.runner.StopAgent("another-workspace", a.ID) {
		t.Error("another workspace stopped this agent by id")
	}
	if h.runner.StopAgentByName("another-workspace", "Looper") {
		t.Error("another workspace stopped this agent by name")
	}
	if got := h.runner.RunningAgentNames("another-workspace"); len(got) != 0 {
		t.Errorf("another workspace can see this run: %v", got)
	}
}

// Only the run straight after a stop hears about it.
func TestNextRunIsToldOnce(t *testing.T) {
	h := newStopHarness(t)
	a := h.agent("Looper", "__STOPTEST_HANG__")
	done := h.start(context.Background(), a, &collector{}, &collector{})
	h.waitForHang()
	h.runner.StopAgent(h.workspaceID, a.ID)
	if err := waitRun(t, done); !errors.Is(err, ErrRunStopped) {
		t.Fatalf("Run returned %v", err)
	}

	// The agent stops asking to hang, so the following runs finish normally.
	path := agentdesigner.AgentDescPath(h.runner.agentsDir, h.workspaceID, a.ID)
	if err := os.WriteFile(path, []byte("carry on"), 0o644); err != nil {
		t.Fatal(err)
	}

	runAndReadPrompt := func() string {
		t.Helper()
		before, _ := filepath.Glob(filepath.Join(h.dir, "prompt.*"))
		if err := waitRun(t, h.start(context.Background(), a, &collector{}, &collector{})); err != nil {
			t.Fatalf("Run: %v", err)
		}
		after, _ := filepath.Glob(filepath.Join(h.dir, "prompt.*"))
		seen := make(map[string]bool)
		for _, b := range before {
			seen[b] = true
		}
		for _, p := range after {
			if !seen[p] {
				data, err := os.ReadFile(p)
				if err != nil {
					t.Fatal(err)
				}
				return string(data)
			}
		}
		t.Fatal("no new prompt recorded")
		return ""
	}

	if next := runAndReadPrompt(); !strings.Contains(next, "<previous_run>") {
		t.Error("the run after a stop should be told")
	}
	if later := runAndReadPrompt(); strings.Contains(later, "<previous_run>") {
		t.Error("only the run straight after a stop should be told")
	}
}

// A run that was never stopped reports its normal outcome and keeps delivering.
func TestUnstoppedRunIsUnchanged(t *testing.T) {
	h := newStopHarness(t)
	a := h.agent("Calm", "say hello")
	sent := &collector{}
	if err := waitRun(t, h.start(context.Background(), a, sent, &collector{})); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if h.latest(a).Stopped {
		t.Error("a normal run must not read as stopped")
	}
	if s := sent.all(); len(s) != 1 || !strings.Contains(s[0], "all done") {
		t.Errorf("a normal run should deliver as before: %v", s)
	}
	if h.runner.StopAgent(h.workspaceID, a.ID) {
		t.Error("a finished run has nothing to stop")
	}
}

// A note's text is what a later run can read, so the stop has to be on disk too.
func TestStoppedRunWritesItsNote(t *testing.T) {
	h := newStopHarness(t)
	a := h.agent("Looper", "__STOPTEST_HANG__")
	done := h.start(context.Background(), a, &collector{}, &collector{})
	h.waitForHang()
	h.runner.StopAgent(h.workspaceID, a.ID)
	if err := waitRun(t, done); !errors.Is(err, ErrRunStopped) {
		t.Fatalf("Run returned %v", err)
	}
	logs, _ := filepath.Glob(filepath.Join(agentdesigner.AgentDir(h.runner.agentsDir, h.workspaceID, a.ID), "logs", "run_*.md"))
	if len(logs) != 1 {
		t.Fatalf("run notes = %v", logs)
	}
	data, err := os.ReadFile(logs[0])
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "status: stopped") || !strings.Contains(s, "## Stopped") {
		t.Errorf("the note should record the stop:\n%s", s)
	}
}
