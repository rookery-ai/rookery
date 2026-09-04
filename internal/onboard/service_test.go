package onboard

import (
	"strings"
	"testing"
)

// The three shipped platforms can have autostart installed; anything else
// cannot and must say what to do instead.
//
// All three joined deliberately, and each earlier wording of this test — "only
// Linux", then "only Linux and Windows" — was accurate right up until it was
// not. Every one of the three deliberately avoids the SYSTEM-level mechanism in
// favour of a per-user one, for the same reason each time: the server's data
// lives under the user's own profile, so anything running as another principal
// could not reach it. Linux uses a systemd user unit, Windows a Task Scheduler
// logon task rather than a Service Control Manager service, and macOS a launchd
// user agent rather than a launch daemon.
//
// The remaining unmanaged platforms are the genuinely unbuilt ones.
func TestOnlyTheShippedPlatformsAreManaged(t *testing.T) {
	for _, goos := range []string{"linux", "windows", "darwin"} {
		svc := ServiceFor(goos)
		if !svc.Managed {
			t.Errorf("%s should be managed", goos)
		}
		if svc.Kind == "" {
			t.Errorf("%s does not name its mechanism", goos)
		}
		if svc.Foreground == "" {
			t.Errorf("%s does not say how to run the server by hand", goos)
		}
	}
	for _, goos := range []string{"freebsd", "openbsd"} {
		svc := ServiceFor(goos)
		if svc.Managed {
			t.Errorf("%s reports Managed, but no autostart integration is built for it", goos)
		}
		if strings.TrimSpace(svc.Note) == "" {
			t.Errorf("%s has no Note explaining what to do instead", goos)
		}
		if svc.Foreground == "" {
			t.Errorf("%s does not say how to run the server by hand", goos)
		}
	}
}

// A managed platform must not also carry a Note telling the operator autostart
// is unavailable. The two would contradict each other, and `upgrade` prints
// whichever it finds.
func TestAManagedPlatformDoesNotAlsoApologise(t *testing.T) {
	for _, goos := range []string{"linux", "windows", "darwin"} {
		if note := strings.TrimSpace(ServiceFor(goos).Note); note != "" {
			t.Errorf("%s is managed but still carries a Note: %q", goos, note)
		}
	}
}

// Every managed platform must name its OWN restart command.
//
// `rookery upgrade` prints this, and it used to hardcode systemctl behind a
// bare `if svc.Managed`. That was right only while Linux was the sole managed
// platform — the moment Windows became one, the same branch would have told a
// Windows operator to run systemctl, which is exactly the bug the comment at
// that call site records having already fixed once for macOS and Windows.
func TestEveryManagedPlatformNamesItsOwnRestartCommand(t *testing.T) {
	for _, goos := range []string{"linux", "windows", "darwin"} {
		svc := ServiceFor(goos)
		if strings.TrimSpace(svc.Restart) == "" {
			t.Errorf("%s is managed but names no restart command; upgrade has nothing to print", goos)
		}
	}
	if strings.Contains(ServiceFor("windows").Restart, "systemctl") {
		t.Error("Windows must not be told to run systemctl — there is none")
	}
	if strings.Contains(ServiceFor("darwin").Restart, "systemctl") {
		t.Error("macOS must not be told to run systemctl — there is none")
	}
	if !strings.Contains(ServiceFor("linux").Restart, "systemctl") {
		t.Error("linux restarts through systemctl")
	}
	// The label is how launchctl addresses the job; a restart command naming
	// anything else would silently act on nothing.
	if !strings.Contains(ServiceFor("darwin").Restart, LaunchAgentLabel) {
		t.Error("the macOS restart command does not name the agent's label, so it would address no job")
	}
}

// An unmanaged platform has nothing to restart, and offering a command would be
// worse than the honest fallback of "restart the one you are running".
func TestUnmanagedPlatformsNameNoRestartCommand(t *testing.T) {
	for _, goos := range []string{"freebsd", "openbsd"} {
		if r := strings.TrimSpace(ServiceFor(goos).Restart); r != "" {
			t.Errorf("%s is not managed but offers a restart command: %q", goos, r)
		}
	}
}

// The unit must point at the binary that is actually installed. The packaged
// unit hardcodes /usr/bin/rookery, so copying it for someone who installed via
// install.sh into ~/.local/bin would enable a service that starts nothing.
func TestUnitFileTargetsTheGivenBinaryAndDataDir(t *testing.T) {
	unit := UnitFileFor("/home/someone/.local/bin/rookery", "/home/someone/.rookery")

	if !strings.Contains(unit, "ExecStart=/home/someone/.local/bin/rookery serve") {
		t.Error("unit does not start the binary it was given")
	}
	if !strings.Contains(unit, "Environment=ROOKERY_DATA_DIR=/home/someone/.rookery") {
		t.Error("unit does not point at the given data directory")
	}
	// ProtectSystem=strict makes the whole filesystem read-only bar the listed
	// paths, so a data dir missing from ReadWritePaths yields a server that
	// starts and then fails on its first write.
	if !strings.Contains(unit, "ReadWritePaths=/home/someone/.rookery") {
		t.Error("data dir is not writable under ProtectSystem=strict")
	}
	if !strings.Contains(unit, "WantedBy=default.target") {
		t.Error("unit has no [Install] target, so `systemctl --user enable` does nothing")
	}
}

func TestSystemdUnitPathIsPerUser(t *testing.T) {
	got := SystemdUnitPath("/home/someone")
	want := "/home/someone/.config/systemd/user/rookery.service"
	if got != want {
		t.Fatalf("SystemdUnitPath = %q, want %q", got, want)
	}
}
