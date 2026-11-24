package onboard

import (
	"strings"
	"testing"
)

// Tesseract is the one host tool whose winget package is a machine-wide
// installer: UB-Mannheim.TesseractOCR is an NSIS installer that writes to
// C:\Program Files. ripgrep and Poppler are portable zips unpacked into the
// user's profile, and python.org's Python installs per-user — none of the
// three needs elevation. So on a standard install Tesseract alone raises a
// Windows permission prompt, and was the one tool that failed when the prompt
// was missed or declined, with nothing saying why.
func TestOnlyTesseractNeedsAdminOnWindows(t *testing.T) {
	for _, tool := range HostTools {
		want := tool.Bin == "tesseract"
		if tool.WindowsNeedsAdmin != want {
			t.Errorf("%s: WindowsNeedsAdmin = %v, want %v", tool.Bin, tool.WindowsNeedsAdmin, want)
		}
	}
}

func TestWingetStepsFlagTheElevatedInstallOnly(t *testing.T) {
	steps := InstallSteps(ManagerWinget, HostTools)
	if len(steps) != len(HostTools) {
		t.Fatalf("winget should have one step per tool, got %d", len(steps))
	}
	for _, s := range steps {
		isTesseract := strings.Contains(s.Command, "UB-Mannheim.TesseractOCR")
		if s.NeedsAdmin != isTesseract {
			t.Errorf("%q: NeedsAdmin = %v, want %v", s.Command, s.NeedsAdmin, isTesseract)
		}
		if len(s.Tools) != 1 {
			t.Errorf("%q: a winget step installs exactly one tool, got %d", s.Command, len(s.Tools))
		}
	}
}

// The flag is about winget's Tesseract package, not about Tesseract. Linux and
// macOS commands already carry their own sudo (or deliberately do not, for
// brew), so nothing there may be reported as needing a Windows prompt.
func TestNoStepNeedsAdminOffWindows(t *testing.T) {
	for _, m := range []Manager{ManagerDNF, ManagerAPT, ManagerZypper, ManagerPacman, ManagerBrew} {
		for _, s := range InstallSteps(m, HostTools) {
			if s.NeedsAdmin {
				t.Errorf("%s: %q flagged as needing a Windows admin prompt", m, s.Command)
			}
		}
	}
}

// InstallCommands is the projection of InstallSteps, so the two can never list
// different commands.
func TestInstallCommandsMatchSteps(t *testing.T) {
	for _, m := range []Manager{ManagerWinget, ManagerDNF, ManagerBrew} {
		steps := InstallSteps(m, HostTools)
		cmds := InstallCommands(m, HostTools)
		if len(steps) != len(cmds) {
			t.Fatalf("%s: %d steps vs %d commands", m, len(steps), len(cmds))
		}
		for i := range steps {
			if steps[i].Command != cmds[i] {
				t.Errorf("%s[%d]: step %q vs command %q", m, i, steps[i].Command, cmds[i])
			}
		}
	}
}
