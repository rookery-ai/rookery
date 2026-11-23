package main

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/backup"
)

// ErrServerRunning's own text is written for `backup restore` ("stop it before
// restoring"). Seen from `serve` it pointed the wrong way, so serve names the
// other server instead.
func TestServeLockErrorNamesTheOtherServer(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(backup.LockPath(dir), []byte("4242\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := serveLockError(dir, backup.ErrServerRunning)
	if !errors.Is(err, backup.ErrServerRunning) {
		t.Fatalf("must still be ErrServerRunning: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"another Rookery server is already running", "4242", backup.LockPath(dir)} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q lacks %q", msg, want)
		}
	}
	if strings.Contains(msg, "restoring") {
		t.Errorf("message %q still talks about restoring", msg)
	}
}

func TestServeLockErrorWithoutAReadablePID(t *testing.T) {
	dir := t.TempDir()
	msg := serveLockError(dir, backup.ErrServerRunning).Error()
	// "(pid " rather than "pid": the lock file is itself named rookery.pid.
	if strings.Contains(msg, "(pid ") {
		t.Errorf("no pid is known, so none may be named: %q", msg)
	}
	if !strings.Contains(msg, "another Rookery server is already running") {
		t.Errorf("message %q does not name the cause", msg)
	}
}

func TestServeLockErrorPassesOtherErrorsThrough(t *testing.T) {
	other := errors.New("open lock file: access denied")
	if got := serveLockError(t.TempDir(), other); got != other {
		t.Fatalf("got %v, want the original error unchanged", got)
	}
}
