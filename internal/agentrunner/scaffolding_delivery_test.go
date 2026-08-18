package agentrunner

import (
	"strings"
	"testing"
)

// The prose fallback exists to rescue a forgotten [CHAT] marker. It must not also
// forward the model's tool-call machinery — which is exactly what reached a user's
// phone in the incident this guards.
func TestDeliverableProseRefusesScaffolding(t *testing.T) {
	tools := []string{"adguard_query_log", "write_file"}

	scaffolding := "<｜DSML｜tool_calls>\n<｜DSML｜invoke name=\"adguard_query_log\">\n" +
		"<｜DSML｜parameter name=\"limit\" string=\"false\">10</｜DSML｜parameter>\n" +
		"</｜DSML｜invoke>\n</｜DSML｜tool_calls>"
	if got := deliverableProse(scaffolding, tools); got != "" {
		t.Errorf("deliverableProse returned %q, want \"\" — this reached a real user's phone", got)
	}

	real := "3 new blocked domains overnight: doubleclick.net and app-measurement.com."
	if got := deliverableProse(real, tools); got != real {
		t.Errorf("deliverableProse suppressed a real message: got %q", got)
	}
}

// Protocol markers are still stripped — the new check is a floor under that
// behaviour, not a replacement for it.
func TestDeliverableProseStillStripsMarkers(t *testing.T) {
	got := deliverableProse("[STATE]{\"a\":1}[/STATE]\nAll quiet overnight.", nil)
	if !strings.Contains(got, "All quiet overnight.") {
		t.Fatalf("prose lost: %q", got)
	}
	if strings.Contains(got, "[STATE]") {
		t.Fatalf("marker leaked: %q", got)
	}
}

// The delivery half of the exhaustion-summary contract. Its sibling lives in
// internal/coder (TestExhaustionSummaryIsNeverSuppressedAsScaffolding): the two
// functions are unexported in different packages, so one test cannot reach both,
// and they pin two genuinely different failure modes.
//
//   - There: the engine's summary must not be FLAGGED by LooksLikeToolScaffolding,
//     which it survives only because rule 1 also demands a markup token.
//   - Here: it must also survive extractProseMessage and reach the user verbatim.
//
// Either failing means a run that ran out of steps reports nothing at all — silence,
// the exact outcome this whole effort exists to remove. Equality, not Contains: an
// unexpected strip must fail rather than pass on a substring.
func TestExhaustionSummarySurvivesDelivery(t *testing.T) {
	tools := []string{"web_fetch", "run_script", "write_file"}

	// Shaped exactly like coder.exhaustionSummary's output, tool names included.
	summary := "⚠️ Stopped early: several tool calls in a row achieved nothing. " +
		"Completed: 3 successful tool calls (web_fetch, run_script, write_file). " +
		"2 failed. See the run log for detail."

	if got := deliverableProse(summary, tools); got != summary {
		t.Fatalf("the engine's own account of a failed run was not delivered intact:\n got  %q\n want %q", got, summary)
	}
}
