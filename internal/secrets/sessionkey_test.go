package secrets

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionKeyPersistsAcrossCalls(t *testing.T) {
	dir := t.TempDir()

	first, err := SessionKey(dir, "")
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if len(first) != 32 {
		t.Fatalf("want a 32-byte key, got %d", len(first))
	}

	second, err := SessionKey(dir, "")
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("the key changed between calls — every restart would sign users out")
	}
}

// The whole point of the change: two installs that configure nothing must not
// end up with the same key. The fallback this replaced was a compiled-in literal,
// so they always did.
func TestSessionKeyDiffersPerInstall(t *testing.T) {
	a, err := SessionKey(t.TempDir(), "")
	if err != nil {
		t.Fatalf("resolve a: %v", err)
	}
	b, err := SessionKey(t.TempDir(), "")
	if err != nil {
		t.Fatalf("resolve b: %v", err)
	}
	if string(a) == string(b) {
		t.Fatal("two fresh installs generated the same session key")
	}
}

func TestSessionKeyFileIsNotWorldReadable(t *testing.T) {
	dir := t.TempDir()
	if _, err := SessionKey(dir, ""); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	fi, err := os.Stat(SessionKeyPath(dir))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("session.key mode = %o, want 600", perm)
	}
}

func TestSessionKeyConfiguredValueWinsAndIsNotWritten(t *testing.T) {
	dir := t.TempDir()
	want := strings.Repeat("ab", 32) // 64 hex chars

	key, err := SessionKey(dir, want)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	decoded, _ := hex.DecodeString(want)
	if string(key) != string(decoded) {
		t.Fatal("a 64-hex-char configured key should decode to its 32 bytes")
	}
	if _, err := os.Stat(SessionKeyPath(dir)); !os.IsNotExist(err) {
		t.Fatal("a configured key must never be persisted to disk")
	}
}

// Historically the server did []byte(cfg.Server.SessionKey), so an operator who
// set a passphrase rather than hex has a working install. Rejecting that form
// would log them out for reading the code instead of the docs.
func TestSessionKeyAcceptsRawConfiguredValue(t *testing.T) {
	raw := "a passphrase someone actually set"
	key, err := SessionKey(t.TempDir(), raw)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if string(key) != raw {
		t.Fatalf("raw configured key = %q, want %q", key, raw)
	}
}

func TestSessionKeyRejectsCorruptFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "session.key"), []byte("not hex"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SessionKey(dir, ""); err == nil {
		t.Fatal("a corrupt session.key should be an error, not a silent new key")
	}
}
