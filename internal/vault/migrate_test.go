package vault

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMigrateLegacyLayout verifies the three legacy directories are consolidated
// into per-user vaults, idempotently and without clobbering existing vault data.
func TestMigrateLegacyLayout(t *testing.T) {
	dataDir := t.TempDir()
	v := New(dataDir)
	const user = "user-1"

	// Seed legacy layout: an agent, a skill, and a memory jsonl.
	mkfile(t, filepath.Join(dataDir, "agents", user, "agent-a", "AGENT.md"), "# agent a")
	mkfile(t, filepath.Join(dataDir, "agents", user, "agent-a", "tools", "x.py"), "print(1)")
	mkfile(t, filepath.Join(dataDir, "skills", user, "my-skill", "SKILL.md"), "# skill")
	mkfile(t, filepath.Join(dataDir, "memory", user, "memory.jsonl"),
		`{"id":"1","content":"a memory","created_at":"2021-01-01T00:00:00Z"}`+"\n")

	if err := v.MigrateLegacyLayout(); err != nil {
		t.Fatalf("MigrateLegacyLayout: %v", err)
	}

	// Agent moved under vaults/<user>/agents/agent-a.
	if _, err := os.Stat(filepath.Join(v.Root(user), "agents", "agent-a", "AGENT.md")); err != nil {
		t.Errorf("agent not migrated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(v.Root(user), "agents", "agent-a", "tools", "x.py")); err != nil {
		t.Errorf("agent tools not migrated: %v", err)
	}
	// Skill moved.
	if _, err := os.Stat(filepath.Join(v.Root(user), "skills", "my-skill", "SKILL.md")); err != nil {
		t.Errorf("skill not migrated: %v", err)
	}
	// Memory converted to a markdown note.
	if _, err := os.Stat(filepath.Join(v.Root(user), "memory", "1.md")); err != nil {
		t.Errorf("memory not migrated to .md: %v", err)
	}
	// Legacy roots drained and removed.
	for _, p := range []string{"agents", "skills", "memory"} {
		if _, err := os.Stat(filepath.Join(dataDir, p)); !os.IsNotExist(err) {
			t.Errorf("legacy %s/ not removed: err=%v", p, err)
		}
	}
	// Scaffold present.
	if _, err := os.Stat(filepath.Join(v.Root(user), "README.md")); err != nil {
		t.Errorf("scaffold README missing: %v", err)
	}

	// Idempotent: a second run is a no-op and must not error.
	if err := v.MigrateLegacyLayout(); err != nil {
		t.Fatalf("second MigrateLegacyLayout: %v", err)
	}
}

func TestMigrateNoLegacyIsNoop(t *testing.T) {
	v := New(t.TempDir())
	if err := v.MigrateLegacyLayout(); err != nil {
		t.Fatalf("MigrateLegacyLayout on empty: %v", err)
	}
}

// TestRemoveLegacyInboxNotes: builds before the notification-reflection removal
// wrote inbox/<uuid>.md plus a sidecar per message. Both go; nothing else may.
func TestRemoveLegacyInboxNotes(t *testing.T) {
	v := New(t.TempDir())
	const user = "u1"
	mustWrite(t, v, user, "inbox/0a107b38.md", "# ⏰ Reminder\n\n⏰ Reminder: put quinoa\n")
	mustWrite(t, v, user, "inbox/92e2068a.md", "# 🤖 weather (cron)\n\n25°C\n")
	mustWrite(t, v, user, ".kb/db-export/inbox_messages/0a107b38.json", "{}")
	mustWrite(t, v, user, ".kb/db-export/chats/c1.json", "{}")
	mustWrite(t, v, user, "notes/keep.md", "mine")

	for i := 0; i < 2; i++ { // idempotent: a second boot must be a clean no-op
		if err := v.RemoveLegacyInboxNotes(); err != nil {
			t.Fatalf("RemoveLegacyInboxNotes pass %d: %v", i, err)
		}
	}

	root := v.Root(user)
	for _, gone := range []string{"inbox", filepath.Join(InternalDir, "db-export", "inbox_messages")} {
		if _, err := os.Stat(filepath.Join(root, gone)); !os.IsNotExist(err) {
			t.Errorf("%s survived the sweep: err=%v", gone, err)
		}
	}
	for _, kept := range []string{"notes/keep.md", ".kb/db-export/chats/c1.json"} {
		if _, err := v.ReadNote(user, kept); err != nil {
			t.Errorf("sweep removed %s: %v", kept, err)
		}
	}
}

func TestRemoveLegacyInboxNotesOnEmptyVaultsDir(t *testing.T) {
	if err := New(t.TempDir()).RemoveLegacyInboxNotes(); err != nil {
		t.Fatalf("no vaults dir should be a no-op: %v", err)
	}
}

func mkfile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
}
