package agentrunner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/vault"
)

// seedHistory gives an agent n finished runs in the past, each with a run note,
// a sidecar and a transcript, as if it had run n times before this change.
func (h *stopHarness) seedHistory(a *db.Agent, n int) []string {
	h.t.Helper()
	base := time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		id := uuid.New().String()
		ids[i] = id
		started := base.Add(time.Duration(i) * time.Minute)
		if err := h.db.CreateAgentRun(&db.AgentRun{ID: id, AgentID: a.ID, WorkspaceID: h.workspaceID, Trigger: "cron"}); err != nil {
			h.t.Fatal(err)
		}
		if err := h.db.FinishAgentRun(id, db.RunOutcome{Stdout: "old", Transcript: "[]", TotalTokens: 10}); err != nil {
			h.t.Fatal(err)
		}
		if _, err := h.db.Exec(`UPDATE agent_runs SET started_at=? WHERE id=?`, started.Format("2006-01-02 15:04:05"), id); err != nil {
			h.t.Fatal(err)
		}
		if err := h.v.Reflector().ReflectAgentRun(h.workspaceID, vault.RunNote{RunID: id, AgentID: a.ID,
			AgentName: a.Name, StartedAt: started, Output: "old"}); err != nil {
			h.t.Fatal(err)
		}
	}
	return ids
}

func (h *stopHarness) runNotes(a *db.Agent) []string {
	h.t.Helper()
	matches, _ := filepath.Glob(filepath.Join(h.v.Root(h.workspaceID), "agents", a.ID, "logs", "run_*.md"))
	return matches
}

// A run brings its own agent under the cap: 16 old runs plus this one leave 15
// full records, in the database and in the knowledge base alike.
func TestARunKeepsOnlyTheLast15Runs(t *testing.T) {
	h := newStopHarness(t)
	a := h.agent("Keeper", "say hello")
	old := h.seedHistory(a, 16)

	if err := h.runOnce(a, &collector{}, &collector{}); err != nil {
		t.Fatalf("run: %v", err)
	}

	if notes := h.runNotes(a); len(notes) != RunHistoryKeep {
		t.Errorf("%d run notes left, want %d", len(notes), RunHistoryKeep)
	}
	for i, id := range old {
		r, err := h.db.GetAgentRun(id)
		if err != nil || r == nil {
			t.Fatalf("run %d: %v", i, err)
		}
		slim := r.Stdout == "" && r.Transcript == ""
		if i < 2 && !slim {
			t.Errorf("old run %d should have been slimmed", i)
		}
		if i >= 2 && slim {
			t.Errorf("run %d is among the last 15 and lost its record", i)
		}
		if r.TotalTokens != 10 {
			t.Errorf("run %d lost its usage", i)
		}
	}
	sidecars, _ := filepath.Glob(filepath.Join(h.v.Root(h.workspaceID), ".kb", "db-export", "agent_runs", "*.json"))
	if len(sidecars) != RunHistoryKeep {
		t.Errorf("%d sidecars left, want %d", len(sidecars), RunHistoryKeep)
	}
}

// The startup pass reaches agents that have not run since the cap existed.
func TestPruneAllRunHistoryCoversAgentsThatDoNotRun(t *testing.T) {
	h := newStopHarness(t)
	a := h.agent("Idle", "x")
	b := h.agent("Small", "x")
	h.seedHistory(a, 18)
	h.seedHistory(b, 4)

	if n := h.runner.PruneAllRunHistory(); n != 2 {
		t.Errorf("visited %d agents, want 2", n)
	}
	if got := len(h.runNotes(a)); got != RunHistoryKeep {
		t.Errorf("idle agent has %d notes, want %d", got, RunHistoryKeep)
	}
	if got := len(h.runNotes(b)); got != 4 {
		t.Errorf("agent under the cap has %d notes, want 4", got)
	}
}

// Every command a run starts sees a TMPDIR private to that run, and the folder is
// gone once the run ends — whatever the coder left in it.
func TestARunGetsItsOwnScratchFolderAndItIsDeleted(t *testing.T) {
	h := newStopHarness(t)
	script := filepath.Join(h.dir, "fake-coder")
	body := fmt.Sprintf(`#!/bin/sh
printf '%%s' "$TMPDIR" > %[1]q/tmpdir
echo '<html>page</html>' > "$TMPDIR/page.html"
echo '[CHAT] done'
`, h.dir)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	a := h.agent("Scratchy", "fetch a page")

	if err := h.runOnce(a, &collector{}, &collector{}); err != nil {
		t.Fatalf("run: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(h.dir, "tmpdir"))
	if err != nil {
		t.Fatalf("the coder never ran: %v", err)
	}
	tmp := string(raw)
	wantParent := filepath.Join(h.dir, "homes", h.workspaceID, "tmp")
	if filepath.Dir(tmp) != wantParent || !strings.HasPrefix(filepath.Base(tmp), "run-") {
		t.Fatalf("TMPDIR = %q, want %s/run-<id>", tmp, wantParent)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("the run's scratch folder survived the run: %v", err)
	}
}
