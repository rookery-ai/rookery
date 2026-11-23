package main

import (
	"errors"
	"fmt"

	"github.com/rookery-ai/rookery/internal/backup"
)

// serveLockError rewrites a held install lock into a message that makes sense
// from `serve`.
//
// backup.ErrServerRunning is worded for `backup restore` — "stop it before
// restoring" — and that is what serve used to print when it could not start,
// which sent people looking at backups. Worse, on Windows the autostart task's
// console closes the moment serve exits, so this message is often read only
// when someone runs `rookery serve` by hand to find out why nothing answers;
// it has to name the cause and where to look on its own.
//
// errors.Is(…, backup.ErrServerRunning) stays true so callers and tests can
// still recognise the condition.
func serveLockError(dataDir string, err error) error {
	if !errors.Is(err, backup.ErrServerRunning) {
		return err
	}
	where := "lock file " + backup.LockPath(dataDir)
	if pid, perr := backup.ReadLockPID(dataDir); perr == nil {
		where = fmt.Sprintf("pid %d, %s", pid, where)
	}
	return &serverRunningError{msg: fmt.Sprintf(
		"another Rookery server is already running with this data directory (%s); stop it before starting a new one",
		where)}
}

// serverRunningError carries serve's wording while still matching
// backup.ErrServerRunning, whose own text it deliberately does not print.
type serverRunningError struct{ msg string }

func (e *serverRunningError) Error() string        { return e.msg }
func (e *serverRunningError) Is(target error) bool { return target == backup.ErrServerRunning }
