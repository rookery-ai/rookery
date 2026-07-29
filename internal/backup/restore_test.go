package backup

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// makeSnapshot produces a snapshot of a synthetic install and returns its bytes
// plus the system key it embeds.
func makeSnapshot(t *testing.T, passphrase string) ([]byte, []byte) {
	t.Helper()
	dataDir := t.TempDir()
	database, dbPath := newTestDB(t, dataDir)
	writeFile(t, filepath.Join(dataDir, "vaults", "ws1", "notes", "a.md"), "restored note")

	sysKey := make([]byte, 32)
	for i := range sysKey {
		sysKey[i] = byte(200 - i)
	}
	destDir := t.TempDir()
	name, err := Snapshot(context.Background(), Options{
		DB: database, DBPath: dbPath, DataDir: dataDir,
		SystemKey: sysKey, Passphrase: passphrase,
		Destination: NewLocalDestination(destDir),
	})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(destDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return raw, sysKey
}

func TestVerifyAcceptsGoodSnapshot(t *testing.T) {
	raw, _ := makeSnapshot(t, "pw")
	m, err := Verify(bytes.NewReader(raw), "pw", "011_pending_actions.up.sql")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if m.WorkspaceCount != 1 {
		t.Fatalf("workspace count = %d, want 1", m.WorkspaceCount)
	}
}

func TestVerifyRejectsWrongPassphrase(t *testing.T) {
	raw, _ := makeSnapshot(t, "pw")
	if _, err := Verify(bytes.NewReader(raw), "nope", "011_pending_actions.up.sql"); !errors.Is(err, ErrBadPassphrase) {
		t.Fatalf("got %v, want ErrBadPassphrase", err)
	}
}

func TestVerifyRefusesNewerSchema(t *testing.T) {
	raw, _ := makeSnapshot(t, "pw")
	if _, err := Verify(bytes.NewReader(raw), "pw", "002_coder_api.up.sql"); !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("got %v, want ErrSchemaTooNew", err)
	}
}

func TestStageAndApplyRestore(t *testing.T) {
	t.Setenv("SA_SYSTEM_KEY", "")
	raw, sysKey := makeSnapshot(t, "pw")

	// A destination install with its own, different data.
	target := t.TempDir()
	writeFile(t, filepath.Join(target, "simple-agents.db"), "OLD DATABASE")
	writeFile(t, filepath.Join(target, "vaults", "wsOld", "notes", "old.md"), "old note")
	oldKey := hex.EncodeToString(bytes.Repeat([]byte{0x11}, 32))
	writeFile(t, filepath.Join(target, "system.key"), oldKey)

	if _, err := StageRestore(bytes.NewReader(raw), target, "pw", "011_pending_actions.up.sql"); err != nil {
		t.Fatalf("StageRestore: %v", err)
	}
	if !HasPendingRestore(target) {
		t.Fatal("staging must leave a pending marker")
	}
	// Staging alone must not have touched anything live.
	if body, _ := os.ReadFile(filepath.Join(target, "simple-agents.db")); string(body) != "OLD DATABASE" {
		t.Fatal("staging must not modify the live database")
	}

	if err := ApplyPendingRestore(target); err != nil {
		t.Fatalf("ApplyPendingRestore: %v", err)
	}

	note, err := os.ReadFile(filepath.Join(target, "vaults", "ws1", "notes", "a.md"))
	if err != nil {
		t.Fatalf("restored note missing: %v", err)
	}
	if string(note) != "restored note" {
		t.Fatalf("got %q, want %q", note, "restored note")
	}
	if _, err := os.Stat(filepath.Join(target, "vaults", "wsOld")); !os.IsNotExist(err) {
		t.Fatal("the previous vault tree must be moved aside, not left in place")
	}

	gotKey, err := os.ReadFile(filepath.Join(target, "system.key"))
	if err != nil {
		t.Fatalf("system.key missing after restore: %v", err)
	}
	if string(gotKey) != hex.EncodeToString(sysKey) {
		t.Fatal("restore must install the snapshot's system key")
	}
	if HasPendingRestore(target) {
		t.Fatal("the marker must be cleared after apply")
	}

	// The rollback safety net: without the OLD key in the pre-restore copy, its
	// master passwords and connector tokens would be permanently undecryptable.
	pre := findPreRestoreDir(t, target)
	preKey, err := os.ReadFile(filepath.Join(pre, "system.key"))
	if err != nil {
		t.Fatalf("pre-restore copy must retain the old system key: %v", err)
	}
	if string(preKey) != oldKey {
		t.Fatalf("pre-restore key = %q, want the old key", preKey)
	}
	if _, err := os.Stat(filepath.Join(pre, "vaults", "wsOld", "notes", "old.md")); err != nil {
		t.Fatalf("pre-restore copy must retain the old vaults: %v", err)
	}
}

