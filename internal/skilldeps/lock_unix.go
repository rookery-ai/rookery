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
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
