package onboard

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// lookup builds a LookPath that resolves exactly the named binaries. Detection
// has to be describable for a host we are not running on — that is the whole
// reason the probe is injected rather than called directly.
func lookup(present ...string) LookPath {
	set := map[string]bool{}
	for _, p := range present {
		set[p] = true
	}
	return func(bin string) (string, error) {
		if set[bin] {
			return "/usr/bin/" + bin, nil
		}
		return "", exec.ErrNotFound
	}
}

func TestMissingReportsOnlyAbsentTools(t *testing.T) {
	missing := Missing(lookup("python3", "rg"))

	var bins []string
	for _, m := range missing {
		bins = append(bins, m.Bin)
	}
	want := []string{"pdftotext", "tesseract"}
	if strings.Join(bins, ",") != strings.Join(want, ",") {
		t.Fatalf("missing = %v, want %v", bins, want)
	}
}

func TestMissingIsEmptyWhenEverythingIsPresent(t *testing.T) {
	if got := Missing(lookup("python3", "rg", "pdftotext", "tesseract")); len(got) != 0 {
		t.Fatalf("want nothing missing, got %v", got)
	}
}

// python3 is the one whose absence is a security property rather than a
// convenience: without it the agent-tool AST guardrail self-skips, so generated
// tool scripts run unchecked. Nothing else in the set is marked Critical, and
// nothing else should be.
func TestOnlyPython3IsCritical(t *testing.T) {
	for _, tool := range HostTools {
		if tool.Critical != (tool.Bin == "python3") {
			t.Errorf("%s Critical = %v", tool.Bin, tool.Critical)
		}
	}
}

// Every tool must resolve to a package name under every manager. A missing entry
// silently drops that tool from the install command — the same failure mode as
// the rpm's unresolvable weak dependency, which installed no OCR and said
// nothing for the life of the package.
func TestEveryManagerNamesEveryTool(t *testing.T) {
	managers := []Manager{ManagerDNF, ManagerAPT, ManagerZypper, ManagerPacman, ManagerBrew, ManagerWinget}
	for _, m := range managers {
		for _, tool := range HostTools {
			name, ok := PackageFor(m, tool.Bin)
			if !ok || name == "" {
				t.Errorf("%s has no package name for %s", m, tool.Bin)
			}
		}
	}
}

// The distribution-specific spellings that actually differ. Getting any of these
// wrong produces an install command that fails, or worse, a weak dependency that
// is dropped in silence.
func TestKnownPackageNameDivergences(t *testing.T) {
	cases := []struct {
		mgr  Manager
		bin  string
		want string
	}{
		{ManagerDNF, "tesseract", "tesseract"},        // Fedora — NOT tesseract-ocr
		{ManagerAPT, "tesseract", "tesseract-ocr"},    // Debian
		{ManagerDNF, "pdftotext", "poppler-utils"},    //
		{ManagerZypper, "pdftotext", "poppler-tools"}, // openSUSE
		{ManagerPacman, "pdftotext", "poppler"},       // Arch
		{ManagerBrew, "pdftotext", "poppler"},         // Homebrew
		{ManagerPacman, "python3", "python"},          // Arch ships python 3 as `python`
		{ManagerWinget, "pdftotext", "oschwartz10612.Poppler"},
	}
	for _, c := range cases {
		got, ok := PackageFor(c.mgr, c.bin)
		if !ok || got != c.want {
			t.Errorf("PackageFor(%s, %s) = %q, want %q", c.mgr, c.bin, got, c.want)
		}
	}
}

func TestInstallCommandsAreEmptyWithoutAManager(t *testing.T) {
	if got := InstallCommands(ManagerNone, HostTools); got != nil {
		t.Fatalf("want no commands without a manager, got %v", got)
	}
	if got := InstallCommands(ManagerDNF, nil); got != nil {
		t.Fatalf("want no commands with nothing missing, got %v", got)
	}
}

func TestInstallCommandsBatchExceptOnWinget(t *testing.T) {
	tools := []HostTool{{Bin: "rg"}, {Bin: "tesseract"}}

	dnf := InstallCommands(ManagerDNF, tools)
	if len(dnf) != 1 {
		t.Fatalf("dnf should install in one command, got %v", dnf)
	}
	if !strings.Contains(dnf[0], "ripgrep") || !strings.Contains(dnf[0], "tesseract") {
		t.Errorf("dnf command missing a package: %q", dnf[0])
	}

	// winget takes one package per invocation, so it is the one manager whose
	// output is a command per tool.
	win := InstallCommands(ManagerWinget, tools)
	if len(win) != 2 {
		t.Fatalf("winget should emit one command per package, got %v", win)
	}
	for _, c := range win {
		if !strings.Contains(c, "--accept-source-agreements") {
			t.Errorf("winget command would block on an interactive agreement prompt: %q", c)
		}
	}
}

// Homebrew refuses to run under sudo, and forcing it leaves root-owned files in
// the prefix that break later installs.
func TestBrewIsNeverRunUnderSudo(t *testing.T) {
	for _, c := range InstallCommands(ManagerBrew, HostTools) {
		if strings.Contains(c, "sudo") {
			t.Errorf("brew command must not use sudo: %q", c)
		}
	}
}

func TestDetectManagerFindsNothingOnABareHost(t *testing.T) {
	if got := DetectManager(func(string) (string, error) { return "", errors.New("nope") }); got != ManagerNone {
		t.Fatalf("want ManagerNone on a host with no package manager, got %q", got)
	}
}

// apt's binary is apt-get, not apt — probing for the wrong name means a Debian
// host reports no package manager at all.
func TestDetectManagerProbesAptGet(t *testing.T) {
	// This only exercises the non-darwin, non-windows branch, which is the one
	// running in CI.
	if got := DetectManager(lookup("apt-get")); got != ManagerAPT && got != ManagerNone {
		t.Fatalf("unexpected manager %q", got)
	}
}
