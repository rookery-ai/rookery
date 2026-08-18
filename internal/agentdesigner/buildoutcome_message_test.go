package agentdesigner

import (
	"strings"
	"testing"
)

// The review step is where a user decides whether to trust an agent. Telling them
// "here's what a test run produces" when nothing ran teaches them not to trust the
// step at all — and for a TIER 1 agent (no script) nothing DID run, which is the
// common case rather than the exotic one.
func TestReviewMessageOnlyClaimsATestRunWhenSomethingRan(t *testing.T) {
	executed := reviewMessage("3 files changed", true)
	if !strings.Contains(executed, "test run") {
		t.Errorf("an executed sample should be presented as a test run: %q", executed)
	}

	notExecuted := reviewMessage("I will list the files and summarise each.", false)
	if strings.Contains(notExecuted, "test run produces") {
		t.Errorf("prose was presented as a test run: %q", notExecuted)
	}
	if !strings.Contains(strings.ToLower(notExecuted), "didn't run") &&
		!strings.Contains(strings.ToLower(notExecuted), "did not run") &&
		!strings.Contains(strings.ToLower(notExecuted), "couldn't run") {
		t.Errorf("a non-executed sample must say so plainly: %q", notExecuted)
	}
}

// Both forms must still tell the user how to proceed — the message is the only
// place the next action is named.
func TestReviewMessageAlwaysOffersTheNextStep(t *testing.T) {
	for _, executed := range []bool{true, false} {
		got := reviewMessage("sample", executed)
		if !strings.Contains(got, "approve") {
			t.Errorf("reviewMessage(executed=%v) does not tell the user how to save: %q", executed, got)
		}
		if !strings.Contains(got, "sample") {
			t.Errorf("reviewMessage(executed=%v) dropped the sample: %q", executed, got)
		}
	}
}
