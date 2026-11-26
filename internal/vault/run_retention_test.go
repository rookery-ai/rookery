package vault

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPruneAgentRunsKeepsNewestNotesAndKeptSidecars(t *testing.T) {
	v := New(t.TempDir())
	r := v.Reflector()
	const ws, agent = "ws1", "agent-a"
	base := time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)

	var ids []string
	for i := 0; i < 18; i++ {
		id := fmt.Sprintf("run-%02d", i)
		ids = append(ids, id)
		if err := r.ReflectAgentRun(ws, RunNote{RunID: id, AgentID: agent, AgentName: "A",
			StartedAt: base.Add(time.Duration(i) * time.Minute), Output: "out"}); err != nil {
			t.Fatalf("ReflectAgentRun: %v", err)
		}
	}
	// Another agent's run, and a sidecar nobody can parse: neither is ours to delete.
	if err := r.ReflectAgentRun(ws, RunNote{RunID: "other-run", AgentID: "agent-b", AgentName: "B",
		StartedAt: base, Output: "out"}); err != nil {
		t.Fatal(err)
	}
	root := v.Root(ws)
	sidecars := filepath.Join(root, ".kb", "db-export", "agent_runs")
	if err := os.WriteFile(filepath.Join(sidecars, "corrupt.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Files the agent keeps beside its run notes stay.
	agentDir := filepath.Join(root, "agents", agent)
	for _, rel := range []string{"AGENT.md", "state.md", filepath.Join("logs", "other.txt")} {
		if err := os.WriteFile(filepath.Join(agentDir, rel), []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := r.PruneAgentRuns(ws, agent, 15, ids[3:])
	if err != nil {
		t.Fatalf("PruneAgentRuns: %v", err)
	}
	if removed != 6 {
		t.Errorf("removed %d files, want 6 (3 notes + 3 sidecars)", removed)
	}
	for i, id := range ids {
		note := RunNotePath(agent, base.Add(time.Duration(i)*time.Minute))
		_, noteErr := v.ReadNote(ws, note)
		_, carErr := v.ReadNote(ws, ".kb/db-export/agent_runs/"+id+".json")
		if i < 3 {
			if !errors.Is(noteErr, os.ErrNotExist) || !errors.Is(carErr, os.ErrNotExist) {
				t.Errorf("run %d should be gone: note=%v sidecar=%v", i, noteErr, carErr)
			}
			continue
		}
		if noteErr != nil || carErr != nil {
			t.Errorf("run %d should be kept: note=%v sidecar=%v", i, noteErr, carErr)
		}
	}
	for _, rel := range []string{".kb/db-export/agent_runs/other-run.json", ".kb/db-export/agent_runs/corrupt.json",
		"agents/agent-a/AGENT.md", "agents/agent-a/state.md", "agents/agent-a/logs/other.txt"} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("%s was deleted: %v", rel, err)
		}
	}
}

// An agent with fewer notes than the cap, or none at all, is left alone.
func TestPruneAgentRunsUnderTheCapIsANoOp(t *testing.T) {
	v := New(t.TempDir())
	r := v.Reflector()
	n, err := r.PruneAgentRuns("ws1", "never-ran", 15, nil)
	if err != nil || n != 0 {
		t.Fatalf("no history: removed=%d err=%v", n, err)
	}
	for i := 0; i < 3; i++ {
		if err := r.ReflectAgentRun("ws1", RunNote{RunID: fmt.Sprint(i), AgentID: "few", AgentName: "F",
			StartedAt: time.Date(2026, 1, 1, 8, i, 0, 0, time.UTC)}); err != nil {
			t.Fatal(err)
		}
	}
	n, err = r.PruneAgentRuns("ws1", "few", 15, []string{"0", "1", "2"})
	if err != nil || n != 0 {
		t.Fatalf("under the cap: removed=%d err=%v", n, err)
	}
	var nilReflector *Reflector
	if n, err := nilReflector.PruneAgentRuns("ws1", "few", 15, nil); err != nil || n != 0 {
		t.Fatalf("nil reflector: %d %v", n, err)
	}
}
