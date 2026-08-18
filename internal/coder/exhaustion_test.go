package coder

import (
	"strings"
	"testing"
)

// At exhaustion we already know every fact worth reporting, so we do not need — and
// after the incident that produced this code, cannot trust — the model to narrate
// its own failure.
func TestExhaustionSummaryStatesWhatHappened(t *testing.T) {
	got := exhaustionSummary(callStats{
		Productive:     12,
		Total:          17,
		Failed:         5,
		SucceededTools: []string{"adguard_query_log", "write_file"},
	}, "budget")

	for _, want := range []string{"12", "adguard_query_log", "write_file"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary %q is missing %q", got, want)
		}
	}
	if strings.Contains(got, "[BLOCKED]") || strings.Contains(got, "[CHAT]") {
		t.Errorf("summary leaked a protocol marker to the user: %q", got)
	}
}

// A run that achieved nothing must not imply that it did.
func TestExhaustionSummaryWithNoProgress(t *testing.T) {
	got := exhaustionSummary(callStats{Total: 3, Failed: 3}, "unproductive")
	if strings.Contains(got, "Completed:") {
		t.Errorf("summary claims completed work where there was none: %q", got)
	}
	if got == "" {
		t.Fatal("summary must never be empty — it is the user's only account of the run")
	}
}
