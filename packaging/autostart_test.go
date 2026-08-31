package packaging

import (
	"strings"
	"testing"
)

// Both installers must offer autostart.
//
// Windows had none at all: the installer finished, the laptop was restarted,
// and nothing came back — with no error, because nothing was ever registered.
// Agents simply stopped running.
func TestBothInstallersOfferAutostart(t *testing.T) {
	for _, name := range []string{"install.sh", "install.ps1"} {
		body := repoFile(t, name)
		if !strings.Contains(body, "rookery service install") {
			t.Errorf("%s never offers to set up autostart", name)
		}
	}
}

// The installers ASK; Go registers.
//
// This is the boundary the whole design rests on, and it is easy to erode: the
// obvious "improvement" when something misbehaves is to inline a schtasks call
// or write the unit file straight from the script. Both scripts would then
// carry a second copy of platform knowledge that CI cannot exercise — install.ps1
// is not even syntax-checked on the development host — and the copies would
// drift from internal/onboard, which is the one place with tests over the
// generated unit and the generated task XML.
//
// External dependencies are deliberately NOT covered by this rule. python3,
// ripgrep, Poppler and Tesseract are ordinary OS packages, and the installers
// install them directly through the host's package manager. Autostart is
// Rookery's own configuration, which is a different thing.
func TestTheInstallersDoNotWriteServiceDefinitionsThemselves(t *testing.T) {
	sh := repoFile(t, "install.sh")
	for _, marker := range []string{"[Unit]", "ExecStart=", "WantedBy="} {
		if strings.Contains(sh, marker) {
			t.Errorf("install.sh writes a systemd unit itself (%q); that belongs in internal/onboard", marker)
		}
	}

	ps1 := repoFile(t, "install.ps1")
	for _, marker := range []string{"<Task", "schtasks", "Register-ScheduledTask"} {
		if strings.Contains(ps1, marker) {
			t.Errorf("install.ps1 registers a scheduled task itself (%q); that belongs in internal/onboard", marker)
		}
	}
}

// A script piped from the network into a shell must not register anything on
// its own initiative, and must not hang when there is no terminal to ask on.
func TestAutostartIsOfferedAndSkippable(t *testing.T) {
	sh := repoFile(t, "install.sh")
	if !strings.Contains(sh, "ROOKERY_NO_SERVICE") {
		t.Error("install.sh offers no way to skip the autostart step")
	}
	if !strings.Contains(sh, "Set that up now?") {
		t.Error("install.sh does not ask before registering autostart")
	}

	ps1 := repoFile(t, "install.ps1")
	if !strings.Contains(ps1, "ROOKERY_NO_SERVICE") && !strings.Contains(ps1, "NoService") {
		t.Error("install.ps1 offers no way to skip the autostart step")
	}
	if !strings.Contains(ps1, "Set that up now?") {
		t.Error("install.ps1 does not ask before registering autostart")
	}
}