func TestStageRestoreRefusesConflictingEnvKey(t *testing.T) {
	raw, _ := makeSnapshot(t, "pw")
	other := hex.EncodeToString(bytes.Repeat([]byte{0x22}, 32))
	t.Setenv("SA_SYSTEM_KEY", other)

	target := t.TempDir()
	_, err := StageRestore(bytes.NewReader(raw), target, "pw", "011_pending_actions.up.sql")
	if !errors.Is(err, ErrSystemKeyConflict) {
		t.Fatalf("got %v, want ErrSystemKeyConflict — SA_SYSTEM_KEY outranks the restored key", err)
	}
	if HasPendingRestore(target) {
		t.Fatal("a refused stage must not leave a marker behind")
	}
}

func TestStageRestoreWrongPassphraseLeavesNothing(t *testing.T) {
	t.Setenv("SA_SYSTEM_KEY", "")
	raw, _ := makeSnapshot(t, "pw")
	target := t.TempDir()

	if _, err := StageRestore(bytes.NewReader(raw), target, "wrong", "011_pending_actions.up.sql"); !errors.Is(err, ErrBadPassphrase) {
		t.Fatalf("got %v, want ErrBadPassphrase", err)
	}
	if HasPendingRestore(target) {
		t.Fatal("a failed stage must not leave a marker")
	}
	if _, err := os.Stat(filepath.Join(target, stagingDirName)); !os.IsNotExist(err) {
		t.Fatal("a failed stage must not leave a staging dir")
	}
}

func TestCancelRestoreClearsMarkerAndStaging(t *testing.T) {
	t.Setenv("SA_SYSTEM_KEY", "")
	raw, _ := makeSnapshot(t, "pw")
	target := t.TempDir()

	if _, err := StageRestore(bytes.NewReader(raw), target, "pw", "011_pending_actions.up.sql"); err != nil {
		t.Fatal(err)
	}
	if err := CancelRestore(target); err != nil {
		t.Fatalf("CancelRestore: %v", err)
	}
	if HasPendingRestore(target) {
		t.Fatal("marker must be gone")
	}
	if _, err := os.Stat(filepath.Join(target, stagingDirName)); !os.IsNotExist(err) {
		t.Fatal("staging dir must be gone")
	}
	// And a later boot must be a no-op rather than applying a stale restore.
	if err := ApplyPendingRestore(target); err != nil {
		t.Fatalf("apply with no marker must be a no-op: %v", err)
	}
}

func TestApplyPendingRestoreNoMarkerIsNoop(t *testing.T) {
	if err := ApplyPendingRestore(t.TempDir()); err != nil {
		t.Fatalf("got %v, want nil", err)
	}
}

func TestLockRefusesSecondHolder(t *testing.T) {
	dir := t.TempDir()
	first, err := AcquireLock(dir)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if _, err := AcquireLock(dir); !errors.Is(err, ErrServerRunning) {
		t.Fatalf("got %v, want ErrServerRunning", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	second, err := AcquireLock(dir)
	if err != nil {
		t.Fatalf("lock must be reusable after release: %v", err)
	}
	second.Release()
}

func findPreRestoreDir(t *testing.T, dataDir string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dataDir, ".pre-restore-*"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected exactly one .pre-restore dir, got %v (%v)", matches, err)
	}
	return matches[0]
}
