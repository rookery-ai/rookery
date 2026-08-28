package browser

import (
	"runtime"
	"strings"
	"testing"
)

// The onboarding step offers to install the browser on every platform, so the
// system-library hint has to be right about which ones actually need one.
// Chromium's shared libraries are a Linux packaging concern; macOS and Windows
// ship them, and printing a dnf command to a Mac user would be noise that
// teaches them to skim the rest.
func TestSystemDepsHintIsLinuxOnly(t *testing.T) {
	for _, mgr := range []string{"dnf", "apt", "zypper", "pacman", "brew", "winget", ""} {
		hint := SystemDepsHint(mgr)
		if runtime.GOOS != "linux" {
			if hint != "" {
				t.Errorf("%s: got a system-library hint on %s: %q", mgr, runtime.GOOS, hint)
			}
			continue
		}
		switch mgr {
		case "dnf", "apt", "zypper", "pacman":
			if hint == "" {
				t.Errorf("%s: no hint for a Linux package manager that needs one", mgr)
			}
		case "brew", "winget", "":
			// brew and winget are not Linux package managers, and an empty
			// manager means none was detected — the caller falls back to naming
			// the libraries instead.
			if hint != "" {
				t.Errorf("%s: unexpected hint %q", mgr, hint)
			}
		}
	}
}

// Every hint must name the libraries Chromium actually fails without. A hint
// that installs the wrong set is worse than none: it looks like the problem was
// addressed, and the next failure reads as unrelated.
func TestLinuxHintsNameTheLibrariesChromiumNeeds(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("hints are empty off Linux")
	}
	for _, mgr := range []string{"dnf", "apt", "zypper", "pacman"} {
		hint := strings.ToLower(SystemDepsHint(mgr))
		for _, lib := range []string{"nss", "atk", "cups", "drm", "randr", "gbm", "asound", "pango"} {
			// Package names differ per distribution (mesa-libgbm vs libgbm1), so
			// the assertion is on the library each one wraps.
			if !strings.Contains(hint, lib) && !(lib == "gbm" && strings.Contains(hint, "mesa")) &&
				!(lib == "asound" && strings.Contains(hint, "alsa")) {
				t.Errorf("%s hint omits %s: %q", mgr, lib, hint)
			}
		}
		if !strings.HasPrefix(hint, "sudo ") {
			t.Errorf("%s hint does not say it needs root: %q", mgr, hint)
		}
	}
}

// Probe must never create what it is probing for. It is called from /healthz and
// from onboarding, and a probe with side effects would report "installed" on a
// host where nothing had been installed.
func TestProbeReportsMissingWithoutCreatingAnything(t *testing.T) {
	dir := t.TempDir()
	av := probeAt(dir+"/driver", dir+"/browsers")
	if av.OK {
		t.Fatal("reported a runtime in an empty directory")
	}
	if !strings.Contains(av.Reason, "rookery browser install") {
		t.Errorf("the reason does not name the fix: %q", av.Reason)
	}
}
