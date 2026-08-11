package onboard

import (
	"strings"
	"testing"
)

// Only Linux can have a service installed today. Claiming otherwise would mean
// shipping a launchd plist or a Windows service wrapper that half works, and a
// half-working service is harder to diagnose than none at all.
func TestOnlyLinuxIsManaged(t *testing.T) {
	if !ServiceFor("linux").Managed {
		t.Error("linux should be managed — the systemd user unit already ships in the deb and rpm")
	}
	for _, goos := range []string{"darwin", "windows", "freebsd"} {
		svc := ServiceFor(goos)
		if svc.Managed {
			t.Errorf("%s reports Managed, but no service integration is built for it", goos)
		}
		if strings.TrimSpace(svc.Note) == "" {
			t.Errorf("%s has no Note explaining what to do instead", goos)
		}
		if svc.Foreground == "" {
			t.Errorf("%s does not say how to run the server by hand", goos)
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
