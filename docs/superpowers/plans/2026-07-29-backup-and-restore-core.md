# Backup and Restore (Core) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a working `simple-agents backup` CLI that snapshots the whole install (DB + every workspace vault) into one passphrase-encrypted file on local disk, and restores it — including onto a different machine.

**Architecture:** A new `internal/backup` package owns everything. A snapshot is `tar → gzip → chunked AES-256-GCM`, written to a `Destination`. The 32-byte system key travels inside the encrypted envelope, which is what makes cross-machine restore work; that key first gains a stable on-disk home so restore has somewhere to put it. Restore only ever mutates a dead install — enforced by an exclusive `flock` the server holds for its lifetime.

**Tech Stack:** Go, stdlib only (`archive/tar`, `compress/gzip`, `crypto/aes`, `crypto/cipher`, `crypto/sha256`, `syscall`), plus `golang.org/x/crypto/argon2` (already a dependency) and `github.com/urfave/cli/v3` (already a dependency). No new modules.

**Spec:** `docs/superpowers/specs/2026-07-29-backup-and-restore-design.md`

## Global Constraints

- **No new Go module dependencies.** gzip over zstd specifically because the project has no zstd dep.
- **Argon2id parameters are fixed and must match `internal/secrets`:** time=3, memory=64*1024 KiB, threads=4, keyLen=32.
- **Snapshot filename format:** `simple-agents-YYYYMMDD-HHMMSS.sab`, UTC. Timestamps must sort lexically.
- **Envelope magic:** the 8 bytes `SABACKUP`. Envelope version `1`. KDF id `1`.
- **Chunk size:** 1 MiB of plaintext per frame.
- **Tests never touch the operator's live install.** Every test uses `t.TempDir()`.
- **Test style:** stdlib `testing`, direct assertions with `t.Fatalf("got %q, want %q", got, want)`. No test framework, no table-driven requirement.
- **Conventional Commits** on every commit (`feat(backup): …`, `test(backup): …`, `refactor(config): …`).
- Run `go test ./internal/backup/... -count=1` after each task; `make ci` before the final commit.

---

### Task 1: Pin the system key to a file

Today `secrets.SystemKeyFromEnv()` derives the key from the hostname when `SA_SYSTEM_KEY` is unset. Renaming the host destroys every secret, and restore has nowhere to put a recovered key. This task fixes both, and ships value on its own.

**Files:**
- Modify: `internal/secrets/service.go` (add `SystemKey`, keep `SystemKeyFromEnv`)
- Modify: `cmd/simple-agents/main.go:111` (call site)
- Test: `internal/secrets/systemkey_file_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `secrets.SystemKey(dataDir string, hasWorkspaces bool) ([]byte, error)` and `secrets.SystemKeyPath(dataDir string) string`. Task 7 uses `SystemKeyPath` to move and write the key file.

- [ ] **Step 1: Write the failing tests**

Create `internal/secrets/systemkey_file_test.go`:

```go
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/secrets/... -run TestSystemKey -count=1`
Expected: FAIL — `undefined: SystemKey`, `undefined: SystemKeyPath`.

- [ ] **Step 3: Implement**

Append to `internal/secrets/service.go`:

```go
// SystemKeyPath is where the pinned 32-byte system key lives.
func SystemKeyPath(dataDir string) string {
	return filepath.Join(dataDir, "system.key")
}

// SystemKey resolves the system key, pinning it to disk so it survives a
// hostname change. Resolution order:
//
//  1. SA_SYSTEM_KEY, if set — still wins, and is never written to disk.
//  2. <dataDir>/system.key, if present.
//  3. Derive and persist. When hasWorkspaces is true the install already holds
//     data encrypted under the legacy hostname-derived key, so that exact key is
//     reproduced and written out — an upgrade must never change it. A fresh
//     install instead gets 32 random bytes, which is strictly stronger than a
//     guessable hostname.
//
// Restore writes the recovered key to this same path, which is how connector
// tokens and stored master passwords survive a move to new hardware.
func SystemKey(dataDir string, hasWorkspaces bool) ([]byte, error) {
	if hex64 := os.Getenv("SA_SYSTEM_KEY"); hex64 != "" {
		key, err := hex.DecodeString(hex64)
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("SA_SYSTEM_KEY must be 64 hex chars (32 bytes), got %d chars", len(hex64))
		}
		return key, nil
	}

	path := SystemKeyPath(dataDir)
	if raw, err := os.ReadFile(path); err == nil {
		key, err := hex.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("%s is corrupt: expected 64 hex chars (32 bytes)", path)
		}
		return key, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read system key: %w", err)
	}

	var key []byte
	if hasWorkspaces {
		host, _ := os.Hostname()
		key = argon2.IDKey([]byte(host), []byte("simple-agents-dev-key"), 1, 64*1024, 4, 32)
	} else {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("generate system key: %w", err)
		}
	}

	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(key)), 0o600); err != nil {
		return nil, fmt.Errorf("persist system key: %w", err)
	}
	return key, nil
}
```

Ensure the import block of `service.go` contains `"path/filepath"` and `"strings"` (it already has `crypto/rand`, `encoding/hex`, `fmt`, `os`, and `golang.org/x/crypto/argon2`).

Leave `SystemKeyFromEnv` in place — it is the documented legacy behaviour and the test above uses it to prove the migration is faithful.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/secrets/... -count=1`
Expected: PASS.

- [ ] **Step 5: Wire the call site**

In `cmd/simple-agents/main.go`, replace the `sysKey` block at line ~111. It must run **after** `db.Open` so the workspace count is available:

```go
			var wsCount int
			if err := database.QueryRow(`SELECT COUNT(*) FROM workspaces`).Scan(&wsCount); err != nil {
				return fmt.Errorf("count workspaces: %w", err)
			}
			sysKey, err := secrets.SystemKey(cfg.Data.Dir, wsCount > 0)
			if err != nil {
				return fmt.Errorf("system key: %w", err)
			}
```

- [ ] **Step 6: Verify the build and full suite**

Run: `go build ./... && go test ./... -count=1 -timeout 120s`
Expected: build succeeds, all tests pass.

- [ ] **Step 7: Commit**

```bash
git add internal/secrets/service.go internal/secrets/systemkey_file_test.go cmd/simple-agents/main.go
git commit -m "feat(secrets): pin the system key to <data_dir>/system.key

Deriving the key from the hostname meant renaming the host silently
destroyed every secret, and restore had nowhere to place a recovered key.
Existing installs keep their exact key and merely gain a file."
```

---

### Task 2: Encryption envelope

**Files:**
- Create: `internal/backup/crypto.go`
- Test: `internal/backup/crypto_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `backup.Encrypt(dst io.Writer, src io.Reader, passphrase string) error`, `backup.Decrypt(dst io.Writer, src io.Reader, passphrase string) error`, and the sentinels `backup.ErrBadPassphrase`, `backup.ErrCorrupt`. Tasks 6 and 7 use these.

- [ ] **Step 1: Write the failing tests**

Create `internal/backup/crypto_test.go`:

```go
package backup

import (
	"bytes"
	"crypto/rand"
	"errors"
	"testing"
)

func roundTrip(t *testing.T, plaintext []byte, pass string) []byte {
	t.Helper()
	var enc bytes.Buffer
	if err := Encrypt(&enc, bytes.NewReader(plaintext), pass); err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	return enc.Bytes()
}

func TestCryptoRoundTrip(t *testing.T) {
	// Larger than one 1 MiB frame, so framing itself is exercised.
	plaintext := make([]byte, 3*chunkSize+1234)
	if _, err := rand.Read(plaintext); err != nil {
		t.Fatal(err)
	}
	sealed := roundTrip(t, plaintext, "correct horse")

	if bytes.Contains(sealed, plaintext[:64]) {
		t.Fatal("ciphertext must not contain plaintext")
	}
	if string(sealed[:8]) != magic {
		t.Fatalf("magic = %q, want %q", sealed[:8], magic)
	}

	var out bytes.Buffer
	if err := Decrypt(&out, bytes.NewReader(sealed), "correct horse"); err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(out.Bytes(), plaintext) {
		t.Fatalf("round trip mismatch: got %d bytes, want %d", out.Len(), len(plaintext))
	}
}

func TestCryptoEmptyInput(t *testing.T) {
	sealed := roundTrip(t, nil, "pw")
	var out bytes.Buffer
	if err := Decrypt(&out, bytes.NewReader(sealed), "pw"); err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("got %d bytes, want 0", out.Len())
	}
}

func TestCryptoWrongPassphrase(t *testing.T) {
	sealed := roundTrip(t, []byte("hello"), "right")
	var out bytes.Buffer
	err := Decrypt(&out, bytes.NewReader(sealed), "wrong")
	if !errors.Is(err, ErrBadPassphrase) {
		t.Fatalf("got %v, want ErrBadPassphrase", err)
	}
}

func TestCryptoDetectsFlippedCiphertextByte(t *testing.T) {
	sealed := roundTrip(t, []byte("hello world"), "pw")
	sealed[len(sealed)-1] ^= 0xff
	var out bytes.Buffer
	if err := Decrypt(&out, bytes.NewReader(sealed), "pw"); err == nil {
		t.Fatal("expected failure on flipped ciphertext byte")
	}
}

// The header is authenticated as AAD, so tampering with the KDF parameters
// must be detected rather than silently honoured.
func TestCryptoDetectsFlippedHeaderByte(t *testing.T) {
	sealed := roundTrip(t, []byte("hello world"), "pw")
	sealed[10] ^= 0xff // inside the argon time/memory parameters
	var out bytes.Buffer
	if err := Decrypt(&out, bytes.NewReader(sealed), "pw"); err == nil {
		t.Fatal("expected failure on flipped header byte")
	}
}

// A snapshot cut short by a failed upload must not decrypt cleanly into a
// partial archive: the final-flag in the AAD makes truncation detectable.
func TestCryptoDetectsTruncation(t *testing.T) {
	plaintext := make([]byte, 2*chunkSize)
	rand.Read(plaintext)
	sealed := roundTrip(t, plaintext, "pw")

	cut := sealed[:headerLen+4+nonceLen+chunkSize/2]
	var out bytes.Buffer
	err := Decrypt(&out, bytes.NewReader(cut), "pw")
	if err == nil {
		t.Fatal("expected failure on truncated stream")
	}
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("got %v, want ErrCorrupt", err)
	}
}

