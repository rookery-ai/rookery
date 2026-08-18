package coder

import "testing"

// TestExhaustionSummaryIsNeverSuppressedAsScaffolding pins a property the two
// functions currently satisfy only by coincidence.
//
// exhaustionSummary deliberately NAMES the tools that succeeded, and the runner's
// deliverableProse runs LooksLikeToolScaffolding(summary, offered) over exactly that
// text before delivering it. Rule 1 fires on "markup token AND an offered tool name",
// so today the summary survives purely because it contains no <…> or ｜…｜ token —
// a property of the current wording, not a guarantee.
//
// If that regressed, the engine's own account of a run that ran out of steps would be
// suppressed as scaffolding and the user would receive silence: the precise failure
// this whole effort exists to remove. So the coincidence is made a contract here.
//
// The adversarial case is SucceededTools == offeredTools — every name the predicate
// searches for appears in the text.
func TestExhaustionSummaryIsNeverSuppressedAsScaffolding(t *testing.T) {
	offered := []string{"web_fetch", "web_search", "run_script", "write_file", "read_file"}
	stats := callStats{
		Productive:     3,
		Total:          9,
		Failed:         6,
		SucceededTools: offered,
	}

	for _, reason := range []string{"budget", "unproductive", "hard-ceiling"} {
		summary := exhaustionSummary(stats, reason)
		if summary == "" {
			t.Fatalf("reason %q: exhaustionSummary produced nothing to deliver", reason)
		}
		if LooksLikeToolScaffolding(summary, offered) {
			t.Errorf("reason %q: the engine's own report of a failed run was flagged as tool "+
				"scaffolding and would be suppressed, leaving the user with silence:\n%s", reason, summary)
		}
	}
}
