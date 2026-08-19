package agentdesigner

import (
	"strings"
	"testing"
)

// The dry run and a real run must agree about what [SILENT] means when the
// agent ALSO said something.
//
// agentrunner.parseCoderOutput gives chat precedence: [SILENT] suppresses the
// prose fallback, but real [CHAT] content is still delivered. dryRunOutput
// checked for the marker first, so a rehearsal that reported news and then
// added [SILENT] was shown to the user as "nothing to report" — the build
// review claiming the agent would stay quiet about the very thing it had just
// found.
func TestDryRunPrefersChatOverALaterSilentMarker(t *testing.T) {
	out, ok := dryRunOutput("[CHAT]\nSkopje is 24C and clear.\n[SILENT]")
	if !ok {
		t.Fatal("a run with chat content must produce output")
	}
	if !strings.Contains(out, "24C") {
		t.Fatalf("chat content lost to a later [SILENT]: %q", out)
	}
	if strings.Contains(out, "nothing to report") {
		t.Fatalf("reported silence despite real content: %q", out)
	}
}

// The marker still means what it says when it is the whole answer — an agent
// built to stay quiet is working correctly, and the review must say so rather
// than show an empty box.
func TestDryRunStillReportsAGenuinelySilentRun(t *testing.T) {
	out, ok := dryRunOutput("[SILENT]")
	if !ok {
		t.Fatal("a silent run must still produce a review message")
	}
	if !strings.Contains(out, "nothing to report") {
		t.Fatalf("a genuinely silent run should say so: %q", out)
	}
}

// Decoration around a lone marker is still recognised — models emit **[SILENT]**
// and `[SILENT]` freely, and treating those as prose would deliver the literal
// marker text to the user as if it were the agent's output.
func TestDryRunRecognisesADecoratedSilentMarker(t *testing.T) {
	out, ok := dryRunOutput("**[SILENT]**")
	if !ok || !strings.Contains(out, "nothing to report") {
		t.Fatalf("decorated marker not recognised: ok=%v out=%q", ok, out)
	}
}
