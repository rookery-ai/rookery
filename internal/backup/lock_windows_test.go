//go:build windows

package backup

import (
	"errors"
	"os"
	"testing"
)

// The build before the LockFileEx fix held rookery.pid open for writing and
// took no byte-range lock at all. A server from that build can still be
// running after `rookery upgrade` replaces the binary, and a byte-range lock
// alone would not see it — so a second serve or a `backup restore` from the new
// binary would run against its live database. The open itself must refuse.
func TestLockRefusesAnOldBuildHolder(t *testing.T) {
	dir := t.TempDir()
	// What the old build's server held: an ordinary read/write handle.
	old, err := os.OpenFile(LockPath(dir), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()

	if _, err := AcquireLock(dir); !errors.Is(err, ErrServerRunning) {
		t.Fatalf("an old-build server holding the file open: got %v, want ErrServerRunning", err)
	}

	old.Close()
	lock, err := AcquireLock(dir)
	if err != nil {
		t.Fatalf("once the old holder is gone the lock must be free: %v", err)
	}
	lock.Release()
}
