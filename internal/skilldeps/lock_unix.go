//go:build !windows

package skilldeps

import (
	"os"
	"path/filepath"
	"syscall"
)

// lockEnv takes an exclusive lock on envDir for the duration of an install (or venv
// creation) and returns its release. flock is per open file description, so it orders
// goroutines in this process and separate `rookery deps` processes alike — up to three
// scheduled agents can install into one skill's environment at once.
func lockEnv(envDir string) (func(), error) {
	if err := os.MkdirAll(envDir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(envDir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	// The handle exists only to hold the flock: nothing is ever written through it,
	// so a Close error cannot lose data and is deliberately discarded. It stays
	// O_RDWR because flock on NFS is emulated with fcntl locks, where an exclusive
	// lock needs a descriptor open for writing.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
