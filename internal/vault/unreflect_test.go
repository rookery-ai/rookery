package vault

import (
	"errors"
	"os"
	"testing"
	"time"
)

// TestUnreflectRemovesNoteAndSidecar is the core of the "deleted items still
// appear in the knowledge base" fix: reflection is a projection of the database,
// so deleting the row without unreflecting leaves a ghost note behind that keeps
// showing up in the KB browser and in search.
func TestUnreflectRemovesNoteAndSidecar(t *testing.T) {
	v := New(t.TempDir())
	r := v.Reflector()
	const user = "u1"

	if err := r.ReflectChat(user, ChatNote{ID: "chat-1", Name: "Trip planning",
		CreatedAt: time.Now(), Messages: []ChatTurn{{Role: "user", Content: "hey"}}}); err != nil {
		t.Fatalf("ReflectChat: %v", err)
	}
	if _, err := v.ReadNote(user, "chats/chat-1.md"); err != nil {
		t.Fatalf("precondition: note not written: %v", err)
	}
	if _, err := v.ReadNote(user, ".kb/db-export/chats/chat-1.json"); err != nil {
		t.Fatalf("precondition: sidecar not written: %v", err)
	}

	if err := r.UnreflectChat(user, "chat-1"); err != nil {
		t.Fatalf("UnreflectChat: %v", err)
	}
	if _, err := v.ReadNote(user, "chats/chat-1.md"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("note survived deletion: err=%v", err)
	}
	if _, err := v.ReadNote(user, ".kb/db-export/chats/chat-1.json"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("sidecar survived deletion: err=%v", err)
	}
}

// TestUnreflectIsIdempotent: the caller has already committed the DB delete by
// the time this runs, and reflection itself is best-effort (the note may never
// have been written), so a missing file must not surface as an error for an
// operation that did succeed.
func TestUnreflectIsIdempotent(t *testing.T) {
	v := New(t.TempDir())
	r := v.Reflector()

	if err := r.UnreflectChat("u1", "never-existed"); err != nil {
		t.Errorf("unreflecting a note that was never written: %v", err)
	}
	if err := r.ReflectInbox("u1", InboxNote{ID: "i1", Source: "reminder", Body: "x"}); err != nil {
		t.Fatalf("ReflectInbox: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := r.UnreflectInbox("u1", "i1"); err != nil {
			t.Errorf("UnreflectInbox pass %d: %v", i, err)
		}
	}
	// A nil reflector is a no-op, matching every other method here.
	var nilR *Reflector
	if err := nilR.UnreflectChat("u1", "c1"); err != nil {
		t.Errorf("nil reflector: %v", err)
	}
}

// TestUnreflectAgentRunsRemovesOnlyThatAgents covers the case the agent-delete
// path could not handle structurally: run sidecars are keyed by RUN id under
// .kb/db-export/agent_runs/, so nothing in the path says which agent they belong
// to. They can only be matched by reading each one back — and a run belonging to
// a DIFFERENT agent must survive.
func TestUnreflectAgentRunsRemovesOnlyThatAgents(t *testing.T) {
	v := New(t.TempDir())
	r := v.Reflector()
	const user = "u1"

	reflectRun := func(runID, agentID string) {
		t.Helper()
		if err := r.ReflectAgentRun(user, RunNote{RunID: runID, AgentID: agentID,
			AgentName: "A", StartedAt: time.Now(), Output: "out"}); err != nil {
			t.Fatalf("ReflectAgentRun: %v", err)
		}
	}
	reflectRun("run-1", "agent-doomed")
	reflectRun("run-2", "agent-doomed")
	reflectRun("run-3", "agent-kept")

	n, err := r.UnreflectAgentRuns(user, "agent-doomed")
	if err != nil {
		t.Fatalf("UnreflectAgentRuns: %v", err)
	}
	if n != 2 {
		t.Errorf("removed %d sidecars, want 2", n)
	}
	for _, id := range []string{"run-1", "run-2"} {
		if _, err := v.ReadNote(user, ".kb/db-export/agent_runs/"+id+".json"); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("sidecar %s survived: err=%v", id, err)
		}
	}
	if _, err := v.ReadNote(user, ".kb/db-export/agent_runs/run-3.json"); err != nil {
		t.Errorf("another agent's sidecar was deleted: %v", err)
	}
}

// TestUnreflectAgentRunsWithNoSidecarDir: an agent that never ran has no
// agent_runs directory at all. Deleting it must not report an error.
func TestUnreflectAgentRunsWithNoSidecarDir(t *testing.T) {
	v := New(t.TempDir())
	n, err := v.Reflector().UnreflectAgentRuns("u1", "agent-never-ran")
	if err != nil {
		t.Fatalf("UnreflectAgentRuns on a vault with no runs: %v", err)
	}
	if n != 0 {
		t.Errorf("removed %d, want 0", n)
	}
}