func TestCryptoRejectsBadMagic(t *testing.T) {
	sealed := roundTrip(t, []byte("x"), "pw")
	copy(sealed[:8], "NOTABACK")
	var out bytes.Buffer
	if err := Decrypt(&out, bytes.NewReader(sealed), "pw"); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("got %v, want ErrCorrupt", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/backup/... -count=1`
Expected: FAIL — the package does not exist yet.

- [ ] **Step 3: Implement**

Create `internal/backup/crypto.go`:

```go
package backup

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
)

// The envelope is a small authenticated header followed by AES-256-GCM frames.
//
// Framing rather than one-shot GCM buys three properties a single seal cannot:
// bounded memory regardless of vault size, detection of reordered frames (the
// frame index is authenticated), and detection of truncation (the final flag is
// authenticated, so a stream cut short by a failed upload cannot decrypt
// cleanly into a partial archive).
const (
	magic           = "SABACKUP"
	envelopeVersion = 1
	kdfArgon2id     = 1

	// Argon2id parameters — identical to internal/secrets, deliberately.
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 4
	argonKeyLen  = 32

	chunkSize = 1 << 20 // 1 MiB of plaintext per frame
	saltLen   = 16
	nonceLen  = 12
	headerLen = 8 + 1 + 1 + 4 + 4 + 1 + saltLen
)

var (
	// ErrBadPassphrase means the first frame failed to authenticate, which in
	// practice always means a wrong passphrase.
	ErrBadPassphrase = errors.New("backup: wrong passphrase")
	// ErrCorrupt means the stream is not a valid snapshot: bad magic, an
	// unknown version, a truncated stream, or a tampered frame.
	ErrCorrupt = errors.New("backup: snapshot is corrupt or truncated")
)

// buildHeader renders the envelope header. It is written in the clear and
// authenticated as AAD, so parameter tampering is detectable.
func buildHeader(salt []byte) []byte {
	h := make([]byte, 0, headerLen)
	h = append(h, magic...)
	h = append(h, envelopeVersion, kdfArgon2id)
	h = binary.BigEndian.AppendUint32(h, argonTime)
	h = binary.BigEndian.AppendUint32(h, argonMemory)
	h = append(h, argonThreads)
	h = append(h, salt...)
	return h
}

// frameAAD binds each frame to the header, its position, and whether it ends
// the stream.
func frameAAD(header []byte, index uint64, final bool) []byte {
	aad := make([]byte, 0, len(header)+9)
	aad = append(aad, header...)
	aad = binary.BigEndian.AppendUint64(aad, index)
	if final {
		aad = append(aad, 1)
	} else {
		aad = append(aad, 0)
	}
	return aad
}

func newAEAD(passphrase string, salt []byte) (cipher.AEAD, error) {
	key := argon2.IDKey([]byte(passphrase), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Encrypt seals src into dst. dst receives the header followed by one frame per
// chunkSize of plaintext; a zero-length input still produces one (empty) final
// frame so that every stream has an authenticated terminator.
func Encrypt(dst io.Writer, src io.Reader, passphrase string) error {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("backup: generate salt: %w", err)
	}
	header := buildHeader(salt)
	if _, err := dst.Write(header); err != nil {
		return fmt.Errorf("backup: write header: %w", err)
	}

	aead, err := newAEAD(passphrase, salt)
	if err != nil {
		return err
	}

	buf := make([]byte, chunkSize)
	var index uint64
	for {
		n, readErr := io.ReadFull(src, buf)
		final := readErr == io.EOF || readErr == io.ErrUnexpectedEOF
		if readErr != nil && !final {
			return fmt.Errorf("backup: read plaintext: %w", readErr)
		}

		nonce := make([]byte, nonceLen)
		if _, err := rand.Read(nonce); err != nil {
			return fmt.Errorf("backup: generate nonce: %w", err)
		}
		sealed := aead.Seal(nil, nonce, buf[:n], frameAAD(header, index, final))

		if err := binary.Write(dst, binary.BigEndian, uint32(len(sealed))); err != nil {
			return fmt.Errorf("backup: write frame length: %w", err)
		}
		if _, err := dst.Write(nonce); err != nil {
			return fmt.Errorf("backup: write nonce: %w", err)
		}
		if _, err := dst.Write(sealed); err != nil {
			return fmt.Errorf("backup: write frame: %w", err)
		}

		if final {
			return nil
		}
		index++
	}
}

// Decrypt opens a stream produced by Encrypt and writes the plaintext to dst.
// It stops at the frame marked final; a stream that ends without one is
// truncated and reported as ErrCorrupt.
func Decrypt(dst io.Writer, src io.Reader, passphrase string) error {
	header := make([]byte, headerLen)
	if _, err := io.ReadFull(src, header); err != nil {
		return fmt.Errorf("%w: short header", ErrCorrupt)
	}
	if string(header[:8]) != magic {
		return fmt.Errorf("%w: not a simple-agents snapshot", ErrCorrupt)
	}
	if header[8] != envelopeVersion {
		return fmt.Errorf("%w: unsupported snapshot version %d", ErrCorrupt, header[8])
	}
	if header[9] != kdfArgon2id {
		return fmt.Errorf("%w: unsupported kdf %d", ErrCorrupt, header[9])
	}

	aead, err := newAEAD(passphrase, header[headerLen-saltLen:])
	if err != nil {
		return err
	}

	var index uint64
	for {
		var length uint32
		if err := binary.Read(src, binary.BigEndian, &length); err != nil {
			// Running out of frames without ever seeing the final flag is the
			// signature of a truncated upload.
			return fmt.Errorf("%w: stream ended without a final frame", ErrCorrupt)
		}
		if length < uint32(aead.Overhead()) || length > chunkSize+uint32(aead.Overhead()) {
			return fmt.Errorf("%w: implausible frame length %d", ErrCorrupt, length)
		}

		nonce := make([]byte, nonceLen)
		if _, err := io.ReadFull(src, nonce); err != nil {
			return fmt.Errorf("%w: short nonce", ErrCorrupt)
		}
		sealed := make([]byte, length)
		if _, err := io.ReadFull(src, sealed); err != nil {
			return fmt.Errorf("%w: short frame", ErrCorrupt)
		}

		// Try the non-final interpretation first, then the final one. Only the
		// AAD differs, so exactly one can authenticate.
		plain, err := aead.Open(nil, nonce, sealed, frameAAD(header, index, false))
		final := false
		if err != nil {
			plain, err = aead.Open(nil, nonce, sealed, frameAAD(header, index, true))
			final = true
		}
		if err != nil {
			if index == 0 {
				return ErrBadPassphrase
			}
			return fmt.Errorf("%w: frame %d failed authentication", ErrCorrupt, index)
		}

		if _, err := dst.Write(plain); err != nil {
			return fmt.Errorf("backup: write plaintext: %w", err)
		}
		if final {
			return nil
		}
		index++
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/backup/... -count=1 -v`
Expected: PASS, all eight crypto tests.

- [ ] **Step 5: Commit**

```bash
git add internal/backup/crypto.go internal/backup/crypto_test.go
git commit -m "feat(backup): chunked AES-256-GCM snapshot envelope

Framing rather than one-shot GCM bounds memory and makes reordering and
truncation detectable — a snapshot cut short by a failed upload must not
decrypt cleanly into a partial archive."
```

---

### Task 3: Manifest and compatibility gate

**Files:**
- Create: `internal/backup/manifest.go`
- Test: `internal/backup/manifest_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `backup.Manifest` (fields below), `backup.FileEntry`, `backup.ManifestName`, `backup.FormatVersion`, `(*Manifest).CheckCompatible(binarySchema string) error`, `backup.ErrSchemaTooNew`, `backup.ErrFormatTooNew`. Tasks 4, 6 and 7 use these.

- [ ] **Step 1: Write the failing tests**

Create `internal/backup/manifest_test.go`:

```go
package backup

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestManifestJSONRoundTrip(t *testing.T) {
	m := Manifest{
		FormatVersion:  FormatVersion,
		AppVersion:     "v0.3.1",
		AppCommit:      "abc1234",
		SchemaVersion:  "011_pending_actions.up.sql",
		SystemKey:      "00112233",
		WorkspaceCount: 7,
		TotalBytes:     123,
		Files:          []FileEntry{{Path: "db/simple-agents.db", Size: 123, SHA256: "deadbeef"}},
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Manifest
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.SchemaVersion != m.SchemaVersion || len(got.Files) != 1 || got.Files[0].SHA256 != "deadbeef" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestManifestAcceptsOlderSchema(t *testing.T) {
	m := Manifest{FormatVersion: FormatVersion, SchemaVersion: "003_agent_runs_usage.up.sql"}
	if err := m.CheckCompatible("011_pending_actions.up.sql"); err != nil {
		t.Fatalf("older schema must be accepted (migrations run forward): %v", err)
	}
}

func TestManifestAcceptsEqualSchema(t *testing.T) {
	m := Manifest{FormatVersion: FormatVersion, SchemaVersion: "011_pending_actions.up.sql"}
	if err := m.CheckCompatible("011_pending_actions.up.sql"); err != nil {
		t.Fatalf("equal schema must be accepted: %v", err)
	}
}

// The gate that matters: a half-applied restore destroys the data it was meant
// to protect, so a snapshot from a newer binary is refused outright.
func TestManifestRefusesNewerSchema(t *testing.T) {
	m := Manifest{FormatVersion: FormatVersion, SchemaVersion: "012_future.up.sql", AppVersion: "v0.9.0"}
	err := m.CheckCompatible("011_pending_actions.up.sql")
	if !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("got %v, want ErrSchemaTooNew", err)
	}
	if !contains(err.Error(), "v0.9.0") {
		t.Fatalf("error must name the version to upgrade to, got %q", err)
	}
}

func TestManifestRefusesNewerFormat(t *testing.T) {
	m := Manifest{FormatVersion: FormatVersion + 1, SchemaVersion: "001_initial_schema.up.sql"}
	if err := m.CheckCompatible("011_pending_actions.up.sql"); !errors.Is(err, ErrFormatTooNew) {
		t.Fatalf("got %v, want ErrFormatTooNew", err)
	}
}

func contains(hay, needle string) bool {
	return len(hay) >= len(needle) && (hay == needle || len(needle) == 0 ||
		func() bool {
			for i := 0; i+len(needle) <= len(hay); i++ {
				if hay[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		}())
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/backup/... -run TestManifest -count=1`
Expected: FAIL — `undefined: Manifest`.

- [ ] **Step 3: Implement**

Create `internal/backup/manifest.go`:

```go
package backup

import (
	"errors"
	"fmt"
	"time"
)

// ManifestName is the archive member holding the manifest. It is written first
// so a reader can validate compatibility before extracting anything.
const ManifestName = "manifest.json"

// FormatVersion is the snapshot layout version, bumped only on a breaking
// change to the archive shape.
const FormatVersion = 1

var (
	// ErrSchemaTooNew means the snapshot came from a newer build. Restoring it
	// would leave the database half-migrated, so it is refused.
	ErrSchemaTooNew = errors.New("backup: snapshot is newer than this build")
	// ErrFormatTooNew means the archive layout itself is from the future.
	ErrFormatTooNew = errors.New("backup: snapshot format is newer than this build")
)

// FileEntry records one archived file so restore can prove it arrived intact.
type FileEntry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Manifest describes a snapshot. SystemKey is the hex-encoded 32-byte system
// key: it rides inside the encrypted envelope so that a restore onto new
// hardware can still decrypt stored master passwords and connector tokens,
// which are otherwise lost without any visible error.
type Manifest struct {
	FormatVersion  int         `json:"format_version"`
	CreatedAt      time.Time   `json:"created_at"`
	AppVersion     string      `json:"app_version"`
	AppCommit      string      `json:"app_commit"`
	SchemaVersion  string      `json:"schema_version"`
	SystemKey      string      `json:"system_key"`
	WorkspaceCount int         `json:"workspace_count"`
	TotalBytes     int64       `json:"total_bytes"`
	Files          []FileEntry `json:"files"`
}

// CheckCompatible reports whether this build can safely restore the snapshot.
// binarySchema is the newest migration this build knows about.
//
// Migration names are compared lexically, which is exactly how they are applied
// (alphabetical order), so string comparison is the correct ordering here and
// not an approximation. An older snapshot is fine — migrations simply run
// forward after the swap.
func (m *Manifest) CheckCompatible(binarySchema string) error {
	if m.FormatVersion > FormatVersion {
		return fmt.Errorf("%w: snapshot format %d, this build understands %d",
			ErrFormatTooNew, m.FormatVersion, FormatVersion)
	}
	if m.SchemaVersion > binarySchema {
		return fmt.Errorf("%w: snapshot schema %q is ahead of this build's %q — upgrade to %s first",
			ErrSchemaTooNew, m.SchemaVersion, binarySchema, m.AppVersion)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/backup/... -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/backup/manifest.go internal/backup/manifest_test.go
git commit -m "feat(backup): snapshot manifest with schema compatibility gate"
```

---

### Task 4: Archive writer and reader

**Files:**
- Create: `internal/backup/archive.go`
- Test: `internal/backup/archive_test.go`

**Interfaces:**
- Consumes: `Manifest`, `FileEntry`, `ManifestName` (Task 3).
- Produces: `backup.writeArchive(dst io.Writer, files []archiveFile, m Manifest) error`, `backup.readArchive(src io.Reader, destDir string) (*Manifest, error)`, `backup.archiveFile{Name string; Path string}`, and `backup.collectVaultFiles(vaultsDir string) ([]archiveFile, error)`. Task 6 and Task 7 use these.

- [ ] **Step 1: Write the failing tests**

Create `internal/backup/archive_test.go`:

```go
package backup

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveRoundTrip(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "db.sqlite"), "DATABASE")
	writeFile(t, filepath.Join(src, "notes", "a.md"), "hello")

	files := []archiveFile{
		{Name: "db/simple-agents.db", Path: filepath.Join(src, "db.sqlite")},
		{Name: "vaults/ws1/notes/a.md", Path: filepath.Join(src, "notes", "a.md")},
	}

	var buf bytes.Buffer
	m := Manifest{FormatVersion: FormatVersion, SchemaVersion: "001_initial_schema.up.sql", SystemKey: "abcd"}
	if err := writeArchive(&buf, files, m); err != nil {
		t.Fatalf("writeArchive: %v", err)
	}

	out := t.TempDir()
	got, err := readArchive(bytes.NewReader(buf.Bytes()), out)
	if err != nil {
		t.Fatalf("readArchive: %v", err)
	}
	if got.SystemKey != "abcd" {
		t.Fatalf("system key = %q, want %q", got.SystemKey, "abcd")
	}
	if len(got.Files) != 2 {
		t.Fatalf("manifest lists %d files, want 2", len(got.Files))
	}
	if got.TotalBytes != int64(len("DATABASE")+len("hello")) {
		t.Fatalf("total bytes = %d, want %d", got.TotalBytes, len("DATABASE")+len("hello"))
	}

	body, err := os.ReadFile(filepath.Join(out, "vaults", "ws1", "notes", "a.md"))
	if err != nil {
		t.Fatalf("read extracted: %v", err)
	}
	if string(body) != "hello" {
		t.Fatalf("got %q, want %q", body, "hello")
	}
}

func TestArchiveDetectsChecksumMismatch(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.md"), "hello")
	var buf bytes.Buffer
	if err := writeArchive(&buf, []archiveFile{{Name: "vaults/ws1/a.md", Path: filepath.Join(src, "a.md")}}, Manifest{FormatVersion: FormatVersion}); err != nil {
		t.Fatal(err)
	}

	// Corrupt the gzip payload; extraction must not silently succeed.
	raw := buf.Bytes()
	raw[len(raw)-6] ^= 0xff
	if _, err := readArchive(bytes.NewReader(raw), t.TempDir()); err == nil {
		t.Fatal("expected an error for a corrupted archive")
	}
}

func TestArchiveRejectsPathTraversal(t *testing.T) {
	var buf bytes.Buffer
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "evil"), "x")
	if err := writeArchive(&buf, []archiveFile{{Name: "../../escape", Path: filepath.Join(src, "evil")}}, Manifest{FormatVersion: FormatVersion}); err != nil {
		t.Fatal(err)
	}
	if _, err := readArchive(bytes.NewReader(buf.Bytes()), t.TempDir()); err == nil {
		t.Fatal("expected extraction to refuse a path escaping the destination")
	}
}

// The regression this guards: vault.List and its siblings hide dotfiles, so
// reusing them would silently drop .kb/ (db-export sidecars, links.json) from
// every snapshot — invisible until a restore came back with no link index.
func TestCollectVaultFilesIncludesDotKB(t *testing.T) {
	vaults := t.TempDir()
	writeFile(t, filepath.Join(vaults, "ws1", "notes", "a.md"), "note")
	writeFile(t, filepath.Join(vaults, "ws1", ".kb", "links.json"), "{}")
	writeFile(t, filepath.Join(vaults, "ws1", ".kb", "db-export", "chats", "c1.json"), "{}")

	files, err := collectVaultFiles(vaults)
	if err != nil {
		t.Fatalf("collectVaultFiles: %v", err)
	}
	var names []string
	for _, f := range files {
		names = append(names, f.Name)
	}
	sort.Strings(names)

	want := []string{
		"vaults/ws1/.kb/db-export/chats/c1.json",
		"vaults/ws1/.kb/links.json",
		"vaults/ws1/notes/a.md",
	}
	if len(names) != len(want) {
		t.Fatalf("got %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("got %v, want %v", names, want)
		}
	}
}

func TestCollectVaultFilesSkipsStagingDirs(t *testing.T) {
	vaults := t.TempDir()
	writeFile(t, filepath.Join(vaults, "ws1", "notes", "a.md"), "note")
	writeFile(t, filepath.Join(vaults, ".restore-staging", "junk"), "x")

	files, err := collectVaultFiles(vaults)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Name == "vaults/.restore-staging/junk" {
			t.Fatal("staging dirs must never be archived")
		}
	}
}

func TestCollectVaultFilesMissingDirIsEmpty(t *testing.T) {
	files, err := collectVaultFiles(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("a missing vaults dir is an empty install, not an error: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("got %d files, want 0", len(files))
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/backup/... -run 'TestArchive|TestCollect' -count=1`
Expected: FAIL — `undefined: archiveFile`.

- [ ] **Step 3: Implement**

Create `internal/backup/archive.go`:

```go
package backup

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// archiveFile pairs an on-disk source with the name it takes inside the archive.
type archiveFile struct {
	Name string // slash-separated path within the archive
	Path string // absolute path on disk
}

// skipDirNames are never archived. claude-homes is excluded because it is
// hundreds of megabytes of regenerable coder cache whose .credentials.json is
// re-copied from the operator's ~/.claude on every invocation.
var skipDirNames = map[string]bool{
	".restore-staging": true,
	"claude-homes":     true,
}

func isSkippedDir(name string) bool {
	return skipDirNames[name] || strings.HasPrefix(name, ".pre-restore-")
}

// collectVaultFiles walks every workspace vault with a raw WalkDir.
//
// It deliberately does NOT use vault.List or its siblings: those hide dotfiles
// by design, which would silently omit .kb/ (the db-export sidecars and
// links.json) from every snapshot. The archive wants the literal tree; the KB
// browser's helpers exist to hide things from humans.
func collectVaultFiles(vaultsDir string) ([]archiveFile, error) {
	if _, err := os.Stat(vaultsDir); os.IsNotExist(err) {
		return nil, nil // an install with no workspaces yet
	}
	var out []archiveFile
	err := filepath.WalkDir(vaultsDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if isSkippedDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil // skip symlinks, sockets, devices
		}
		rel, err := filepath.Rel(vaultsDir, p)
		if err != nil {
			return err
		}
		out = append(out, archiveFile{
			Name: path.Join("vaults", filepath.ToSlash(rel)),
			Path: p,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk vaults: %w", err)
	}
	return out, nil
}

// writeArchive streams files into dst as tar+gzip, prefixed by the manifest.
// Checksums and sizes are computed as each file is copied, then the manifest is
// written first — which requires a two-pass approach, so checksums are computed
// up front.
func writeArchive(dst io.Writer, files []archiveFile, m Manifest) error {
	m.FormatVersion = FormatVersion
	m.Files = make([]FileEntry, 0, len(files))
	m.TotalBytes = 0

	for _, f := range files {
		sum, size, err := hashFile(f.Path)
		if err != nil {
			return err
		}
		m.Files = append(m.Files, FileEntry{Path: f.Name, Size: size, SHA256: sum})
		m.TotalBytes += size
	}

	gz := gzip.NewWriter(dst)
	tw := tar.NewWriter(gz)

	manifestJSON, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	if err := tw.WriteHeader(&tar.Header{
		Name: ManifestName, Mode: 0o600, Size: int64(len(manifestJSON)),
		ModTime: m.CreatedAt, Typeflag: tar.TypeReg,
	}); err != nil {
		return fmt.Errorf("write manifest header: %w", err)
	}
	if _, err := tw.Write(manifestJSON); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	for i, f := range files {
		if err := copyIntoTar(tw, f, m.Files[i].Size, m.CreatedAt); err != nil {
			return err
		}
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("close tar: %w", err)
	}
	return gz.Close()
}

func copyIntoTar(tw *tar.Writer, f archiveFile, size int64, modTime time.Time) error {
	in, err := os.Open(f.Path)
	if err != nil {
		return fmt.Errorf("open %s: %w", f.Path, err)
	}
	defer in.Close()

	if err := tw.WriteHeader(&tar.Header{
		Name: f.Name, Mode: 0o600, Size: size, ModTime: modTime, Typeflag: tar.TypeReg,
	}); err != nil {
		return fmt.Errorf("write header %s: %w", f.Name, err)
	}
	// io.CopyN, not io.Copy: tar demands exactly the declared size, and a file
	// that grew between hashing and copying would otherwise corrupt the stream.
	if _, err := io.CopyN(tw, in, size); err != nil {
		return fmt.Errorf("copy %s: %w", f.Name, err)
	}
	return nil
}

func hashFile(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, fmt.Errorf("open %s: %w", p, err)
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, fmt.Errorf("hash %s: %w", p, err)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// readArchive extracts src into destDir and returns the manifest, verifying
// every file's SHA-256 against it. A mismatch aborts, naming the file.
func readArchive(src io.Reader, destDir string) (*Manifest, error) {
	gz, err := gzip.NewReader(src)
	if err != nil {
		return nil, fmt.Errorf("open gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	var m *Manifest
	want := map[string]FileEntry{}

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		if hdr.Name == ManifestName {
			raw, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("read manifest: %w", err)
			}
			m = &Manifest{}
			if err := json.Unmarshal(raw, m); err != nil {
				return nil, fmt.Errorf("parse manifest: %w", err)
			}
			for _, e := range m.Files {
				want[e.Path] = e
			}
			continue
		}
		if m == nil {
			return nil, fmt.Errorf("archive does not start with %s", ManifestName)
		}

		target, err := safeJoin(destDir, hdr.Name)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return nil, fmt.Errorf("create dir for %s: %w", hdr.Name, err)
		}
		sum, n, err := writeAndHash(target, tr)
		if err != nil {
			return nil, err
		}
		entry, ok := want[hdr.Name]
		if !ok {
			return nil, fmt.Errorf("archive contains %s which the manifest does not list", hdr.Name)
		}
		if entry.SHA256 != sum || entry.Size != n {
			return nil, fmt.Errorf("checksum mismatch for %s", hdr.Name)
		}
		delete(want, hdr.Name)
	}

	if m == nil {
		return nil, fmt.Errorf("archive has no %s", ManifestName)
	}
	if len(want) > 0 {
		for name := range want {
			return nil, fmt.Errorf("manifest lists %s but the archive does not contain it", name)
		}
	}
	return m, nil
}

func writeAndHash(target string, r io.Reader) (string, int64, error) {
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", 0, fmt.Errorf("create %s: %w", target, err)
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), r)
	if err != nil {
		return "", 0, fmt.Errorf("write %s: %w", target, err)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// safeJoin refuses any archive member whose name escapes destDir. Extracting an
// untrusted tar without this is the classic zip-slip vulnerability.
func safeJoin(destDir, name string) (string, error) {
	if path.IsAbs(name) || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("archive member %q is an absolute path", name)
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	target := filepath.Join(destDir, clean)
	rel, err := filepath.Rel(destDir, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("archive member %q escapes the destination", name)
	}
	return target, nil
}
```

Add `"time"` to the import block (used by `copyIntoTar`).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/backup/... -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/backup/archive.go internal/backup/archive_test.go
git commit -m "feat(backup): tar+gzip archive with checksum verification

The vault walker is a raw WalkDir on purpose: vault.List hides dotfiles, so
reusing it would silently drop .kb/ from every snapshot."
```

---

### Task 5: Destination interface and local filesystem destination

**Files:**
- Create: `internal/backup/destination.go`
- Create: `internal/backup/dest_local.go`
- Test: `internal/backup/dest_local_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `backup.Destination` interface, `backup.Entry`, `backup.SnapshotName(t time.Time) string`, `backup.IsSnapshotName(string) bool`, `backup.LocalDestination` with `backup.NewLocalDestination(dir string) *LocalDestination`. Tasks 6, 7 and 8 use these; the S3 destination in the follow-up plan implements the same interface.

- [ ] **Step 1: Write the failing tests**

Create `internal/backup/dest_local_test.go`:

```go
package backup

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSnapshotNameSortsLexically(t *testing.T) {
	earlier := SnapshotName(time.Date(2026, 7, 29, 3, 0, 0, 0, time.UTC))
	later := SnapshotName(time.Date(2026, 7, 30, 3, 0, 0, 0, time.UTC))
	if earlier != "simple-agents-20260729-030000.sab" {
		t.Fatalf("got %q", earlier)
	}
	if !(earlier < later) {
		t.Fatal("snapshot names must sort lexically by time — retention depends on it")
	}
}

func TestIsSnapshotName(t *testing.T) {
	if !IsSnapshotName("simple-agents-20260729-030000.sab") {
		t.Fatal("must accept a well-formed name")
	}
	for _, bad := range []string{
		"notes.txt", "simple-agents-2026-07-29.sab", "simple-agents-20260729-030000.sab.tmp",
		"other-20260729-030000.sab",
	} {
		if IsSnapshotName(bad) {
			t.Fatalf("must reject %q — retention deletes only what it matches", bad)
		}
	}
}

func TestLocalPutGetListDelete(t *testing.T) {
	dir := t.TempDir()
	d := NewLocalDestination(dir)
	ctx := context.Background()
	body := []byte("snapshot bytes")
	name := SnapshotName(time.Date(2026, 7, 29, 3, 0, 0, 0, time.UTC))

	if err := d.Put(ctx, name, bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("Put: %v", err)
	}

	rc, err := d.Get(ctx, name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(got, body) {
		t.Fatalf("got %q, want %q", got, body)
	}

	entries, err := d.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != name || entries[0].Size != int64(len(body)) {
		t.Fatalf("List = %+v", entries)
	}

	if err := d.Delete(ctx, name); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if entries, _ := d.List(ctx); len(entries) != 0 {
		t.Fatalf("expected empty after delete, got %+v", entries)
	}
}

func TestLocalListIgnoresForeignFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "important-tax-return.pdf"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "simple-agents-20260729-030000.sab.tmp"), []byte("x"), 0o644)

	entries, err := NewLocalDestination(dir).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a shared directory's other files must never be listed: %+v", entries)
	}
}

// A listing must never expose a half-written upload.
func TestLocalPutIsAtomic(t *testing.T) {
	dir := t.TempDir()
	d := NewLocalDestination(dir)
	name := SnapshotName(time.Now().UTC())
	if err := d.Put(context.Background(), name, strings.NewReader("body"), 4); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if len(matches) != 0 {
		t.Fatalf("temp files must not survive a successful Put: %v", matches)
	}
}

func TestLocalGetMissingIsNotFound(t *testing.T) {
	_, err := NewLocalDestination(t.TempDir()).Get(context.Background(), "simple-agents-20260729-030000.sab")
	if err == nil {
		t.Fatal("expected an error for a missing snapshot")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/backup/... -run 'TestSnapshotName|TestIsSnapshot|TestLocal' -count=1`
Expected: FAIL — `undefined: SnapshotName`.

- [ ] **Step 3: Implement the interface**

Create `internal/backup/destination.go`:

```go
package backup

import (
	"context"
	"io"
	"regexp"
	"time"
)

// Destination is where snapshots are stored. Implementations are expected to be
// safe for a single concurrent caller; the scheduler serialises runs.
//
// Put takes an explicit size because S3 requires Content-Length. The engine
// stages the encrypted archive to a temp file before uploading, so the size is
// always known and a failed upload is retryable without regenerating the
// snapshot.
type Destination interface {
	Name() string
	Put(ctx context.Context, name string, r io.Reader, size int64) error
	Get(ctx context.Context, name string) (io.ReadCloser, error)
	List(ctx context.Context) ([]Entry, error)
	Delete(ctx context.Context, name string) error
}

// Entry is one stored snapshot.
type Entry struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

// snapshotNameRe matches exactly the names this package creates. Retention and
// listing both filter on it so that a destination shared with other data never
// has a foreign file listed, downloaded, or deleted.
var snapshotNameRe = regexp.MustCompile(`^simple-agents-\d{8}-\d{6}\.sab$`)

// SnapshotName renders the canonical name for a snapshot taken at t. The layout
// sorts lexically by time, which is what retention relies on.
func SnapshotName(t time.Time) string {
	return "simple-agents-" + t.UTC().Format("20060102-150405") + ".sab"
}

// IsSnapshotName reports whether name is one of ours.
func IsSnapshotName(name string) bool {
	return snapshotNameRe.MatchString(name)
}
```

- [ ] **Step 4: Implement the local destination**

Create `internal/backup/dest_local.go`:

```go
package backup

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// LocalDestination stores snapshots in a directory on the host. It is the
// reference implementation of Destination and makes the whole engine testable
// with no network.
type LocalDestination struct {
	dir string
}

func NewLocalDestination(dir string) *LocalDestination {
	return &LocalDestination{dir: dir}
}

func (d *LocalDestination) Name() string { return "local:" + d.dir }

// Put writes to a temp file and renames, so a listing never shows a
// half-written snapshot.
func (d *LocalDestination) Put(ctx context.Context, name string, r io.Reader, size int64) error {
	if err := os.MkdirAll(d.dir, 0o700); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}
	tmp := filepath.Join(d.dir, name+".tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", tmp, err)
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, filepath.Join(d.dir, name)); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("finalize %s: %w", name, err)
	}
	return nil
}

func (d *LocalDestination) Get(ctx context.Context, name string) (io.ReadCloser, error) {
	f, err := os.Open(filepath.Join(d.dir, name))
	if err != nil {
		return nil, fmt.Errorf("open snapshot %s: %w", name, err)
	}
	return f, nil
}

func (d *LocalDestination) List(ctx context.Context) ([]Entry, error) {
	items, err := os.ReadDir(d.dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list backup dir: %w", err)
	}
	var out []Entry
	for _, it := range items {
		if it.IsDir() || !IsSnapshotName(it.Name()) {
			continue
		}
		info, err := it.Info()
		if err != nil {
			continue
		}
		out = append(out, Entry{Name: it.Name(), Size: info.Size(), ModTime: info.ModTime()})
	}
	return out, nil
}

func (d *LocalDestination) Delete(ctx context.Context, name string) error {
	if !IsSnapshotName(name) {
		return fmt.Errorf("refusing to delete %q: not a snapshot name", name)
	}
	if err := os.Remove(filepath.Join(d.dir, name)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete %s: %w", name, err)
	}
	return nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/backup/... -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/backup/destination.go internal/backup/dest_local.go internal/backup/dest_local_test.go
git commit -m "feat(backup): Destination interface and local filesystem backend

Listing and deletion filter on the snapshot name pattern so a destination
shared with other data never has a foreign file touched."
```

---

### Task 6: The Snapshot engine

**Files:**
- Create: `internal/backup/backup.go`
- Test: `internal/backup/backup_test.go`

**Interfaces:**
- Consumes: `Encrypt` (Task 2), `Manifest` (Task 3), `writeArchive`/`collectVaultFiles` (Task 4), `Destination`/`SnapshotName` (Task 5).
- Produces: `backup.Options`, `backup.Snapshot(ctx context.Context, opts Options) (string, error)` returning the snapshot name, and `backup.LatestSchemaVersion(database *sql.DB) (string, error)`. Task 8 (CLI) and the follow-up plan's scheduler and web API call `Snapshot`.

- [ ] **Step 1: Write the failing tests**

Create `internal/backup/backup_test.go`:

```go
package backup

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// newTestDB builds a throwaway SQLite file with just enough shape for the
// engine: a schema_migrations table and a workspaces table.
func newTestDB(t *testing.T, dir string) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(dir, "simple-agents.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{
		`CREATE TABLE schema_migrations (name TEXT PRIMARY KEY, applied_at TEXT)`,
		`INSERT INTO schema_migrations(name) VALUES ('001_initial_schema.up.sql')`,
		`INSERT INTO schema_migrations(name) VALUES ('011_pending_actions.up.sql')`,
		`CREATE TABLE workspaces (id TEXT PRIMARY KEY)`,
		`INSERT INTO workspaces(id) VALUES ('ws1')`,
	}
	for _, s := range stmts {
		if _, err := database.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	t.Cleanup(func() { database.Close() })
	return database, path
}

func TestLatestSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	database, _ := newTestDB(t, dir)
	got, err := LatestSchemaVersion(database)
	if err != nil {
		t.Fatalf("LatestSchemaVersion: %v", err)
	}
	if got != "011_pending_actions.up.sql" {
		t.Fatalf("got %q, want the highest applied migration", got)
	}
}

func TestSnapshotProducesDecryptableArchive(t *testing.T) {
	dataDir := t.TempDir()
	database, dbPath := newTestDB(t, dataDir)

	vaults := filepath.Join(dataDir, "vaults")
	writeFile(t, filepath.Join(vaults, "ws1", "notes", "a.md"), "my note")
	writeFile(t, filepath.Join(vaults, "ws1", ".kb", "links.json"), "{}")
	// Must be excluded — hundreds of MB of regenerable cache in a real install.
	writeFile(t, filepath.Join(dataDir, "claude-homes", "ws1", "creds"), "secret")

	destDir := t.TempDir()
	sysKey := make([]byte, 32)
	for i := range sysKey {
		sysKey[i] = byte(i)
	}

	name, err := Snapshot(context.Background(), Options{
		DB:          database,
		DBPath:      dbPath,
		DataDir:     dataDir,
		SystemKey:   sysKey,
		Passphrase:  "correct horse",
		Destination: NewLocalDestination(destDir),
	})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !IsSnapshotName(name) {
		t.Fatalf("returned name %q is not a snapshot name", name)
	}

	sealed, err := os.ReadFile(filepath.Join(destDir, name))
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	var plain bytes.Buffer
	if err := Decrypt(&plain, bytes.NewReader(sealed), "correct horse"); err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	out := t.TempDir()
	m, err := readArchive(bytes.NewReader(plain.Bytes()), out)
	if err != nil {
		t.Fatalf("readArchive: %v", err)
	}

	if m.SchemaVersion != "011_pending_actions.up.sql" {
		t.Fatalf("schema version = %q", m.SchemaVersion)
	}
	if m.WorkspaceCount != 1 {
		t.Fatalf("workspace count = %d, want 1", m.WorkspaceCount)
	}
	if len(m.SystemKey) != 64 {
		t.Fatalf("system key must be 64 hex chars, got %d", len(m.SystemKey))
	}
	if _, err := os.Stat(filepath.Join(out, "db", "simple-agents.db")); err != nil {
		t.Fatalf("database missing from snapshot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "vaults", "ws1", ".kb", "links.json")); err != nil {
		t.Fatalf(".kb must be archived: %v", err)
	}
	for _, e := range m.Files {
		if filepath.HasPrefix(e.Path, "claude-homes") {
			t.Fatalf("claude-homes must never be archived, found %s", e.Path)
		}
	}
}

// VACUUM INTO yields a consistent copy; copying the live file would be torn.
func TestSnapshotDatabaseIsQueryable(t *testing.T) {
	dataDir := t.TempDir()
	database, dbPath := newTestDB(t, dataDir)
	destDir := t.TempDir()

	name, err := Snapshot(context.Background(), Options{
		DB: database, DBPath: dbPath, DataDir: dataDir,
		SystemKey: make([]byte, 32), Passphrase: "pw",
		Destination: NewLocalDestination(destDir),
	})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	rc, _ := NewLocalDestination(destDir).Get(context.Background(), name)
	sealed, _ := io.ReadAll(rc)
	rc.Close()

	var plain bytes.Buffer
	if err := Decrypt(&plain, bytes.NewReader(sealed), "pw"); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if _, err := readArchive(bytes.NewReader(plain.Bytes()), out); err != nil {
		t.Fatal(err)
	}

	restored, err := sql.Open("sqlite", filepath.Join(out, "db", "simple-agents.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var n int
	if err := restored.QueryRow(`SELECT COUNT(*) FROM workspaces`).Scan(&n); err != nil {
		t.Fatalf("snapshot database is not queryable: %v", err)
	}
	if n != 1 {
		t.Fatalf("got %d workspaces, want 1", n)
	}
}

func TestSnapshotRequiresPassphrase(t *testing.T) {
	dataDir := t.TempDir()
	database, dbPath := newTestDB(t, dataDir)
	_, err := Snapshot(context.Background(), Options{
		DB: database, DBPath: dbPath, DataDir: dataDir,
		SystemKey: make([]byte, 32), Passphrase: "",
		Destination: NewLocalDestination(t.TempDir()),
	})
	if err == nil {
		t.Fatal("an empty passphrase must be refused, never written unencrypted")
	}
}

func TestSnapshotRequiresSystemKey(t *testing.T) {
	dataDir := t.TempDir()
	database, dbPath := newTestDB(t, dataDir)
	_, err := Snapshot(context.Background(), Options{
		DB: database, DBPath: dbPath, DataDir: dataDir,
		SystemKey: make([]byte, 16), Passphrase: "pw",
		Destination: NewLocalDestination(t.TempDir()),
	})
	if err == nil {
		t.Fatal("a short system key must be refused")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/backup/... -run TestSnapshot -count=1`
Expected: FAIL — `undefined: Snapshot`.

- [ ] **Step 3: Implement**

Create `internal/backup/backup.go`:

```go
// Package backup snapshots an entire Simple Agents install — the database and
// every workspace vault — into one passphrase-encrypted file, and restores it.
//
// The 32-byte system key travels INSIDE the encrypted envelope. That is the
// whole reason cross-machine restore works: the key encrypts every workspace's
// stored master password and every connector and chat-platform token, and it is
// derived from the hostname on installs that never set SA_SYSTEM_KEY. A restore
// without it produces an install that boots, looks healthy, and has silently
// lost every scheduled agent and every connector.
package backup

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/ilijad1/simple-agents-v2/internal/buildinfo"
)

// Options configures one Snapshot run.
type Options struct {
	DB          *sql.DB     // live database handle, used for VACUUM INTO and counts
	DBPath      string      // path of the live database file (for logging only)
	DataDir     string      // install data root; vaults/ live under it
	SystemKey   []byte      // 32 bytes, embedded in the manifest
	Passphrase  string      // envelope passphrase; must not be empty
	Destination Destination // where the finished snapshot is written
	Now         time.Time   // snapshot timestamp; zero means time.Now()
}

// LatestSchemaVersion returns the newest applied migration name. Migrations are
// applied in alphabetical order, so MAX(name) is the newest.
func LatestSchemaVersion(database *sql.DB) (string, error) {
	var name string
	err := database.QueryRow(`SELECT COALESCE(MAX(name), '') FROM schema_migrations`).Scan(&name)
	if err != nil {
		return "", fmt.Errorf("read schema version: %w", err)
	}
	return name, nil
}

// Snapshot writes one encrypted snapshot to the destination and returns its
// name.
//
// The pipeline is: VACUUM INTO a consistent database copy → tar+gzip it with
// the vault tree → encrypt → stage to a temp file → upload. Staging to a temp
// file rather than streaming straight to the destination gives a known
// Content-Length (S3 requires it) and makes a failed upload retryable without
// regenerating the snapshot.
func Snapshot(ctx context.Context, opts Options) (string, error) {
	if opts.Passphrase == "" {
		return "", fmt.Errorf("backup: refusing to write an unencrypted snapshot: no passphrase configured")
	}
	if len(opts.SystemKey) != 32 {
		return "", fmt.Errorf("backup: system key must be 32 bytes, got %d", len(opts.SystemKey))
	}
	if opts.Destination == nil {
		return "", fmt.Errorf("backup: no destination configured")
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	work, err := os.MkdirTemp(opts.DataDir, ".backup-work-")
	if err != nil {
		return "", fmt.Errorf("backup: create work dir: %w", err)
	}
	defer os.RemoveAll(work)

	// VACUUM INTO is a single statement producing a consistent, checkpointed
	// copy. Copying the .db file directly would be torn: a live install carries
	// a multi-megabyte WAL that a plain copy does not fold in.
	dbCopy := filepath.Join(work, "simple-agents.db")
	if _, err := opts.DB.ExecContext(ctx, `VACUUM INTO ?`, dbCopy); err != nil {
		return "", fmt.Errorf("backup: vacuum database: %w", err)
	}

	schema, err := LatestSchemaVersion(opts.DB)
	if err != nil {
		return "", err
	}
	var wsCount int
	if err := opts.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspaces`).Scan(&wsCount); err != nil {
		return "", fmt.Errorf("backup: count workspaces: %w", err)
	}

	vaultFiles, err := collectVaultFiles(filepath.Join(opts.DataDir, "vaults"))
	if err != nil {
		return "", err
	}
	files := append([]archiveFile{{Name: "db/simple-agents.db", Path: dbCopy}}, vaultFiles...)

	m := Manifest{
		CreatedAt:      now.UTC(),
		AppVersion:     buildinfo.Version,
		AppCommit:      buildinfo.Commit,
		SchemaVersion:  schema,
		SystemKey:      hex.EncodeToString(opts.SystemKey),
		WorkspaceCount: wsCount,
	}

	staged := filepath.Join(work, "snapshot.sab")
	if err := buildEncrypted(staged, files, m, opts.Passphrase); err != nil {
		return "", err
	}

	f, err := os.Open(staged)
	if err != nil {
		return "", fmt.Errorf("backup: open staged snapshot: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("backup: stat staged snapshot: %w", err)
	}

	name := SnapshotName(now)
	if err := opts.Destination.Put(ctx, name, f, info.Size()); err != nil {
		return "", fmt.Errorf("backup: upload snapshot: %w", err)
	}
	return name, nil
}

// buildEncrypted writes the archive through the encryption envelope into path.
// The two stages are joined with an io.Pipe so the plaintext archive is never
// held in memory or written to disk.
func buildEncrypted(path string, files []archiveFile, m Manifest, passphrase string) error {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("backup: create staged snapshot: %w", err)
	}
	defer out.Close()

	pr, pw := io.Pipe()
	archiveErr := make(chan error, 1)
	go func() {
		err := writeArchive(pw, files, m)
		// CloseWithError(nil) behaves as a plain Close, so this covers both cases.
		pw.CloseWithError(err)
		archiveErr <- err
	}()

	encErr := Encrypt(out, pr, passphrase)
	// Drain the writer goroutine before reporting, so an archive error is not
	// masked by the encrypt side simply seeing a closed pipe.
	if err := <-archiveErr; err != nil {
		return err
	}
	if encErr != nil {
		return encErr
	}
	return out.Sync()
}
```

Confirm the module path in the `buildinfo` import matches `go.mod` (run `head -1 go.mod`); adjust if it differs.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/backup/... -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/backup/backup.go internal/backup/backup_test.go
git commit -m "feat(backup): snapshot engine

VACUUM INTO for a consistent database copy, then tar+gzip+encrypt staged to
a temp file so the upload has a known length and is retryable."
```

---

### Task 7: Restore, the liveness lock, and pending-restore apply

**Files:**
- Create: `internal/backup/lock.go`
- Create: `internal/backup/restore.go`
- Test: `internal/backup/restore_test.go`

**Interfaces:**
- Consumes: `Decrypt` (Task 2), `Manifest`/`CheckCompatible` (Task 3), `readArchive` (Task 4), `Destination` (Task 5).
- Produces:
  - `backup.AcquireLock(dataDir string) (*Lock, error)` and `(*Lock).Release() error`
  - `backup.StageRestore(src io.Reader, dataDir, passphrase, binarySchema string) (*Manifest, error)`
  - `backup.ApplyPendingRestore(dataDir string) error`
  - `backup.CancelRestore(dataDir string) error`
  - `backup.HasPendingRestore(dataDir string) bool`
  - `backup.Verify(src io.Reader, passphrase, binarySchema string) (*Manifest, error)`
  - `backup.ErrServerRunning`, `backup.ErrSystemKeyConflict`

  Task 8 (CLI) and the follow-up plan's web API call all of these.

- [ ] **Step 1: Write the failing tests**

Create `internal/backup/restore_test.go`:

```go
package backup

import (
	"bytes"
	"context"
	"database/sql"
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

	_, err := StageRestore(bytes.NewReader(raw), t.TempDir(), "pw", "011_pending_actions.up.sql")
	if !errors.Is(err, ErrSystemKeyConflict) {
		t.Fatalf("got %v, want ErrSystemKeyConflict — SA_SYSTEM_KEY outranks the restored key", err)
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
	if _, err := os.Stat(filepath.Join(target, ".restore-staging")); !os.IsNotExist(err) {
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

var _ = sql.Open // keep the sqlite import referenced across build tags
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/backup/... -run 'TestVerify|TestStage|TestApply|TestCancel|TestLock' -count=1`
Expected: FAIL — `undefined: Verify`.

- [ ] **Step 3: Implement the lock**

Create `internal/backup/lock.go`:

```go
//go:build unix

package backup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// ErrServerRunning means the exclusive install lock is held — almost always by
// a running server. Restoring under a live server is the failure class this
// design exists to avoid, so it is refused rather than negotiated.
var ErrServerRunning = errors.New("backup: the server is running; stop it before restoring")

// Lock is an exclusive advisory lock over the whole install.
//
// A flock is used rather than a PID file because the kernel releases it
// automatically when the holder dies, so a crash can never leave a stale file
// that wedges recovery.
type Lock struct {
	f *os.File
}

// LockPath is the lock file for an install.
func LockPath(dataDir string) string {
	return filepath.Join(dataDir, "simple-agents.pid")
}

// AcquireLock takes the exclusive install lock without blocking.
func AcquireLock(dataDir string) (*Lock, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	f, err := os.OpenFile(LockPath(dataDir), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, ErrServerRunning
	}
	// Record the pid for human triage; the flock, not this content, is the lock.
	f.Truncate(0)
	f.Seek(0, 0)
	fmt.Fprintf(f, "%d\n", os.Getpid())
	return &Lock{f: f}, nil
}

// Release drops the lock.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	closeErr := l.f.Close()
	l.f = nil
	if err != nil {
		return err
	}
	return closeErr
}
```

Create `internal/backup/lock_windows.go` so the cross-compile matrix in `make ci` keeps passing:

```go
//go:build windows

package backup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrServerRunning means the exclusive install lock is held.
var ErrServerRunning = errors.New("backup: the server is running; stop it before restoring")

// Lock is an exclusive advisory lock over the whole install. On Windows the
// exclusive open itself provides the mutual exclusion: the OS refuses a second
// handle while the first is open with no sharing.
type Lock struct {
	f *os.File
}

// LockPath is the lock file for an install.
func LockPath(dataDir string) string {
	return filepath.Join(dataDir, "simple-agents.pid")
}

// AcquireLock takes the exclusive install lock without blocking.
func AcquireLock(dataDir string) (*Lock, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	f, err := os.OpenFile(LockPath(dataDir), os.O_CREATE|os.O_RDWR|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil, ErrServerRunning
		}
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	fmt.Fprintf(f, "%d\n", os.Getpid())
	return &Lock{f: f}, nil
}

// Release drops the lock and removes the file.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	name := l.f.Name()
	err := l.f.Close()
	l.f = nil
	if rmErr := os.Remove(name); err == nil {
		err = rmErr
	}
	return err
}
```

- [ ] **Step 4: Implement restore**

Create `internal/backup/restore.go`:

```go
package backup

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// ErrSystemKeyConflict means SA_SYSTEM_KEY is set to something other than the
// snapshot's key. The environment variable outranks the key file, so proceeding
// would install a key the process then ignores — and the restored data would
// fail to decrypt with no obvious cause.
var ErrSystemKeyConflict = errors.New("backup: SA_SYSTEM_KEY conflicts with the snapshot's system key")

const (
	stagingDirName = ".restore-staging"
	markerName     = ".restore-pending"
)

// pendingMarker records a staged restore awaiting the next startup.
type pendingMarker struct {
	StagedAt      time.Time `json:"staged_at"`
	SnapshotName  string    `json:"snapshot_name"`
	AppVersion    string    `json:"app_version"`
	SchemaVersion string    `json:"schema_version"`
}

func stagingDir(dataDir string) string { return filepath.Join(dataDir, stagingDirName) }
func markerPath(dataDir string) string { return filepath.Join(dataDir, markerName) }

// HasPendingRestore reports whether a staged restore is waiting to be applied.
func HasPendingRestore(dataDir string) bool {
	_, err := os.Stat(markerPath(dataDir))
	return err == nil
}

// Verify decrypts and fully validates a snapshot without touching the install.
// It is the cheap way to find out that a backup is intact before needing it.
func Verify(src io.Reader, passphrase, binarySchema string) (*Manifest, error) {
	scratch, err := os.MkdirTemp("", "sa-verify-")
	if err != nil {
		return nil, fmt.Errorf("backup: create scratch dir: %w", err)
	}
	defer os.RemoveAll(scratch)
	return decryptAndExtract(src, scratch, passphrase, binarySchema)
}

// decryptAndExtract decrypts src, extracts it into destDir, verifies every
// checksum, and applies the compatibility gate.
func decryptAndExtract(src io.Reader, destDir, passphrase, binarySchema string) (*Manifest, error) {
	pr, pw := io.Pipe()
	decErr := make(chan error, 1)
	go func() {
		err := Decrypt(pw, src, passphrase)
		pw.CloseWithError(err)
		decErr <- err
	}()

	m, archiveErr := readArchive(pr, destDir)
	// Drain the decrypt goroutine first: a wrong passphrase surfaces there, and
	// reporting the archive's "unexpected EOF" instead would be actively
	// misleading.
	if err := <-decErr; err != nil {
		return nil, err
	}
	if archiveErr != nil {
		return nil, archiveErr
	}
	if err := m.CheckCompatible(binarySchema); err != nil {
		return nil, err
	}
	return m, nil
}

// StageRestore decrypts and verifies a snapshot into <dataDir>/.restore-staging
// and records a pending marker. Nothing live is touched: a wrong passphrase, a
// corrupt archive or an incompatible schema all fail with the install intact.
//
// The swap itself happens in ApplyPendingRestore, at the top of the next
// startup, before the database is opened.
func StageRestore(src io.Reader, dataDir, passphrase, binarySchema string) (*Manifest, error) {
	staging := stagingDir(dataDir)
	if err := os.RemoveAll(staging); err != nil {
		return nil, fmt.Errorf("backup: clear staging dir: %w", err)
	}
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return nil, fmt.Errorf("backup: create staging dir: %w", err)
	}

	m, err := decryptAndExtract(src, staging, passphrase, binarySchema)
	if err != nil {
		os.RemoveAll(staging)
		return nil, err
	}

	if envKey := os.Getenv("SA_SYSTEM_KEY"); envKey != "" && !strings.EqualFold(envKey, m.SystemKey) {
		os.RemoveAll(staging)
		return nil, fmt.Errorf("%w: unset SA_SYSTEM_KEY, or set it to the snapshot's key, then retry",
			ErrSystemKeyConflict)
	}
	if _, err := hex.DecodeString(m.SystemKey); err != nil || len(m.SystemKey) != 64 {
		os.RemoveAll(staging)
		return nil, fmt.Errorf("backup: snapshot has no usable system key")
	}

	marker, err := json.MarshalIndent(pendingMarker{
		StagedAt:      time.Now().UTC(),
		AppVersion:    m.AppVersion,
		SchemaVersion: m.SchemaVersion,
	}, "", "  ")
	if err != nil {
		os.RemoveAll(staging)
		return nil, fmt.Errorf("backup: encode marker: %w", err)
	}
	if err := os.WriteFile(markerPath(dataDir), marker, 0o600); err != nil {
		os.RemoveAll(staging)
		return nil, fmt.Errorf("backup: write marker: %w", err)
	}
	return m, nil
}

// CancelRestore abandons a staged restore. Without it an abandoned restore lies
// in wait and fires whenever the server next starts — possibly weeks later,
// over data the owner has since changed.
func CancelRestore(dataDir string) error {
	if err := os.Remove(markerPath(dataDir)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("backup: remove marker: %w", err)
	}
	if err := os.RemoveAll(stagingDir(dataDir)); err != nil {
		return fmt.Errorf("backup: remove staging dir: %w", err)
	}
	return nil
}

// ApplyPendingRestore performs the swap if a restore is staged, and is a no-op
// otherwise. It MUST be called at the very top of serve — before the database
// is opened and before migrations run.
//
// The old database, vaults and system.key move together into
// .pre-restore-<ts>/. The key travels with them deliberately: its master
// passwords and connector tokens are encrypted under the OLD key, so leaving
// the key behind would make the rollback copy permanently undecryptable the
// instant the restore landed.
func ApplyPendingRestore(dataDir string) error {
	raw, err := os.ReadFile(markerPath(dataDir))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("backup: read marker: %w", err)
	}
	var marker pendingMarker
	_ = json.Unmarshal(raw, &marker) // a damaged marker still applies; it is only metadata

	staging := stagingDir(dataDir)
	if _, err := os.Stat(staging); err != nil {
		return fmt.Errorf("backup: marker present but staging dir is missing; run 'simple-agents backup cancel-restore'")
	}

	slog.Warn("applying pending restore",
		"staged_at", marker.StagedAt.Format(time.RFC3339),
		"snapshot_version", marker.AppVersion,
		"snapshot_schema", marker.SchemaVersion)

	preDir := filepath.Join(dataDir, ".pre-restore-"+time.Now().UTC().Format("20060102-150405"))
	if err := os.MkdirAll(preDir, 0o700); err != nil {
		return fmt.Errorf("backup: create pre-restore dir: %w", err)
	}

	// Move the current install aside. Staging lives inside dataDir so every
	// rename is within one filesystem: atomic, and never half-complete.
	for _, name := range []string{
		"simple-agents.db", "simple-agents.db-wal", "simple-agents.db-shm",
		"vaults", "system.key",
	} {
		src := filepath.Join(dataDir, name)
		if _, err := os.Stat(src); os.IsNotExist(err) {
			continue
		}
		if err := os.Rename(src, filepath.Join(preDir, name)); err != nil {
			return fmt.Errorf("backup: move %s aside: %w", name, err)
		}
	}

	if err := os.Rename(filepath.Join(staging, "db", "simple-agents.db"),
		filepath.Join(dataDir, "simple-agents.db")); err != nil {
		return fmt.Errorf("backup: install restored database: %w", err)
	}
	stagedVaults := filepath.Join(staging, "vaults")
	if _, err := os.Stat(stagedVaults); err == nil {
		if err := os.Rename(stagedVaults, filepath.Join(dataDir, "vaults")); err != nil {
			return fmt.Errorf("backup: install restored vaults: %w", err)
		}
	}

	// Only now, with the old key safely inside preDir, install the new one.
	if err := installSystemKey(staging, dataDir); err != nil {
		return err
	}

	if err := os.RemoveAll(staging); err != nil {
		return fmt.Errorf("backup: clear staging dir: %w", err)
	}
	if err := os.Remove(markerPath(dataDir)); err != nil {
		return fmt.Errorf("backup: clear marker: %w", err)
	}
	slog.Warn("restore applied", "previous_data", preDir)
	return nil
}

// installSystemKey writes the snapshot's system key from the staged manifest.
func installSystemKey(staging, dataDir string) error {
	raw, err := os.ReadFile(filepath.Join(staging, ManifestName))
	if err != nil {
		return fmt.Errorf("backup: read staged manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("backup: parse staged manifest: %w", err)
	}
	if len(m.SystemKey) != 64 {
		return fmt.Errorf("backup: staged manifest has no usable system key")
	}
	if err := os.WriteFile(filepath.Join(dataDir, "system.key"), []byte(m.SystemKey), 0o600); err != nil {
		return fmt.Errorf("backup: install system key: %w", err)
	}
	return nil
}
```

The import block for this file is exactly: `encoding/hex`, `encoding/json`, `errors`, `fmt`, `io`, `log/slog`, `os`, `path/filepath`, `strings`, `time`. (`strings` is used by the `SA_SYSTEM_KEY` comparison; there is no `bytes` import.)

**Note on `readArchive` and the manifest:** `decryptAndExtract` extracts `manifest.json` only into memory today. Update `readArchive` (Task 4) so it *also* writes the manifest to disk at `destDir/manifest.json` — `installSystemKey` reads it from the staging dir. Add this immediately after the `json.Unmarshal(raw, m)` call in `readArchive`:

```go
			if err := os.WriteFile(filepath.Join(destDir, ManifestName), raw, 0o600); err != nil {
				return nil, fmt.Errorf("write manifest: %w", err)
			}
```

and ensure `destDir` exists at the top of `readArchive`:

```go
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return nil, fmt.Errorf("create destination: %w", err)
	}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/backup/... -count=1`
Expected: PASS.

- [ ] **Step 6: Verify the Windows build still compiles**

Run: `GOOS=windows GOARCH=amd64 go build ./...`
Expected: success. (This is the cross-compile guard `make ci` enforces.)

- [ ] **Step 7: Commit**

```bash
git add internal/backup/lock.go internal/backup/lock_windows.go internal/backup/restore.go internal/backup/restore_test.go internal/backup/archive.go
git commit -m "feat(backup): restore staging, install lock and startup apply

The old system.key moves into .pre-restore-* with the data it decrypts —
leaving it behind would make the rollback copy undecryptable the instant a
restore landed."
```

---

### Task 8: CLI subcommands and serve wiring

**Files:**
- Modify: `cmd/simple-agents/main.go` (register `backupCommand()`, hold the lock in `serve`, call `ApplyPendingRestore`)
- Create: `cmd/simple-agents/backup_cmd.go`
- Delete from: `internal/config/config.go` (the `BackupConfig` struct and the `Backup` field)
- Test: `internal/config/config_test.go` (add a case asserting the yaml key is gone)

**Interfaces:**
- Consumes: everything from Tasks 1–7.
- Produces: `simple-agents backup {now,list,verify,restore,cancel-restore}`. The follow-up plan's web API reuses `backup.Snapshot`, `backup.StageRestore` and `backup.Verify` directly, not the CLI.

- [ ] **Step 1: Write the failing test for the config removal**

Add to `internal/config/config_test.go` (create the file if absent, `package config`):

```go
func TestBackupConfigIsGone(t *testing.T) {
	// The inert backup config was replaced by owner-level settings stored in
	// the database. A second, unread config surface next to the real one is
	// exactly what this project's no-fake-settings rule forbids.
	raw := []byte("backup:\n  enabled: true\n  target: git\n")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("an unknown key must be ignored, not fatal: %v", err)
	}
	if reflect.ValueOf(*cfg).FieldByName("Backup").IsValid() {
		t.Fatal("Config.Backup must no longer exist")
	}
}
```

Ensure the file imports `os`, `path/filepath`, `reflect`, `testing`.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/config/... -run TestBackupConfigIsGone -count=1`
Expected: FAIL — `Config.Backup must no longer exist`.

- [ ] **Step 3: Delete the inert config**

In `internal/config/config.go`, remove the `Backup BackupConfig \`yaml:"backup"\`` field from `Config` and delete the entire `BackupConfig` struct with its doc comment.

Run: `go build ./... && go test ./internal/config/... -count=1`
Expected: build succeeds (it was referenced nowhere else), test passes.

- [ ] **Step 4: Write the CLI**

Create `cmd/simple-agents/backup_cmd.go`:

```go
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/urfave/cli/v3"
	"golang.org/x/term"

	"github.com/ilijad1/simple-agents-v2/internal/backup"
	"github.com/ilijad1/simple-agents-v2/internal/config"
)

// readPassphrase reads the envelope passphrase from the terminal, or from
// stdin when --passphrase-stdin is given. It is never a flag: flags land in
// shell history and in ps output.
func readPassphrase(stdin bool) (string, error) {
	if stdin {
		var pw string
		if _, err := fmt.Scanln(&pw); err != nil {
			return "", fmt.Errorf("read passphrase from stdin: %w", err)
		}
		return pw, nil
	}
	fmt.Fprint(os.Stderr, "Passphrase: ")
	raw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read passphrase: %w", err)
	}
	return string(raw), nil
}

// localDestFor resolves the destination used by the CLI. The CLI targets a
// local directory only; remote destinations are configured in settings and
// used by the scheduler.
func localDestFor(cmd *cli.Command, cfg *config.Config) backup.Destination {
	dir := cmd.String("dir")
	if dir == "" {
		dir = filepath.Join(cfg.Data.Dir, "backups")
	}
	return backup.NewLocalDestination(dir)
}

func openDBReadOnly(cfg *config.Config) (*sql.DB, error) {
	database, err := sql.Open("sqlite", cfg.Database.Path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	return database, nil
}

func backupCommand() *cli.Command {
	dirFlag := &cli.StringFlag{Name: "dir", Usage: "Local backup directory (default <data_dir>/backups)"}
	stdinFlag := &cli.BoolFlag{Name: "passphrase-stdin", Usage: "Read the passphrase from stdin instead of the terminal"}

	return &cli.Command{
		Name:  "backup",
		Usage: "Snapshot and restore the whole install",
		Commands: []*cli.Command{
			{
				Name:  "now",
				Usage: "Write a snapshot immediately",
				Flags: []cli.Flag{dirFlag, stdinFlag},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cfg, err := config.Load(cmd.String("config"))
					if err != nil {
						return err
					}
					pw, err := readPassphrase(cmd.Bool("passphrase-stdin"))
					if err != nil {
						return err
					}
					database, err := openDBReadOnly(cfg)
					if err != nil {
						return err
					}
					defer database.Close()

					var wsCount int
					if err := database.QueryRow(`SELECT COUNT(*) FROM workspaces`).Scan(&wsCount); err != nil {
						return fmt.Errorf("count workspaces: %w", err)
					}
					sysKey, err := secretsSystemKey(cfg.Data.Dir, wsCount > 0)
					if err != nil {
						return err
					}

					name, err := backup.Snapshot(ctx, backup.Options{
						DB: database, DBPath: cfg.Database.Path, DataDir: cfg.Data.Dir,
						SystemKey: sysKey, Passphrase: pw,
						Destination: localDestFor(cmd, cfg),
					})
					if err != nil {
						return err
					}
					fmt.Printf("wrote %s\n", name)
					return nil
				},
			},
			{
				Name:  "list",
				Usage: "List stored snapshots",
				Flags: []cli.Flag{dirFlag},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cfg, err := config.Load(cmd.String("config"))
					if err != nil {
						return err
					}
					entries, err := localDestFor(cmd, cfg).List(ctx)
					if err != nil {
						return err
					}
					if len(entries) == 0 {
						fmt.Println("no snapshots")
						return nil
					}
					for _, e := range entries {
						fmt.Printf("%s  %10d bytes  %s\n", e.Name, e.Size, e.ModTime.Format("2006-01-02 15:04"))
					}
					return nil
				},
			},
			{
				Name:      "verify",
				Usage:     "Decrypt and checksum a snapshot without restoring it",
				ArgsUsage: "<file|snapshot-name>",
				Flags:     []cli.Flag{dirFlag, stdinFlag},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cfg, err := config.Load(cmd.String("config"))
					if err != nil {
						return err
					}
					rc, err := openSnapshot(ctx, cmd, cfg)
					if err != nil {
						return err
					}
					defer rc.Close()
					pw, err := readPassphrase(cmd.Bool("passphrase-stdin"))
					if err != nil {
						return err
					}
					schema, err := binarySchemaVersion(cfg)
					if err != nil {
						return err
					}
					m, err := backup.Verify(rc, pw, schema)
					if err != nil {
						return err
					}
					fmt.Printf("ok: %d files, %d workspaces, taken %s by %s\n",
						len(m.Files), m.WorkspaceCount,
						m.CreatedAt.Format("2006-01-02 15:04"), m.AppVersion)
					return nil
				},
			},
			{
				Name:      "restore",
				Usage:     "Restore a snapshot (the server must be stopped)",
				ArgsUsage: "<file|snapshot-name>",
				Flags:     []cli.Flag{dirFlag, stdinFlag},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cfg, err := config.Load(cmd.String("config"))
					if err != nil {
						return err
					}
					lock, err := backup.AcquireLock(cfg.Data.Dir)
					if err != nil {
						return err
					}
					defer lock.Release()

					rc, err := openSnapshot(ctx, cmd, cfg)
					if err != nil {
						return err
					}
					defer rc.Close()
					pw, err := readPassphrase(cmd.Bool("passphrase-stdin"))
					if err != nil {
						return err
					}
					schema, err := binarySchemaVersion(cfg)
					if err != nil {
						return err
					}
					if _, err := backup.StageRestore(rc, cfg.Data.Dir, pw, schema); err != nil {
						return err
					}
					if err := backup.ApplyPendingRestore(cfg.Data.Dir); err != nil {
						return err
					}
					fmt.Println("restore complete; the previous data is in .pre-restore-* under the data dir")
					return nil
				},
			},
			{
				Name:  "cancel-restore",
				Usage: "Abandon a staged restore so it does not apply on the next start",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cfg, err := config.Load(cmd.String("config"))
					if err != nil {
						return err
					}
					if !backup.HasPendingRestore(cfg.Data.Dir) {
						fmt.Println("no restore is pending")
						return nil
					}
					if err := backup.CancelRestore(cfg.Data.Dir); err != nil {
						return err
					}
					fmt.Println("pending restore cancelled")
					return nil
				},
			},
		},
	}
}

// openSnapshot resolves the argument as a filesystem path first, then as a
// snapshot name in the backup directory.
func openSnapshot(ctx context.Context, cmd *cli.Command, cfg *config.Config) (io.ReadCloser, error) {
	arg := cmd.Args().First()
	if arg == "" {
		return nil, errors.New("a snapshot file or name is required")
	}
	if f, err := os.Open(arg); err == nil {
		return f, nil
	}
	return localDestFor(cmd, cfg).Get(ctx, arg)
}

// binarySchemaVersion reports the newest migration this build ships, which is
// what the snapshot's schema version is compared against.
func binarySchemaVersion(cfg *config.Config) (string, error) {
	entries, err := os.ReadDir(resolveDir("migrations"))
	if err != nil {
		return "", fmt.Errorf("read migrations dir: %w", err)
	}
	newest := ""
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".up.sql") && name > newest {
			newest = name
		}
	}
	if newest == "" {
		return "", errors.New("no migrations found")
	}
	return newest, nil
}
```

Add the imports this file needs: `io`, `strings`, and `_ "modernc.org/sqlite"`. Add `secretsSystemKey` as a thin alias near the top of the file so the CLI does not import `secrets` twice under different names:

```go
var secretsSystemKey = secrets.SystemKey
```

with `"github.com/ilijad1/simple-agents-v2/internal/secrets"` imported. Confirm `golang.org/x/term` is already in `go.mod` (`grep term go.mod`); if it is absent, replace `readPassphrase`'s terminal branch with a plain `bufio.NewReader(os.Stdin).ReadString('\n')` and trim the newline, rather than adding a dependency.

- [ ] **Step 5: Register the command and wire serve**

In `cmd/simple-agents/main.go`, add `backupCommand(),` to the `Commands:` slice at line ~56.

Then, inside the `serve` action, **before** `db.Open` (which is at line ~103), insert:

```go
			if err := backup.ApplyPendingRestore(cfg.Data.Dir); err != nil {
				return fmt.Errorf("apply pending restore: %w", err)
			}
			installLock, err := backup.AcquireLock(cfg.Data.Dir)
			if err != nil {
				return err
			}
			defer installLock.Release()
```

Order matters and is load-bearing: the restore swap must complete before the database is opened or migrated, and the lock must be held for the server's whole lifetime so a concurrent `backup restore` refuses.

Add `"github.com/ilijad1/simple-agents-v2/internal/backup"` to the imports.

- [ ] **Step 6: Verify the build and the whole suite**

Run: `go build ./... && go test ./... -count=1 -timeout 120s`
Expected: build succeeds, all tests pass.

- [ ] **Step 7: Smoke-test the CLI end to end against a temp install**

```bash
export SA_DATA_DIR=$(mktemp -d)
go run ./cmd/simple-agents db migrate
go run ./cmd/simple-agents owner bootstrap -u tester -p 'test-pw-123'
echo 'backup-pass' | go run ./cmd/simple-agents backup now --passphrase-stdin
go run ./cmd/simple-agents backup list
echo 'backup-pass' | go run ./cmd/simple-agents backup verify \
  "$(ls "$SA_DATA_DIR"/backups)" --passphrase-stdin
```

Expected: `backup now` prints `wrote simple-agents-…​.sab`, `list` shows it, `verify` prints `ok: N files, …`.

Then prove the restore path and the lock:

```bash
echo 'backup-pass' | go run ./cmd/simple-agents backup restore \
  "$(ls "$SA_DATA_DIR"/backups)" --passphrase-stdin
ls -d "$SA_DATA_DIR"/.pre-restore-*   # the rollback copy exists
cat "$SA_DATA_DIR"/system.key          # the key was installed
```

Expected: `restore complete`, a `.pre-restore-*` directory containing `system.key`, and `system.key` present at the data root.

**Per the live-instance-safety rule, do all of this against `SA_DATA_DIR=$(mktemp -d)` — never the operator's `~/.simple-agents-v2`.**

- [ ] **Step 8: Run the full CI gate**

Run: `make ci`
Expected: fmt, vet, race tests, the six-target cross-compile, and the UI build all pass.

- [ ] **Step 9: Commit**

```bash
git add cmd/simple-agents/backup_cmd.go cmd/simple-agents/main.go internal/config/config.go internal/config/config_test.go
git commit -m "feat(cli): simple-agents backup now/list/verify/restore/cancel-restore

serve now applies a pending restore before opening the database and holds an
exclusive install lock for its lifetime, so a restore can never run against a
live install. Drops the inert config.BackupConfig it replaces."
```

---

## Self-Review

**Spec coverage.** Spec §"Corollary: pin the system key" → Task 1. §"Encryption envelope" → Task 2. §"manifest.json" + the compatibility gate → Task 3. §"Contents" incl. the raw-WalkDir/`.kb` requirement and the exclusion list → Task 4. §"Destinations" (interface + local) → Task 5. §"Snapshot format" pipeline and `VACUUM INTO` → Task 6. §"Restore" incl. the liveness interlock, `SA_SYSTEM_KEY` conflict, staging-inside-data_dir, the `system.key`-in-pre-restore fix, and cancellation → Task 7. §"Build order" items 1–2 and the `config.BackupConfig` deletion → Task 8.

Deferred to the follow-up plan by design (spec §"Build order" 3–5): owner config in `system_settings`, the schedule ticker, retention, S3 + SigV4, the eight API routes, and the settings UI. The upload door's `iolimit` exemption belongs to the web API and is therefore in that plan, not this one.

**Placeholder scan.** No TBDs; every step carries the actual code or the exact command and its expected output.

**Type consistency.** `Manifest`, `FileEntry`, `archiveFile`, `Entry`, `Destination`, `Options` and `Lock` are each defined once and used with matching field names throughout. `chunkSize`, `headerLen`, `nonceLen` and `magic` are referenced by the Task 2 tests and defined in the same task. `readArchive` is defined in Task 4 and amended once, explicitly, in Task 7 step 4.

**Known follow-ups for the implementer, not defects:**
- `readArchive` extracts to `destDir` and also writes `manifest.json` there — the amendment in Task 7 step 4 is required for `installSystemKey` to work; do not skip it.
- Confirm `golang.org/x/term` is already a dependency before using it (Task 8 step 4 gives the fallback).
