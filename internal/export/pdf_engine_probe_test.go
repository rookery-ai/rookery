package export

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// writeScript puts an executable shell script on disk and returns its path.
func writeScript(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// A renderer that hangs must be rejected, and rejected QUICKLY.
//
// This is the defect: `/usr/bin/chromium` on Ubuntu is frequently a wrapper that
// never renders and never returns. Because engine selection only asked whether
// the name resolved, the export committed to it and sat there until pdfTimeout
// (30s) killed it, reporting `signal: killed` — a message that names neither the
// engine nor the reason, and that recurs on every export because the next one
// picks the same binary again.
func TestAHangingEngineIsRejectedQuickly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in engine is a shell script")
	}
	hang := writeScript(t, "chromium", "#!/bin/sh\nsleep 300\n")

	start := time.Now()
	ok := engineRunsReal(hang)
	elapsed := time.Since(start)

	if ok {
		t.Fatal("a renderer that never returns was accepted")
	}
	if elapsed >= pdfTimeout {
		t.Errorf("rejecting a hung engine took %s — it must be bounded by the probe timeout (%s), not the render timeout (%s)",
			elapsed, engineProbeTimeout, pdfTimeout)
	}
}

// A binary that runs but fails is equally not a renderer. A broken install and
// a missing one should reach the same outcome.
func TestAFailingEngineIsRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in engine is a shell script")
	}
	broken := writeScript(t, "chromium", "#!/bin/sh\nexit 1\n")

	if engineRunsReal(broken) {
		t.Error("a renderer that exits non-zero was accepted")
	}
}

// The happy path must stay cheap: this runs on every export, so a healthy
// engine has to cost a version print and nothing more.
func TestAWorkingEngineIsAccepted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in engine is a shell script")
	}
	good := writeScript(t, "chromium", "#!/bin/sh\necho 'Chromium 140.0.0'\n")

	start := time.Now()
	if !engineRunsReal(good) {
		t.Fatal("a working renderer was rejected")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("probing a healthy engine took %s; it should be immediate", elapsed)
	}
}

// The point of rejecting a broken engine is that the NEXT one gets a turn. The
// ordered list has always implied that and never delivered it, because the
// first name that resolved won outright.
func TestFindPDFEngineFallsThroughToTheNextEngine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in engines are shell scripts")
	}
	hang := writeScript(t, "hanging", "#!/bin/sh\nsleep 300\n")
	good := writeScript(t, "working", "#!/bin/sh\necho ok\n")

	saved := pdfEngines
	t.Cleanup(func() { pdfEngines = saved })
	pdfEngines = []pdfEngine{
		{bin: "hanging", locate: func() (string, bool) { return hang, true }, command: chromiumArgs},
		{bin: "working", locate: func() (string, bool) { return good, true }, command: chromiumArgs},
	}

	eng, path, ok := findPDFEngine()

	if !ok {
		t.Fatal("no engine was selected though a working one was available")
	}
	if eng.bin != "working" || path != good {
		t.Errorf("selected %q at %s; the hanging engine should have been skipped", eng.bin, path)
	}
}

// With nothing usable, the honest answer is "unavailable" rather than a
// selection that will hang later.
func TestFindPDFEngineReportsNoneWhenEveryEngineIsBroken(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in engines are shell scripts")
	}
	broken := writeScript(t, "broken", "#!/bin/sh\nexit 1\n")

	saved := pdfEngines
	t.Cleanup(func() { pdfEngines = saved })
	pdfEngines = []pdfEngine{
		{bin: "broken", locate: func() (string, bool) { return broken, true }, command: chromiumArgs},
	}

	if _, _, ok := findPDFEngine(); ok {
		t.Error("an engine that cannot run was reported as available")
	}
}
