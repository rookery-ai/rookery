package coder

import "testing"

// A turn is "productive" only when a tool actually did something. The engine uses
// this to decide whether a turn spends base budget, so the distinction has to be
// exact: a repeat that is short-circuited never reached a tool, and an error result
// means the tool ran and failed. Neither is progress.
func TestCallStatsCountsOnlyRealProgress(t *testing.T) {
	h := &hostToolSet{}

	h.noteCall("adguard_query_log", false) // succeeded
	h.noteCall("write_file", true)         // failed
	h.noteCall("adguard_query_log", false) // succeeded again

	got := h.callStats()
	if got.Productive != 2 {
		t.Errorf("Productive = %d, want 2", got.Productive)
	}
	if got.Total != 3 {
		t.Errorf("Total = %d, want 3", got.Total)
	}
	if got.Failed != 1 {
		t.Errorf("Failed = %d, want 1", got.Failed)
	}
	// Names feed the human-readable exhaustion summary, so they are deduped: a list
	// repeating one tool nine times tells the reader nothing.
	if len(got.SucceededTools) != 1 || got.SucceededTools[0] != "adguard_query_log" {
		t.Errorf("SucceededTools = %v, want [adguard_query_log]", got.SucceededTools)
	}
}

func TestCallStatsStartsEmpty(t *testing.T) {
	got := (&hostToolSet{}).callStats()
	if got.Productive != 0 || got.Total != 0 || got.Failed != 0 || len(got.SucceededTools) != 0 {
		t.Fatalf("fresh stats = %+v, want zeroes", got)
	}
}
