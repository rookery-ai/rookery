// Package backup snapshots an entire Rookery install — the database and
// every workspace vault — into one passphrase-encrypted file, and restores it.
//
// The 32-byte system key travels INSIDE the encrypted envelope. That is the
// whole reason cross-machine restore works: the key encrypts every workspace's
// stored master password and every connector and chat-platform token, and it is
// derived from the hostname on installs that never set ROOKERY_SYSTEM_KEY. A restore
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

	"github.com/rookery-ai/rookery/internal/buildinfo"
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

	if err := os.MkdirAll(opts.DataDir, 0o700); err != nil {
		return "", fmt.Errorf("backup: create data dir: %w", err)
	}
	work, err := os.MkdirTemp(opts.DataDir, ".backup-work-")
	if err != nil {
		return "", fmt.Errorf("backup: create work dir: %w", err)
	}
	defer os.RemoveAll(work)

	// VACUUM INTO is a single statement producing a consistent, checkpointed
	// copy. Copying the .db file directly would be torn: a live install carries
	// a multi-megabyte WAL that a plain copy does not fold in.
	dbCopy := filepath.Join(work, "rookery.db")
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
	files := append([]archiveFile{{Name: "db/rookery.db", Path: dbCopy}}, vaultFiles...)

	m := Manifest{
		CreatedAt:      now.UTC(),
		AppVersion:     buildinfo.Version,
		AppCommit:      buildinfo.Commit,
		SchemaVersion:  schema,
		SystemKey:      hex.EncodeToString(opts.SystemKey),
		WorkspaceCount: wsCount,
	}

	staged := filepath.Join(work, "snapshot.rkb")
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

	name, err := freeSnapshotName(ctx, opts.Destination, now)
	if err != nil {
		return "", err
	}
	if err := opts.Destination.Put(ctx, name, f, info.Size()); err != nil {
		return "", fmt.Errorf("backup: upload snapshot: %w", err)
	}
	return name, nil
}

// freeSnapshotName picks a name not already taken at the destination.
//
// Snapshot names have one-second granularity, so two runs inside the same
// second — a double-clicked "Back up now", or a manual run racing the
// scheduler — would otherwise resolve to the same name and the second would
// silently overwrite the first. Advancing by whole seconds keeps names
// lexically sortable, which is what retention depends on.
func freeSnapshotName(ctx context.Context, d Destination, now time.Time) (string, error) {
	entries, err := d.List(ctx)
	if err != nil {
		// A destination that cannot be listed is not fatal here: fall back to
		// the plain name rather than refusing to take a backup at all.
		return SnapshotName(now), nil
	}
	taken := make(map[string]bool, len(entries))
	for _, e := range entries {
		taken[e.Name] = true
	}
	candidate := now
	for i := 0; i < 60; i++ {
		name := SnapshotName(candidate)
		if !taken[name] {
			return name, nil
		}
		candidate = candidate.Add(time.Second)
	}
	return "", fmt.Errorf("backup: no free snapshot name near %s", SnapshotName(now))
}

// buildEncrypted writes the archive through the encryption envelope into path.
// The two stages are joined with an io.Pipe so the plaintext archive is never
// held in memory or written to disk.
func buildEncrypted(path string, files []archiveFile, m Manifest, passphrase string) error {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("backup: create staged snapshot: %w", err)
	}
	// Closed explicitly on the success path below so a close failure is
	// reported rather than swallowed; this deferred close only covers the
	// error returns, where a second Close is a harmless no-op error.
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

	// Unblock the writer before receiving. If Encrypt returned early — a full
	// disk on the staged file is the realistic case — nothing is draining the
	// pipe, and writeArchive's next Write would block forever, so the receive
	// below would hang the whole snapshot path. Closing the read side makes
	// that Write fail instead.
	pr.CloseWithError(encErr)

	// Drain the writer goroutine before reporting, so an archive error is not
	// masked by the encrypt side simply seeing a closed pipe.
	if err := <-archiveErr; err != nil {
		return err
	}
	if encErr != nil {
		return encErr
	}
	if err := out.Sync(); err != nil {
		return fmt.Errorf("backup: flush staged snapshot: %w", err)
	}
	// Sync catches the local full-disk case, but close is where a
	// network-backed filesystem reports a deferred write error — and a
	// snapshot truncated at this point would still be uploaded, listed and
	// counted as a good backup until the day someone tried to restore it.
	if err := out.Close(); err != nil {
		return fmt.Errorf("backup: close staged snapshot: %w", err)
	}
	return nil
}
