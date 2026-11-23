//go:build windows

package backup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrServerRunning means the exclusive install lock is held — almost always by
// a running server. Restoring under a live server is the failure class this
// design exists to avoid, so it is refused rather than negotiated.
var ErrServerRunning = errors.New("backup: the server is running; stop it before restoring")

// Lock is an exclusive advisory lock over the whole install. On Windows the
// exclusive create itself provides the mutual exclusion: the OS refuses a
// second O_EXCL create while the first holder has not removed the file.
type Lock struct {
	f *os.File
}

// LockPath is the lock file for an install.
func LockPath(dataDir string) string {
	return filepath.Join(dataDir, "rookery.pid")
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
