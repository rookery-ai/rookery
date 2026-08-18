package agentdesigner

import (
	"strings"
	"testing"
)

// A build whose tool loop was cut short still advances to review — it may well be
// complete — but it must never be PRESENTED as finished.
//
// The regression this guards is subtle: once the grace turn started always returning a
// non-nil Result instead of ErrMaxTurns, a build whose script had already verified took
// the fully confident "Here's what a test run produces… Does this look right?" path with
// nothing anywhere saying it had run out of turns. The caveat is keyed on the engine's
// own Result.StopReason, never on the model remembering to emit a marker.
func TestCaveatTruncatedBuild(t *testing.T) {
	const review = "Here's what a test run produces:\n\nGood morning, Ilija!\n\nDoes this look right?"

	t.Run("a normal finish is untouched", func(t *testing.T) {
		if got := caveatTruncatedBuild(review, "", ""); got != review {
			t.Errorf("an uncut build was caveated:\n%s", got)
		}
	})

	for _, reason := range []string{"budget", "unproductive", "hard-ceiling"} {
		t.Run("stop reason "+reason+" is caveated", func(t *testing.T) {
			got := caveatTruncatedBuild(review, reason, "")
			if !strings.HasPrefix(got, truncatedBuildCaveat) {
				t.Fatalf("a truncated build was presented as a finished one:\n%s", got)
			}
			if !strings.Contains(got, review) {
				t.Errorf("the review message was lost behind the caveat:\n%s", got)
			}
		})
	}

	// A [BLOCKED] reply carries the model's own account of what it could not do, and
	// reconcileBlockedOutcome already prepends its own heads-up. It passes through
	// untouched rather than collecting a second warning about the same event.
	t.Run("a [BLOCKED] reply passes through untouched", func(t *testing.T) {
		blockedMsg := "⚠️ Heads up — I built this but couldn't fully confirm it works end to end: " +
			"the weather API needs a key.\n\n" + review
		if got := caveatTruncatedBuild(blockedMsg, "budget", "the weather API needs a key."); got != blockedMsg {
			t.Errorf("a [BLOCKED] reply was modified:\n%s", got)
		}
	})
}
