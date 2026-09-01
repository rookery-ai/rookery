package health

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/rookery-ai/rookery/internal/onboard"
)

// The one property the host-tool work exists to establish: setup and /healthz
// must never disagree about whether a tool is usable.
//
// Setup previously answered from onboard.Missing and /healthz from its own
// exec.LookPath, and the obvious fix for "setup cannot see my tools" — teach
// onboard to search harder — would have split those two answers apart. Setup
// would report "all present" while OCR, PDF extraction and the agent-tool AST
// guardrail stayed broken, because every one of those resolves through PATH.
// That is a silent failure replacing a loud one, and in the direction that
// matters: python3's absence disables a security control without printing
// anything.
//
// PATH augmentation is what keeps the two answers identical, and this is the
// test that would catch it coming apart. It runs on Linux, where it can be made
// fully deterministic, even though the bug it guards is a Windows one.
//
// The agreement is asserted AFTER augmenting, and the asymmetry is real rather
// than a convenience: between process start and AugmentProcessPath the resolver
// genuinely knows more than PATH does, which is the entire reason augmentation
// exists. An earlier draft of this test asserted agreement before it too, and
// failed on all four tools — which is the clearest possible statement of why
// the augmentation has to run in main's Before hook, ahead of anything that
// reads either surface. cmd/rookery's own test pins that wiring; if it is ever
// removed, this window stops being theoretical.
func TestSetupAndHealthzAgreeAboutHostTools(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in tools are shell scripts")
	}

	home := t.TempDir()
	localBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tool := range onboard.HostTools {
		body := "#!/bin/sh\nexit 0\n"
		if tool.Bin == "python3" {
			// This one is verified by being run, not by existing.
			body = "#!/bin/sh\necho 'Python 3.13.0'\n"
		}
		if err := os.WriteFile(filepath.Join(localBin, tool.Bin), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// An empty PATH removes every dependency on what is installed on the machine
	// running this test, and reproduces the reported shape exactly: the tools are
	// on disk, and PATH does not reach them.
	t.Setenv("HOME", home)
	t.Setenv("PATH", "")

	onboard.AugmentProcessPath()

	assertAgreement(t, "tools installed off PATH", true)
}

// The other half: a host that genuinely has none of them must have both
// surfaces say so. Reporting a tool as present is the more dangerous error —
// python3's absence disables the AST guardrail silently — so "we found nothing"
// has to survive augmentation rather than being papered over by it.
func TestSetupAndHealthzAgreeWhenNothingIsInstalled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH semantics differ")
	}

	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", "")

	if added := onboard.AugmentProcessPath(); len(added) != 0 {
		t.Fatalf("nothing is installed, yet directories were added: %v", added)
	}

	assertAgreement(t, "nothing installed", false)
}

// assertAgreement checks the two surfaces against each other, and against what
// the situation actually is.
func assertAgreement(t *testing.T, when string, wantPresent bool) {
	t.Helper()

	missing := map[string]bool{}
	for _, tool := range onboard.MissingOn(onboard.CurrentHost()) {
		missing[tool.Bin] = true
	}

	tools := Detect(false, "full").Tools
	reported := map[string]bool{
		"python3":   tools.Python3,
		"rg":        tools.Ripgrep,
		"pdftotext": tools.PDFToText,
		"tesseract": tools.Tesseract,
	}

	for bin, healthSaysPresent := range reported {
		setupSaysPresent := !missing[bin]

		if setupSaysPresent != healthSaysPresent {
			t.Errorf("%s: setup and /healthz disagree about %s — setup says present=%v, /healthz says present=%v",
				when, bin, setupSaysPresent, healthSaysPresent)
		}
		if healthSaysPresent != wantPresent {
			t.Errorf("%s: %s reported present=%v, want %v", when, bin, healthSaysPresent, wantPresent)
		}
	}
}
