package backup

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ReadLockPID returns the pid recorded in the install lock file.
//
// The pid is for a human doing triage and for error messages; it is never what
// decides whether the lock is held — the OS lock does that, so a pid recorded by
// a process long gone (and possibly reused since) cannot wedge anything.
func ReadLockPID(dataDir string) (int, error) {
	f, err := os.Open(LockPath(dataDir))
	if err != nil {
		return 0, err
	}
	defer f.Close()
	line, err := bufio.NewReader(f).ReadString('\n')
	if err != nil && line == "" {
		return 0, fmt.Errorf("read lock file: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		return 0, fmt.Errorf("lock file holds no pid: %w", err)
	}
	return pid, nil
}
