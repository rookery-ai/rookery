package vault

import (
	"os"
	"testing"
)

// TestGuardRevertsOutOfScopeWrites is the core safety property: an agent may
// freely write its own dir but any change it makes to the user's protected
// content (notes, memory, etc.) is reverted after the run.
func TestGuardRevertsOutOfScopeWrites(t *testing.T) {
	v := New(t.TempDir())
	const user = "u1"
	g := v.NewGuard()

	// Seed the user's protected content and an agent workspace.
	mustWrite(t, v, user, "notes/journal.md", "original journal")
	mustWrite(t, v, user, "memory/fact.md", "remembered fact")
	mustWrite(t, v, user, "agents/agent-1/AGENT.md", "agent instructions")

	snap, err := g.Snapshot(user)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	// Simulate the agent run: it modifies a user note (out of scope), deletes
	// another (out of scope), creates a stray note (out of scope), and legitimately
	// writes within its own dir (in scope).
	mustWrite(t, v, user, "notes/journal.md", "VANDALIZED")
	if err := v.Delete(user, "memory/fact.md"); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, v, user, "notes/stray.md", "should not exist")
	mustWrite(t, v, user, "agents/agent-1/notes/learned.md", "legit agent knowledge")
	mustWrite(t, v, user, "agents/agent-1/state.json", `{"x":1}`)

	violations, err := g.Restore(snap)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(violations) != 3 {
		t.Errorf("violations = %v, want 3 (modified, deleted, created)", violations)
	}

	// Protected content restored exactly.
	if got, _ := v.ReadNote(user, "notes/journal.md"); string(got) != "original journal" {
		t.Errorf("journal not reverted: %q", got)
	}
	if got, _ := v.ReadNote(user, "memory/fact.md"); string(got) != "remembered fact" {
		t.Errorf("deleted memory not recreated: %q", got)
	}
	if _, err := v.ReadNote(user, "notes/stray.md"); !os.IsNotExist(err) {
		t.Errorf("stray note not removed: err=%v", err)
	}

	// Agent's own dir left completely untouched.
	if got, _ := v.ReadNote(user, "agents/agent-1/notes/learned.md"); string(got) != "legit agent knowledge" {
		t.Errorf("agent's own write was wrongly reverted: %q", got)
	}
	if got, _ := v.ReadNote(user, "agents/agent-1/state.json"); string(got) != `{"x":1}` {
		t.Errorf("agent state wrongly reverted: %q", got)
	}
}

func TestGuardNoViolationsWhenWellBehaved(t *testing.T) {
	v := New(t.TempDir())
	const user = "u1"
	g := v.NewGuard()
	mustWrite(t, v, user, "notes/a.md", "a")
	mustWrite(t, v, user, "agents/x/AGENT.md", "x")

	snap, _ := g.Snapshot(user)
	// Agent only writes its own dir.
	mustWrite(t, v, user, "agents/x/logs/run.md", "log")

	violations, err := g.Restore(snap)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("violations = %v, want none", violations)
	}
}

// TestGuardIgnoresConcurrentReflections is the regression test for the real-world
// race: while an agent runs for minutes, the reminder/session pollers reflect new
// rows into the vault. Those system writes must NOT be seen as out-of-scope agent
// writes and reverted.
func TestGuardIgnoresConcurrentReflections(t *testing.T) {
	v := New(t.TempDir())
	const user = "u1"
	g := v.NewGuard()
	r := v.Reflector()

	mustWrite(t, v, user, "notes/journal.md", "my journal")

	snap, _ := g.Snapshot(user)

	// Simulate background pollers firing mid-run.
	if err := r.ReflectReminder(user, ReminderNote{ID: "rem-1", Message: "buy milk"}); err != nil {
		t.Fatal(err)
	}
	if err := r.ReflectSession(user, SessionNote{ID: "sess-1", Name: "chat"}); err != nil {
		t.Fatal(err)
	}
	// And the agent legitimately writes its own dir.
	mustWrite(t, v, user, "agents/a1/notes/learned.md", "knowledge")

	violations, err := g.Restore(snap)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("violations = %v, want none (system reflections must be ignored)", violations)
	}
	// The reflected notes must still exist.
	if _, err := v.ReadNote(user, "reminders/rem-1.md"); err != nil {
		t.Errorf("reminder reflection was wrongly deleted: %v", err)
	}
	if _, err := v.ReadNote(user, "sessions/sess-1.md"); err != nil {
		t.Errorf("session reflection was wrongly deleted: %v", err)
	}
}

func TestGuardNilSafe(t *testing.T) {
	var g *Guard
	snap, err := g.Snapshot("u1")
	if err != nil || snap != nil {
		t.Fatalf("nil guard Snapshot = %v, %v", snap, err)
	}
	if v, err := g.Restore(nil); err != nil || v != nil {
		t.Fatalf("nil guard Restore = %v, %v", v, err)
	}
}
