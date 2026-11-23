//go:build windows

package backup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// ErrServerRunning means the exclusive install lock is held — almost always by
// a running server. Restoring under a live server is the failure class this
// design exists to avoid, so it is refused rather than negotiated.
var ErrServerRunning = errors.New("backup: the server is running; stop it before restoring")

// Lock is an exclusive lock over the whole install.
//
// It is a LockFileEx byte-range lock on a file held open for the holder's
// lifetime, because Windows releases it when the holder dies, however it dies.
// That is the property the Unix flock has, and the one the previous
// implementation lacked: it treated the mere EXISTENCE of the file as the lock
// (an O_EXCL create) and only Release removed it. `serve` has no shutdown
// path — a reboot, a closed console window, Ctrl+C or `schtasks /End` all kill
// it with no deferred code run — so the file survived every stop and every
// later start refused with ErrServerRunning, while the logon task's console
// closed before anyone could read why. Autostart therefore worked exactly once.
//
// A leftover file from that implementation is simply opened and locked here,
// which is what heals an install that is already wedged.
type Lock struct {
	f *os.File
}

// The locked range sits far past the pid text rather than over it: Windows
// enforces byte-range locks against READS from other processes too, so locking
// the content would make `Get-Content rookery.pid` fail during triage. A range
// beyond end-of-file is legal and locks nothing anyone reads.
const (
	lockOffsetHigh = 0x7fffffff
	lockLen        = 1
)

// openLockFile opens the lock file for read and write, sharing it with READERS
// only.
//
// The share mode is the second half of the lock, and it exists for one case:
// a server from the build before this one. That build held rookery.pid open
// for writing and took no byte-range lock, so after `rookery upgrade` replaces
// the binary while it is still running, LockFileEx alone would find nothing
// held, and a second serve or a `backup restore` would run against its live
// database. Refusing to share WRITE makes Windows fail this open with
// ERROR_SHARING_VIOLATION while any other process has the file open for
// writing. That covers the old build, and a second server of this one too.
// Readers that share write access (Go's os.Open, PowerShell's Get-Content) can
// still read the pid. A handle closes when its process dies, so this half
// cannot go stale either.
//
// The handle is not inheritable (nil security attributes), so no child process
// (a coder, Chromium) can keep it alive after the server dies.
func openLockFile(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ,
		nil,
		windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL,
		0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
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
	f, err := openLockFile(LockPath(dataDir))
	if err != nil {
		if errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			return nil, ErrServerRunning
		}
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	ol := &windows.Overlapped{OffsetHigh: lockOffsetHigh}
	err = windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, lockLen, 0, ol)
	if err != nil {
		f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
			return nil, ErrServerRunning
		}
		return nil, fmt.Errorf("lock install: %w", err)
	}
	// Record the pid for human triage; the OS lock, not this content, is the lock.
	if err := f.Truncate(0); err == nil {
		if _, err := f.Seek(0, 0); err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
		}
	}
	return &Lock{f: f}, nil
}

// Release drops the lock. The file stays, as it does on Unix: it is the OS
// lock that matters, and the next holder simply reopens the file.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	ol := &windows.Overlapped{OffsetHigh: lockOffsetHigh}
	err := windows.UnlockFileEx(windows.Handle(l.f.Fd()), 0, lockLen, 0, ol)
	closeErr := l.f.Close()
	l.f = nil
	if err != nil {
		return err
	}
	return closeErr
}
