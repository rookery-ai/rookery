package vault

import (
	"os"
	"path/filepath"
	"testing"
)

// abs is the absolute on-disk path a host tool would resolve for rel.
func abs(v *Vault, workspaceID, rel string) string {
	return filepath.Join(v.Root(workspaceID), filepath.FromSlash(rel))
}

// mustRead reads a vault-relative file, failing the test if it is missing.
func mustRead(t *testing.T, v *Vault, user, rel string) string {
	t.Helper()
	data, err := os.ReadFile(abs(v, user, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

// TestJournalRevertsTheThreeShapesOfChange covers what a rehearsal can do to a
// note it should not have touched: overwrite it, delete it, or invent it.
//
// All three are reverted through Record, which is the exact path write_file /
// edit_file / save_to_kb take.
func TestJournalRevertsTheThreeShapesOfChange(t *testing.T) {
	v := New(t.TempDir())
	const user = "u1"

	mustWrite(t, v, user, "notes/journal.md", "original journal")
	mustWrite(t, v, user, "memory/fact.md", "remembered fact")

	j := v.NewWriteJournal(user)

	// Modify.
	if err := j.Record(abs(v, user, "notes/journal.md")); err != nil {
		t.Fatalf("Record: %v", err)
	}
	mustWrite(t, v, user, "notes/journal.md", "REHEARSAL OUTPUT")

	// Delete.
	if err := j.Record(abs(v, user, "memory/fact.md")); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := v.Delete(user, "memory/fact.md"); err != nil {
		t.Fatal(err)
	}

	// Create.
	if err := j.Record(abs(v, user, "notes/invented.md")); err != nil {
		t.Fatalf("Record: %v", err)
	}
	mustWrite(t, v, user, "notes/invented.md", "should not survive")

	reverted, err := j.Revert()
	if err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if len(reverted) != 3 {
		t.Fatalf("reverted %d paths, want 3: %v", len(reverted), reverted)
	}

	if got := mustRead(t, v, user, "notes/journal.md"); got != "original journal" {
		t.Errorf("journal.md = %q, want the original bytes back", got)
	}
	if got := mustRead(t, v, user, "memory/fact.md"); got != "remembered fact" {
		t.Errorf("fact.md = %q, want the deleted file recreated", got)
	}
	if _, err := os.Stat(abs(v, user, "notes/invented.md")); !os.IsNotExist(err) {
		t.Error("notes/invented.md survived the revert; a rehearsal's invention must not persist")
	}
}

// TestJournalLeavesTheAgentsOwnDirectoryAlone pins the boundary that makes this
// safe to run during a build at all: the build's OUTPUT lives under agents/,
// and reverting that would delete the agent that was just built.
func TestJournalLeavesTheAgentsOwnDirectoryAlone(t *testing.T) {
	v := New(t.TempDir())
	const user = "u1"
	j := v.NewWriteJournal(user)

	for _, rel := range []string{
		"agents/draft_weather/AGENT.md",
		"agents/draft_weather/tools/fetch.py",
		"agents/draft_weather/state.md",
		"chats/c1.md",
	} {
		if err := j.Record(abs(v, user, rel)); err != nil {
			t.Fatalf("Record(%s): %v", rel, err)
		}
		mustWrite(t, v, user, rel, "build output")
	}

	reverted, err := j.Revert()
	if err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if len(reverted) != 0 {
		t.Fatalf("reverted %v, want nothing outside the protected region", reverted)
	}
	if got := mustRead(t, v, user, "agents/draft_weather/AGENT.md"); got != "build output" {
		t.Errorf("AGENT.md = %q, want the build's output untouched", got)
	}
}

// TestJournalKeepsTheStateFromBeforeTheRehearsal pins first-write-wins.
//
// A rehearsal that writes the same note three times must have the state from
// before the FIRST write restored. Recording the latest prior state instead
// would leave the user's note holding the rehearsal's second-to-last draft —
// a silent corruption that looks like a successful revert.
func TestJournalKeepsTheStateFromBeforeTheRehearsal(t *testing.T) {
	v := New(t.TempDir())
	const user = "u1"
	mustWrite(t, v, user, "notes/log.md", "the user's real content")

	j := v.NewWriteJournal(user)
	for _, draft := range []string{"draft one", "draft two", "draft three"} {
		if err := j.Record(abs(v, user, "notes/log.md")); err != nil {
			t.Fatalf("Record: %v", err)
		}
		mustWrite(t, v, user, "notes/log.md", draft)
	}

	if _, err := j.Revert(); err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if got := mustRead(t, v, user, "notes/log.md"); got != "the user's real content" {
		t.Errorf("log.md = %q, want the pre-rehearsal content", got)
	}
}

// TestJournalPrunesOnlyDirectoriesTheRehearsalCreated covers the asymmetry that
// makes pruning safe: a folder the rehearsal invented goes, a folder the user
// already had stays even when the revert leaves it empty.
func TestJournalPrunesOnlyDirectoriesTheRehearsalCreated(t *testing.T) {
	v := New(t.TempDir())
	const user = "u1"

	// A folder the user already has, which the rehearsal will empty out.
	mustWrite(t, v, user, "notes/existing/placeholder.md", "user's file")
	userDir := abs(v, user, "notes/existing")

	j := v.NewWriteJournal(user)

	// Rehearsal invents a nested folder.
	if err := j.Record(abs(v, user, "notes/invented/deep/report.md")); err != nil {
		t.Fatalf("Record: %v", err)
	}
	mustWrite(t, v, user, "notes/invented/deep/report.md", "rehearsal output")

	// Rehearsal also drops a file into the user's existing folder.
	if err := j.Record(abs(v, user, "notes/existing/extra.md")); err != nil {
		t.Fatalf("Record: %v", err)
	}
	mustWrite(t, v, user, "notes/existing/extra.md", "rehearsal output")

	if _, err := j.Revert(); err != nil {
		t.Fatalf("Revert: %v", err)
	}

	if _, err := os.Stat(abs(v, user, "notes/invented")); !os.IsNotExist(err) {
		t.Error("notes/invented survived; a directory the rehearsal created should be pruned")
	}
	if _, err := os.Stat(userDir); err != nil {
		t.Errorf("notes/existing was removed (%v); a directory the user already had must survive", err)
	}
	if got := mustRead(t, v, user, "notes/existing/placeholder.md"); got != "user's file" {
		t.Errorf("placeholder.md = %q, want the user's file intact", got)
	}
}

// TestJournalAroundExecCatchesSubprocessWrites covers the third write channel —
// bash / run_script — where the individual writes are invisible to this process
// and only a before/after diff can find them.
func TestJournalAroundExecCatchesSubprocessWrites(t *testing.T) {
	v := New(t.TempDir())
	const user = "u1"
	mustWrite(t, v, user, "notes/keep.md", "original")
	mustWrite(t, v, user, "memory/gone.md", "will be deleted")

	j := v.NewWriteJournal(user)

	err := j.AroundExec(func() error {
		// Stand in for a script: modify, create and delete, all opaquely.
		mustWrite(t, v, user, "notes/keep.md", "clobbered by a script")
		mustWrite(t, v, user, "notes/script-output.md", "script wrote this")
		return v.Delete(user, "memory/gone.md")
	})
	if err != nil {
		t.Fatalf("AroundExec: %v", err)
	}

	if _, err := j.Revert(); err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if got := mustRead(t, v, user, "notes/keep.md"); got != "original" {
		t.Errorf("keep.md = %q, want the original restored", got)
	}
	if got := mustRead(t, v, user, "memory/gone.md"); got != "will be deleted" {
		t.Errorf("gone.md = %q, want the script's delete undone", got)
	}
	if _, err := os.Stat(abs(v, user, "notes/script-output.md")); !os.IsNotExist(err) {
		t.Error("script-output.md survived the revert")
	}
}

// TestJournalAroundExecReturnsTheCallersError pins that journaling is
// transparent: a failing script must fail exactly as it would without a
// journal, or the caller's error handling changes depending on whether a
// rehearsal is being cleaned up.
func TestJournalAroundExecReturnsTheCallersError(t *testing.T) {
	v := New(t.TempDir())
	j := v.NewWriteJournal("u1")
	want := os.ErrPermission
	if got := j.AroundExec(func() error { return want }); got != want {
		t.Fatalf("AroundExec returned %v, want the caller's own error %v", got, want)
	}
}

// TestJournalNilSafe pins the property every other surface depends on: chat,
// real agent runs and the design conversation pass no journal at all, and must
// not need a nil check of their own.
func TestJournalNilSafe(t *testing.T) {
	var j *WriteJournal
	if err := j.Record("/anything"); err != nil {
		t.Fatalf("Record on nil journal: %v", err)
	}
	called := false
	if err := j.AroundExec(func() error { called = true; return nil }); err != nil {
		t.Fatalf("AroundExec on nil journal: %v", err)
	}
	if !called {
		t.Fatal("AroundExec on a nil journal must still run the call")
	}
	reverted, err := j.Revert()
	if err != nil || len(reverted) != 0 {
		t.Fatalf("Revert on nil journal = (%v, %v), want (nil, nil)", reverted, err)
	}
}

// TestJournalIsQuietWhenTheRehearsalBehaves pins that a rehearsal touching only
// its own directory produces an empty revert list — so the caller's log line
// distinguishes "cleaned up after itself" from "had to undo work".
func TestJournalIsQuietWhenTheRehearsalBehaves(t *testing.T) {
	v := New(t.TempDir())
	const user = "u1"
	mustWrite(t, v, user, "notes/journal.md", "untouched")

	j := v.NewWriteJournal(user)
	err := j.AroundExec(func() error {
		mustWrite(t, v, user, "agents/draft_x/tools/run.py", "print(1)")
		return nil
	})
	if err != nil {
		t.Fatalf("AroundExec: %v", err)
	}
	reverted, err := j.Revert()
	if err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if len(reverted) != 0 {
		t.Fatalf("reverted %v, want nothing", reverted)
	}
}
