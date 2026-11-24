package packaging

import (
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/onboard"
)

// install.ps1 and internal/onboard must agree on what Python is called,
// because when they did not the disagreement was permanent.
//
// The script probed `python` and installed Python.Python.3.13; the Go side
// probed `python3`, which python.org's distribution does not ship. So the
// installer installed Python, reported success, and setup asked for it again —
// on that run and on every run afterwards, since installing it a second time
// changed nothing. Neither file is wrong when read alone, which is why this
// needs a test that reads both.
func TestTheWindowsInstallerProbesEveryPythonSpellingOnboardDoes(t *testing.T) {
	ps1 := repoFile(t, "install.ps1")

	var python onboard.HostTool
	for _, tool := range onboard.HostTools {
		if tool.Bin == "python3" {
			python = tool
		}
	}
	if python.Bin == "" {
		t.Fatal("internal/onboard no longer has a python3 host tool")
	}

	for _, name := range python.Bins("windows") {
		if !strings.Contains(ps1, "'"+name+"'") {
			t.Errorf("install.ps1 does not probe %q, which internal/onboard accepts on Windows — "+
				"the two will disagree about whether Python is installed", name)
		}
	}
}

// Both sides prove Python by running it, and both must keep doing so.
//
// Stock Windows ships an App Execution Alias named python3.exe that resolves
// like a real command and opens the Microsoft Store. Probing for existence
// alone would make the installer skip Python and setup report it present, which
// leaves the agent-tool AST guardrail disabled with nothing printed anywhere —
// strictly worse than the re-offering bug this whole change set is about.
func TestTheWindowsInstallerVerifiesPythonByRunningIt(t *testing.T) {
	ps1 := repoFile(t, "install.ps1")

	if !strings.Contains(ps1, "--version") {
		t.Error("install.ps1 must run a candidate to prove it is Python, not merely find it")
	}
	if !strings.Contains(ps1, "'Python'") && !strings.Contains(ps1, "match 'Python'") {
		t.Error("install.ps1 must check the output names Python — the Store stub resolves but is not an interpreter")
	}

	var python onboard.HostTool
	for _, tool := range onboard.HostTools {
		if tool.Bin == "python3" {
			python = tool
		}
	}
	if !python.VerifyByRunning {
		t.Error("internal/onboard stopped verifying python3 by running it; the Store stub will be accepted")
	}
}

// Tesseract's winget package is a machine-wide installer and raises a Windows
// permission prompt; the other three are not. Both surfaces must warn about the
// same packages, or the installer and setup give contradicting advice about the
// one install that needs it.
func TestTheWindowsInstallerFlagsTheSameElevatedPackagesAsOnboard(t *testing.T) {
	ps1 := repoFile(t, "install.ps1")

	want := map[string]bool{}
	for _, tool := range onboard.HostTools {
		if !tool.WindowsNeedsAdmin {
			continue
		}
		id, ok := onboard.PackageFor(onboard.ManagerWinget, tool.Bin)
		if !ok {
			t.Fatalf("%s needs admin on Windows but has no winget package", tool.Bin)
		}
		want[id] = true
	}

	got := map[string]bool{}
	for _, line := range strings.Split(ps1, "\n") {
		if !strings.Contains(line, "Admin = $true") {
			continue
		}
		i := strings.Index(line, "Winget = '")
		if i < 0 {
			t.Errorf("install.ps1 marks a line Admin without a winget id: %q", strings.TrimSpace(line))
			continue
		}
		rest := line[i+len("Winget = '"):]
		got[rest[:strings.Index(rest, "'")]] = true
	}

	for id := range want {
		if !got[id] {
			t.Errorf("internal/onboard says %s needs administrator permission; install.ps1 does not mark it Admin", id)
		}
	}
	for id := range got {
		if !want[id] {
			t.Errorf("install.ps1 marks %s Admin; internal/onboard does not", id)
		}
	}
	if !strings.Contains(ps1, "Run as administrator") {
		t.Error("install.ps1 must tell the user how to retry an elevated install that failed")
	}
}
