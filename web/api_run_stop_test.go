package web

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rookery-ai/rookery/internal/agentdesigner"
	"github.com/rookery-ai/rookery/internal/agentrunner"
	"github.com/rookery-ai/rookery/internal/coder"
	"github.com/rookery-ai/rookery/internal/db"
)

// stoppableRunner wires a runner whose coder is a real process that sleeps until it
// is killed, so the endpoint has to stop an actual run.
func stoppableRunner(t *testing.T, s *Server, wsID string, a *db.Agent) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-coder")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 300\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	vaults := filepath.Join(dir, "vaults")
	path := agentdesigner.AgentDescPath(vaults, wsID, a.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("loop"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := coder.New(script, time.Minute, filepath.Join(dir, "homes"), dir).WithBackendType("generic").WithSandbox(false)
	s.runner = agentrunner.New(s.db, nil, vaults, filepath.Join(dir, "homes"), dir, c, vaults)
}

func TestAPIStopRunStopsAScheduledRun(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a := seedAgent(t, s, wsID)
	stoppableRunner(t, s, wsID, a)

	// Started the way the scheduler starts it: no entry in the web run tracker.
	done := make(chan error, 1)
	go func() {
		done <- s.runner.Run(context.Background(), agentrunner.RunInput{
			AgentID: a.ID, WorkspaceID: wsID, Trigger: "cron",
		})
	}()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if run, err := s.db.GetUnfinishedAgentRun(a.ID); err == nil && run != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the run never started")
		}
		time.Sleep(20 * time.Millisecond)
	}

	rec := doJSON(t, s, http.MethodPost, "/api/v1/agents/"+a.ID+"/run/stop", nil, cookies)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("stop: %d %s", rec.Code, rec.Body.String())
	}
	select {
	case err := <-done:
		if !errors.Is(err, agentrunner.ErrRunStopped) {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the run did not end")
	}

	// The run list reads stopped, and pressing Stop again reports nothing running.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/agents/"+a.ID, nil, cookies)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"stopped"`) {
		t.Fatalf("agent detail should show a stopped run: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodPost, "/api/v1/agents/"+a.ID+"/run/stop", nil, cookies)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second stop: %d, want 404", rec.Code)
	}
}

func TestAPIStopRunWithNothingRunning(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a := seedAgent(t, s, wsID)

	// No runner configured: nothing can be running.
	rec := doJSON(t, s, http.MethodPost, "/api/v1/agents/"+a.ID+"/run/stop", nil, cookies)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("stop with no runner: %d, want 404", rec.Code)
	}
	stoppableRunner(t, s, wsID, a)
	rec = doJSON(t, s, http.MethodPost, "/api/v1/agents/"+a.ID+"/run/stop", nil, cookies)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("stop with nothing running: %d, want 404", rec.Code)
	}
}

func TestAPIStopRunRejectsAnotherWorkspacesAgent(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)
	rec := doJSON(t, s, http.MethodPost, "/api/v1/agents/no-such-agent/run/stop", nil, cookies)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("stop of an unknown agent: %d, want 404", rec.Code)
	}
}

func TestStoppedRunStatusIsNotFailed(t *testing.T) {
	exit := -1
	run := &db.AgentRun{ExitCode: &exit, Stopped: true}
	if got := toAPIRun(run)["status"]; got != "stopped" {
		t.Errorf("toAPIRun status = %v, want stopped", got)
	}
	dash := toAPIDashboardRun(&db.AgentRunWithName{AgentRun: *run, AgentName: "A"})
	if dash.Status != "stopped" {
		t.Errorf("toAPIDashboardRun status = %v, want stopped", dash.Status)
	}
	run.Stopped = false
	if got := toAPIRun(run)["status"]; got != "failed" {
		t.Errorf("a failed run should stay failed, got %v", got)
	}
}
