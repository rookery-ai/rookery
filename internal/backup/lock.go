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
	if err := f.Truncate(0); err == nil {
		if _, err := f.Seek(0, 0); err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
		}
	}
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
