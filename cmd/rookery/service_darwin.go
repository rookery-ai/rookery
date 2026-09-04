package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rookery-ai/rookery/internal/onboard"
)

// launchdDomain is the domain target launchctl addresses a user agent in.
//
// gui/<uid> rather than user/<uid>: the GUI domain is the one a LaunchAgent
// belongs to, and it is what exists once the owner has signed in. Getuid is the
// EFFECTIVE uid, which is what launchctl matches — someone who ran this under
// sudo would otherwise install an agent into root's domain and wonder why it
// never starts for them.
func launchdDomain() string {
	return "gui/" + strconv.Itoa(os.Getuid())
}

// launchdTarget is the service target for the agent: domain plus label.
func launchdTarget() string {
	return launchdDomain() + "/" + onboard.LaunchAgentLabel
}

// installAutostart writes the launch agent plist and loads it.
//
// Uses bootstrap/bootout rather than the deprecated `launchctl load -w` /
// `unload`. The bootout first is deliberate and its failure is ignored:
// bootstrap refuses a label already loaded, so reinstalling over an existing
// agent — which is what happens on an upgrade, or after editing the data
// directory — would otherwise fail with "service already loaded" and leave the
// OLD plist running while the new file sat on disk unused. That is the quiet
// half-success this whole command is written to avoid.
func installAutostart(env serviceEnv, out io.Writer) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}

	plistPath := onboard.LaunchAgentPath(home)
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return fmt.Errorf("create LaunchAgents directory: %w", err)
	}

	// launchd cannot create the directory holding StandardOutPath, and a job
	// whose redirect target is missing FAILS TO SPAWN — reporting it only to
	// the system log, so from here it would look like the agent installed fine
	// and the server simply never started.
	logPath := onboard.LaunchAgentLogPath(env.dataDir)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}

	// Generated against the binary actually running, not a fixed path: someone
	// who installed via install.sh has it in ~/.local/bin, and an agent naming a
	// binary that is not there starts nothing.
	plist := onboard.LaunchAgentPlistFor(env.binary, env.dataDir)
	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		return fmt.Errorf("write launch agent: %w", err)
	}
	fmt.Fprintf(out, "wrote %s\n", plistPath)

	_ = runArgs([]string{"launchctl", "bootout", launchdTarget()})

	if err := runArgs([]string{"launchctl", "bootstrap", launchdDomain(), plistPath}); err != nil {
		return fmt.Errorf("launchctl bootstrap: %w", err)
	}
	// Separate from bootstrap: an agent a user previously disabled stays
	// disabled across a bootstrap, so without this a reinstall would appear to
	// succeed and start nothing.
	_ = runArgs([]string{"launchctl", "enable", launchdTarget()})

	fmt.Fprintf(out, "logs go to %s\n", logPath)
	fmt.Fprintln(out, "starts at login — note that a Mac which reboots with nobody signed in")
	fmt.Fprintln(out, "  will not start Rookery until someone logs in.")
	return nil
}

func uninstallAutostart(out io.Writer) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	plistPath := onboard.LaunchAgentPath(home)

	// Unloading is attempted before the file is removed, and its failure is not
	// fatal: an agent that was never loaded must not stop the file being
	// cleaned up. Same policy as the systemd path's disable.
	_ = runArgs([]string{"launchctl", "bootout", launchdTarget()})

	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove launch agent: %w", err)
	}

	fmt.Fprintln(out, "Rookery will no longer start automatically.")
	fmt.Fprintln(out, "Your data directory is untouched.")
	return nil
}

func autostartStatus() (bool, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, "", fmt.Errorf("resolve home directory: %w", err)
	}
	plistPath := onboard.LaunchAgentPath(home)
	if _, err := os.Stat(plistPath); err != nil {
		return false, "", nil
	}

	// The file existing means "registered"; whether it is actually LOADED is a
	// separate question, and the two disagree exactly when something went wrong
	// — which is when this command is run. `launchctl print` fails outright for
	// a job that is not loaded, so its error is itself the useful answer.
	detail := plistPath
	printed, err := exec.Command("launchctl", "print", launchdTarget()).Output()
	if err != nil {
		detail += "\nnot currently loaded — load it with `rookery service install`"
		return true, detail, nil
	}
	detail += "\nloaded (" + launchdTarget() + ")"
	for _, line := range strings.Split(string(printed), "\n") {
		// One line out of several hundred: the rest is launchd's full job dump,
		// which is not something to print at someone who asked whether their
		// server is running.
		if s := strings.TrimSpace(line); strings.HasPrefix(s, "state = ") {
			detail += "\n" + s
			break
		}
	}
	return true, detail, nil
}
