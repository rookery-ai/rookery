//go:build !windows && !darwin

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/rookery-ai/rookery/internal/onboard"
)

// installAutostart writes and enables the systemd user unit.
//
// The unit is per-USER by design: the server owns a data directory under $HOME,
// so a system unit would have to run as some other account and could not reach
// it. That is the same constraint that rules out a Service Control Manager
// service on Windows.
func installAutostart(env serviceEnv, out io.Writer) error {
	if runtime.GOOS != "linux" {
		svc := onboard.CurrentService()
		return fmt.Errorf("%s", svc.Note)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	unitPath := onboard.SystemdUnitPath(home)
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return fmt.Errorf("create unit directory: %w", err)
	}

	// Generated against the binary actually running, not copied from the
	// package: the packaged unit hardcodes /usr/bin/rookery, so an install.sh
	// user with the binary in ~/.local/bin would enable a unit that starts
	// nothing.
	unit := onboard.UnitFileFor(env.binary, env.dataDir)
	if packaged, ok := onboard.FindPackagedUnit(); ok && env.binary == "/usr/bin/rookery" {
		if b, err := os.ReadFile(packaged); err == nil {
			unit = string(b)
		}
	}
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return fmt.Errorf("write unit: %w", err)
	}
	fmt.Fprintf(out, "wrote %s\n", unitPath)

	for _, args := range [][]string{
		{"systemctl", "--user", "daemon-reload"},
		{"systemctl", "--user", "enable", "--now", "rookery"},
	} {
		if err := runArgs(args); err != nil {
			return fmt.Errorf("%s: %w", strings.Join(args, " "), err)
		}
	}

	// Without lingering a user unit stops when the last session closes, so a
	// headless box reboots and the scheduler never comes back. The failure is
	// invisible until an agent silently misses its schedule, which is why this
	// is reported rather than ignored — but it is not fatal, because the server
	// is running and will keep running until logout.
	if err := runArgs([]string{"loginctl", "enable-linger"}); err != nil {
		fmt.Fprintln(out, "could not enable lingering — the server will stop when you log out.")
		fmt.Fprintln(out, "  fix it with: loginctl enable-linger")
		return nil
	}
	fmt.Fprintln(out, "lingering enabled — survives logout and reboot")
	return nil
}

func uninstallAutostart(out io.Writer) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("%s", onboard.CurrentService().Note)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	unitPath := onboard.SystemdUnitPath(home)

	// Disabling is attempted before the file is removed, and its failure is not
	// fatal: a unit that was never enabled, or a systemd that is not running,
	// must not stop the file being cleaned up.
	_ = runArgs([]string{"systemctl", "--user", "disable", "--now", "rookery"})

	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove unit: %w", err)
	}
	_ = runArgs([]string{"systemctl", "--user", "daemon-reload"})

	fmt.Fprintln(out, "Rookery will no longer start automatically.")
	fmt.Fprintln(out, "Your data directory is untouched.")
	return nil
}

func autostartStatus() (bool, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, "", fmt.Errorf("resolve home directory: %w", err)
	}
	unitPath := onboard.SystemdUnitPath(home)
	if _, err := os.Stat(unitPath); err != nil {
		return false, "", nil
	}

	detail := unitPath
	if out, err := exec.Command("systemctl", "--user", "is-active", "rookery").Output(); err == nil {
		detail += "\n" + strings.TrimSpace(string(out))
	}
	return true, detail, nil
}
