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
