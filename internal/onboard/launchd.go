package onboard

import (
	"encoding/xml"
	"fmt"
	"path/filepath"
	"strings"
)

// LaunchAgentLabel is what launchd calls the job, and what `launchctl bootout`
// and `launchctl kickstart` are given. By convention it is also the plist's
// filename stem — see LaunchAgentPath.
const LaunchAgentLabel = "cloud.rookery.server"

// LaunchAgentPath is where a per-user launch agent belongs.
//
// A user AGENT rather than a system DAEMON, and the reasoning is the same one
// that put Windows on a Task Scheduler logon task instead of a Service Control
// Manager service. A LaunchDaemon in /Library/LaunchDaemons starts at boot, but
// it needs administrator rights to install and runs as root or as some other
// principal — so it could not reach a data directory under the user's own
// profile, which is exactly the problem the Linux side avoids by using a
// systemd USER unit.
//
// The accepted cost, recorded rather than engineered around: an agent starts at
// LOGIN, not at boot. A headless Mac that reboots does not start Rookery until
// someone signs in — unlike Linux, where `loginctl enable-linger` makes a user
// unit survive logout and start at boot. There is no launchd equivalent of
// lingering for an agent.
func LaunchAgentPath(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents", LaunchAgentLabel+".plist")
}

// LaunchAgentLogPath is where the agent's output is sent. launchd has no
// journal, so unless a job names a file its stdout and stderr are discarded —
// see LaunchAgentPlistFor.
func LaunchAgentLogPath(dataDir string) string {
	return filepath.Join(dataDir, "logs", "rookery.log")
}

// LaunchAgentPlistFor renders the launchd agent that starts Rookery when the
// owner signs in.
//
// Generated rather than shipped on disk, for the same reason UnitFileFor is:
// the path to the binary is decided at install time, and a file carrying a
// fixed path would register a job that starts nothing.
//
// Four keys here are load-bearing, and for three of them the obvious spelling
// is actively wrong:
//
//   - KeepAlive is a dict with SuccessfulExit=false, NOT a bare <true/>. Bare
//     true restarts the server after a CLEAN exit as well, so stopping it
//     deliberately — or running `rookery uninstall` — brings it straight back,
//     and the only way to make it stay down is to unload the agent. The dict
//     form is the mirror of the systemd unit's Restart=on-failure, which is
//     what this platform's sibling already does.
//
//   - StandardOutPath and StandardErrorPath are mandatory. launchd has nothing
//     like the journal systemd writes to, so a job that does not name a file
//     has its output discarded entirely — the server would run with no log at
//     all. Their directory must also already exist: launchd cannot create it,
//     and a job whose redirect target is missing fails to spawn, reporting it
//     only to the system log. The caller creates it before writing this file.
//
//   - PATH is set explicitly. A launchd-started process inherits a minimal PATH
//     that contains neither /opt/homebrew/bin (Apple silicon) nor
//     /usr/local/bin (Intel), so a coder or host tool the operator's own
//     terminal finds is invisible to the server — the same trap
//     coder.coderSearchDirs exists to work around from the other direction.
//     Detection can search harder; anything that later shells out cannot.
//
//   - ProcessType is deliberately NOT set to Background. That value tells
//     launchd the job is not user-facing and may be throttled for CPU and I/O,
//     which is wrong for a process serving HTTP and firing scheduled agent
//     runs. Omitting it leaves the Standard policy, which applies no
//     throttling.
//
// RunAtLoad makes installing start the server now rather than only at the next
// login, matching `systemctl --user enable --now`.
func LaunchAgentPlistFor(binary, dataDir string) string {
	logPath := LaunchAgentLogPath(dataDir)

	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	sb.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	sb.WriteString(`<plist version="1.0">` + "\n<dict>\n")

	plistString(&sb, "Label", LaunchAgentLabel)

	sb.WriteString("  <key>ProgramArguments</key>\n  <array>\n")
	for _, arg := range []string{binary, "serve"} {
		fmt.Fprintf(&sb, "    <string>%s</string>\n", escapePlist(arg))
	}
	sb.WriteString("  </array>\n")

	sb.WriteString("  <key>RunAtLoad</key>\n  <true/>\n")

	// on-failure, not always. See the comment above.
	sb.WriteString("  <key>KeepAlive</key>\n  <dict>\n")
	sb.WriteString("    <key>SuccessfulExit</key>\n    <false/>\n")
	sb.WriteString("  </dict>\n")

	sb.WriteString("  <key>EnvironmentVariables</key>\n  <dict>\n")
	plistStringIndent(&sb, "    ", "ROOKERY_DATA_DIR", dataDir)
	plistStringIndent(&sb, "    ", "PATH", launchAgentPATH)
	sb.WriteString("  </dict>\n")

	plistString(&sb, "WorkingDirectory", dataDir)
	plistString(&sb, "StandardOutPath", logPath)
	plistString(&sb, "StandardErrorPath", logPath)

	sb.WriteString("</dict>\n</plist>\n")
	return sb.String()
}

// launchAgentPATH is the PATH a launchd agent is given.
//
// Both Homebrew prefixes are listed because which one is right depends on the
// architecture (/opt/homebrew on Apple silicon, /usr/local on Intel) and a
// non-existent directory on PATH is harmless. The system directories follow so
// a Homebrew build wins over an older system copy, which is the ordering a
// login shell would produce anyway.
const launchAgentPATH = "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

func plistString(sb *strings.Builder, key, value string) {
	plistStringIndent(sb, "  ", key, value)
}

func plistStringIndent(sb *strings.Builder, indent, key, value string) {
	fmt.Fprintf(sb, "%s<key>%s</key>\n%s<string>%s</string>\n",
		indent, escapePlist(key), indent, escapePlist(value))
}

// escapePlist XML-escapes a value.
//
// Not optional: a data directory or a binary path can legitimately contain an
// ampersand, and launchctl rejects a malformed plist with a message that names
// nothing useful — the same unhelpful failure shape that forced TaskXMLFor to
// be careful about its encoding.
func escapePlist(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
