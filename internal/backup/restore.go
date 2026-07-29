package backup

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrSystemKeyConflict means ROOKERY_SYSTEM_KEY is set to something other than the
// snapshot's key. The environment variable outranks the key file, so proceeding
// would install a key the process then ignores — and the restored data would
// fail to decrypt with no obvious cause.
var ErrSystemKeyConflict = errors.New("backup: ROOKERY_SYSTEM_KEY conflicts with the snapshot's system key")

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

	// Unblock the decrypt goroutine if readArchive bailed out early, so the
	// receive below cannot hang.
	pr.CloseWithError(archiveErr)

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

	if envKey := os.Getenv("ROOKERY_SYSTEM_KEY"); envKey != "" && !strings.EqualFold(envKey, m.SystemKey) {
		os.RemoveAll(staging)
		return nil, fmt.Errorf("%w: unset ROOKERY_SYSTEM_KEY, or set it to the snapshot's key, then retry",
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
		return fmt.Errorf("backup: marker present but staging dir is missing; run 'rookery backup cancel-restore'")
	}

	slog.Warn("applying pending restore",
		"staged_at", marker.StagedAt.Format(time.RFC3339),
		"snapshot_version", marker.AppVersion,
		"snapshot_schema", marker.SchemaVersion)

	// Only the most recent rollback copy is retained. Each restore otherwise
	// leaves a full copy of the database and every vault behind, so a handful
	// of restores would quietly fill the disk.
	if old, err := filepath.Glob(filepath.Join(dataDir, ".pre-restore-*")); err == nil {
		for _, dir := range old {
			if err := os.RemoveAll(dir); err != nil {
				slog.Warn("could not remove an old pre-restore copy", "dir", dir, "error", err)
			}
		}
	}

	preDir := filepath.Join(dataDir, ".pre-restore-"+time.Now().UTC().Format("20060102-150405"))
	if err := os.MkdirAll(preDir, 0o700); err != nil {
		return fmt.Errorf("backup: create pre-restore dir: %w", err)
	}

	// Move the current install aside. Staging lives inside dataDir so every
	// rename is within one filesystem: atomic, and never half-complete.
	for _, name := range []string{
		"rookery.db", "rookery.db-wal", "rookery.db-shm",
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

	if err := os.Rename(filepath.Join(staging, "db", "rookery.db"),
		filepath.Join(dataDir, "rookery.db")); err != nil {
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
