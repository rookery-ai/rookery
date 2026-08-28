package agentdesigner

import (
	"os"
	"strings"
	"testing"
)

// These are source-shape assertions, in the same spirit as
// mcp_build_wiring_test.go. They exist because the defect they pin was
// invisible to every behavioural test: the build simply had no browser, so
// nothing failed — a rehearsal just quietly could not open the page the agent
// was being written to read, and decideBuildOutcome then reported the
// "couldn't confirm" outcome the dry run exists to remove.
//
// A real end-to-end assertion would need a live coder and a real Chromium in
// CI, which this repo deliberately does not have. Reading the wiring is the
// strongest check available, and it is strictly better than the nothing that
// let this ship.

func readSource(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestBuildCoderIsGivenTheBrowser(t *testing.T) {
	body := readSource(t, "flow.go")
	i := strings.Index(body, "generationCoder := coderSvc.WithDir(workDir)")
	if i < 0 {
		t.Fatal("could not find the generation coder construction")
	}
	j := strings.Index(body[i:], "generationCoder.Generate(")
	if j < 0 {
		t.Fatal("could not find the generation call")
	}
	if !strings.Contains(body[i:i+j], "WithBrowser(f.browser") {
		t.Error("the build coder never receives the browser, so a rehearsal cannot open a page")
	}
}

func TestDryRunIsGivenTheBrowser(t *testing.T) {
	body := readSource(t, "dryrun.go")
	if !strings.Contains(body, "WithBrowser(f.browser") {
		t.Error("the dry run never receives the browser, so its sample cannot describe a rendered page")
	}
}

// The build must be handed a ZERO policy. Acting is refused during a build by
// browser.CheckAct regardless, but passing a permissive policy here would mean
// the refusal is the only thing standing between a rehearsal and a real "Pay"
// click — and defence in depth is the whole reason the policy is a parameter.
func TestBuildAndDryRunPassAReadOnlyBrowserPolicy(t *testing.T) {
	for _, f := range []string{"flow.go", "dryrun.go"} {
		body := readSource(t, f)
		if !strings.Contains(body, "WithBrowser(f.browser, browser.Policy{})") {
			t.Errorf("%s does not pass an empty (read-only) browser policy", f)
		}
	}
}

// Without this the build prompt never mentions the browser, so a weak model
// hand-writes Playwright — the exact failure the native tools replace.
func TestBuildPromptIsToldTheBrowserExists(t *testing.T) {
	body := readSource(t, "flow.go")
	if !strings.Contains(body, "BrowserAvailable: f.browser != nil") {
		t.Error("ImplementationParams.BrowserAvailable is never set, so the build prompt omits the browser")
	}
}
