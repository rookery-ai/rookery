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
