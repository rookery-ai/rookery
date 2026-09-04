package onboard

import (
	"encoding/xml"
	"path/filepath"
	"strings"
	"testing"
)

// The generated plist is the tested artifact, exactly as the systemd unit and
// the Task Scheduler XML are. There is no macOS host in this project and none
// in CI, so this is the level at which the agent can be checked at all — the
// same standing the Windows half has, and it is recorded as a real gap rather
// than papered over.
func TestLaunchAgentPlistIsWellFormed(t *testing.T) {
	got := LaunchAgentPlistFor("/Users/owner/.local/bin/rookery", "/Users/owner/.rookery")

	var into any
	if err := xml.Unmarshal([]byte(got), &into); err != nil {
		t.Fatalf("generated plist does not parse: %v\n%s", err, got)
	}
	if !strings.Contains(got, `<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"`) {
		t.Error("plist is missing Apple's DOCTYPE; launchctl rejects the file")
	}
}

// The one that matters most in this file. Every item here is a default or an
// obvious spelling that produces a server which looks installed and behaves
// wrongly — the launchd equivalent of the four Task Scheduler defaults.
func TestLaunchAgentPlistAvoidsTheSpellingsThatMisbehave(t *testing.T) {
	got := LaunchAgentPlistFor("/usr/local/bin/rookery", "/Users/owner/.rookery")

	// KeepAlive must be conditional on failure. A bare <true/> restarts the
	// server after a CLEAN exit too, so a deliberate stop or `rookery
	// uninstall` brings it right back and it cannot be stopped without
	// unloading the agent.
	if !strings.Contains(got, "<key>KeepAlive</key>") {
		t.Fatal("no KeepAlive: the server would not restart after a crash")
	}
	if !strings.Contains(got, "<key>SuccessfulExit</key>\n    <false/>") {
		t.Error("KeepAlive is not conditional on SuccessfulExit=false — a clean exit would be restarted, " +
			"which makes the server impossible to stop deliberately")
	}
	if strings.Contains(got, "<key>KeepAlive</key>\n  <true/>") {
		t.Error("KeepAlive is a bare true; it must be the SuccessfulExit=false dict")
	}

	// launchd has no journal. Without these the server's output is discarded.
	for _, key := range []string{"StandardOutPath", "StandardErrorPath"} {
		if !strings.Contains(got, "<key>"+key+"</key>") {
			t.Errorf("no %s: launchd discards output a job does not redirect, so the server would have no log at all", key)
		}
	}

	// A launchd agent inherits a minimal PATH containing neither Homebrew
	// prefix, so a coder the operator's own terminal finds is invisible here.
	if !strings.Contains(got, "<key>PATH</key>") {
		t.Fatal("no PATH: a launchd agent's inherited PATH omits both Homebrew prefixes")
	}
	for _, dir := range []string{"/opt/homebrew/bin", "/usr/local/bin"} {
		if !strings.Contains(got, dir) {
			t.Errorf("PATH omits %s, so a coder installed there would be invisible to the server", dir)
		}
	}

	// Background tells launchd the job may be throttled for CPU and I/O, which
	// is wrong for a process serving HTTP and firing scheduled runs.
	if strings.Contains(got, "<string>Background</string>") {
		t.Error("ProcessType is Background: launchd may throttle CPU and I/O, " +
			"which is wrong for a server answering requests and running agents on a schedule")
	}

	// Installing should start it now, not only at the next login — matching
	// `systemctl --user enable --now`.
	if !strings.Contains(got, "<key>RunAtLoad</key>\n  <true/>") {
		t.Error("RunAtLoad is not set: installing would not start the server until the next login")
	}
}

// The plist must name the binary that is actually running, the same property
// UnitFileFor has: someone who installed via install.sh has it in ~/.local/bin,
// and an agent naming a binary that is not there starts nothing.
func TestLaunchAgentPlistCarriesTheGivenBinaryAndDataDir(t *testing.T) {
	got := LaunchAgentPlistFor("/Users/owner/.local/bin/rookery", "/Users/owner/Rookery Data")

	if !strings.Contains(got, "<string>/Users/owner/.local/bin/rookery</string>") {
		t.Error("plist does not name the binary it was given")
	}
	if !strings.Contains(got, "<string>serve</string>") {
		t.Error("plist does not pass the serve subcommand")
	}
	if !strings.Contains(got, "ROOKERY_DATA_DIR") || !strings.Contains(got, "Rookery Data") {
		t.Error("plist does not carry the data directory, so the server would use the default one")
	}
}

// A path can legitimately contain an ampersand, and launchctl rejects a
// malformed plist with a message naming nothing useful.
func TestLaunchAgentPlistEscapesItsValues(t *testing.T) {
	got := LaunchAgentPlistFor("/Users/a&b/rookery", "/Users/a&b/.rookery")

	if strings.Contains(got, "a&b") {
		t.Error("an ampersand reached the document unescaped; launchctl would reject the file")
	}
	if !strings.Contains(got, "a&amp;b") {
		t.Error("the ampersand was not escaped as &amp;")
	}
	var into any
	if err := xml.Unmarshal([]byte(got), &into); err != nil {
		t.Fatalf("plist with an ampersand in a path does not parse: %v", err)
	}
}

// The label is the address launchctl bootout and kickstart are given, and by
// convention the plist's filename stem. A mismatch means uninstall silently
// leaves a loaded job behind.
func TestLaunchAgentPathMatchesTheLabel(t *testing.T) {
	path := LaunchAgentPath("/Users/owner")

	if want := filepath.Join("/Users/owner", "Library", "LaunchAgents", LaunchAgentLabel+".plist"); path != want {
		t.Errorf("LaunchAgentPath = %q, want %q", path, want)
	}
	if !strings.Contains(LaunchAgentPlistFor("/x", "/y"), "<string>"+LaunchAgentLabel+"</string>") {
		t.Error("the plist's Label does not match LaunchAgentLabel, so bootout would address a job that is not there")
	}
}
