package secrets

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestSystemKeyEnvWins(t *testing.T) {
	dir := t.TempDir()
	want := make([]byte, 32)
	for i := range want {
		want[i] = byte(i)
	}
	t.Setenv("SA_SYSTEM_KEY", hex.EncodeToString(want))

	got, err := SystemKey(dir, false)
	if err != nil {
		t.Fatalf("SystemKey: %v", err)
	}
	if hex.EncodeToString(got) != hex.EncodeToString(want) {
		t.Fatalf("got %x, want %x", got, want)
	}
	if _, err := os.Stat(SystemKeyPath(dir)); !os.IsNotExist(err) {
		t.Fatal("env key must not be persisted to disk")
	}
}

func TestSystemKeyFreshInstallGeneratesRandom(t *testing.T) {
	t.Setenv("SA_SYSTEM_KEY", "")
	dirA, dirB := t.TempDir(), t.TempDir()

	a, err := SystemKey(dirA, false)
	if err != nil {
		t.Fatalf("SystemKey A: %v", err)
	}
	b, err := SystemKey(dirB, false)
	if err != nil {
		t.Fatalf("SystemKey B: %v", err)
	}
	if len(a) != 32 {
		t.Fatalf("key length = %d, want 32", len(a))
	}
	if hex.EncodeToString(a) == hex.EncodeToString(b) {
		t.Fatal("two fresh installs must not share a key")
	}
}

func TestSystemKeyPersistsAndReloads(t *testing.T) {
	t.Setenv("SA_SYSTEM_KEY", "")
	dir := t.TempDir()

	first, err := SystemKey(dir, false)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := SystemKey(dir, false)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if hex.EncodeToString(first) != hex.EncodeToString(second) {
		t.Fatal("key must be stable across calls once persisted")
	}

	info, err := os.Stat(SystemKeyPath(dir))
	if err != nil {
		t.Fatalf("stat key file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("key file mode = %o, want 600", perm)
	}
}

// The migration guarantee: an install that already holds encrypted data keeps
// the exact hostname-derived key it has always used, and merely gains a file.
func TestSystemKeyExistingInstallKeepsHostnameKey(t *testing.T) {
	t.Setenv("SA_SYSTEM_KEY", "")
	dir := t.TempDir()

	legacy, err := SystemKeyFromEnv()
	if err != nil {
		t.Fatalf("SystemKeyFromEnv: %v", err)
	}
	got, err := SystemKey(dir, true)
	if err != nil {
		t.Fatalf("SystemKey: %v", err)
	}
	if hex.EncodeToString(got) != hex.EncodeToString(legacy) {
		t.Fatalf("existing install key changed: got %x, want %x", got, legacy)
	}
}

func TestSystemKeyRejectsCorruptFile(t *testing.T) {
	t.Setenv("SA_SYSTEM_KEY", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "system.key"), []byte("not-hex"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SystemKey(dir, false); err == nil {
		t.Fatal("expected error for a corrupt key file")
	}
}
