package skilldesigner

import (
	"strings"
	"testing"
)

// A skill build whose tool loop was cut short still advances to review — it may well be
// complete — but it must never be PRESENTED as finished.
//
// The regression this guards is the skill-designer half of the one already fixed for
// agents: a skill build sets buildphase.Generation, so it shares the new turn budget, and
// once the grace turn started always returning a non-nil Result instead of ErrMaxTurns
// the honest-refusal branch went dead. The deterministic exhaustionSummary carries no
// [BLOCKED], so the only remaining gate saw nothing and the build reached "Does this look
// right?" with no sign it had run out of turns. The caveat is keyed on the engine's own
// Result.StopReason, never on the model remembering to emit a marker.
func TestCaveatTruncatedBuild(t *testing.T) {
	const review = "Here's the generated skill and how it tested:\n\n---\nAll scripts compile.\n---\n\nDoes this look right?"

	t.Run("a normal finish is untouched", func(t *testing.T) {
		if got := caveatTruncatedBuild(review, ""); got != review {
			t.Errorf("an uncut build was caveated:\n%s", got)
		}
	})

	for _, reason := range []string{"budget", "unproductive", "hard-ceiling"} {
		t.Run("stop reason "+reason+" is caveated", func(t *testing.T) {
			got := caveatTruncatedBuild(review, reason)
			if !strings.HasPrefix(got, truncatedBuildCaveat) {
				t.Fatalf("a truncated build was presented as a finished one:\n%s", got)
			}
			if !strings.Contains(got, review) {
				t.Errorf("the review message was lost behind the caveat:\n%s", got)
			}
		})
	}
}
