package agentdesigner

import (
	"strings"
	"testing"

	"github.com/ilijad1/rookery/internal/prompts"
)

// TestReconcileBlockedOutcome covers the A9/D1 headline fix: a [BLOCKED] marker must not
// destroy a build that is actually complete on disk. The reconciliation folds the marker
// into the on-disk decision instead of short-circuiting to the destroy path.
func TestReconcileBlockedOutcome(t *testing.T) {
	t.Run("presentable + blocker → advance with an honest caveat (keep the build)", func(t *testing.T) {
		d := buildDecision{presentable: true, message: "Here's what a test run produces: ...", thinProof: true}
		got := reconcileBlockedOutcome(d, "What couldn't be done: I couldn't confirm the fetch in time.", prompts.BackendFullCoder)
		if !got.advance {
			t.Fatal("a presentable build must advance to review even when [BLOCKED] was emitted (do not destroy correct work)")
		}
		if !strings.Contains(got.message, "couldn't fully confirm") {
			t.Errorf("advanced-with-blocker message must carry an honest caveat; got %q", got.message)
		}
		if !strings.Contains(got.message, "test run produces") {
			t.Errorf("the caveat must be prepended to the original review message, not replace it; got %q", got.message)
		}
	})

	t.Run("weak backend + not-presentable gate + no blocker → DROP-THE-SCRIPT note + forceTier1", func(t *testing.T) {
		// This is the common case: the weak model finished with no [BLOCKED] marker, and
		// decideBuildOutcome's gate set presentable=false + hasAuthoredScript=true. The retry
		// note must NOT be the generic "safety/quality check" note, which is false here
		// (ethics + guardrails passed) and would make the retry rewrite clean code.
		//
		// The note offers exactly ONE option: drop the script. It previously also offered
		// "actually run the script and show its real output" — the approach that had just
		// failed — and a weak model re-picked it every time, regenerating the same
		// unverifiable script. forceTier1 makes the remaining option binding, since the note
		// itself is advisory prose the model may ignore.
		d := buildDecision{presentable: false, hasAuthoredScript: true, message: "…keep going…"}
		got := reconcileBlockedOutcome(d, "", prompts.BackendToolCalling)
		if got.advance {
			t.Fatal("weak-gate build must not advance")
		}
		if got.message != d.message {
			t.Errorf("must keep decideBuildOutcome's user message; got %q", got.message)
		}
		if !strings.Contains(got.recordFailNote, "drop the script") {
			t.Errorf("retry note must steer toward dropping the script; got %q", got.recordFailNote)
		}
		if strings.Contains(got.recordFailNote, "run the script and show") {
			t.Errorf("retry note must NOT re-offer the approach that just failed; got %q", got.recordFailNote)
		}
		if !got.forceTier1 {
			t.Error("weak-gate build must force TIER 1 on the retry — the advisory note alone is what let the loop persist")
		}
		if strings.Contains(strings.ToLower(got.recordFailNote), "safety/quality") {
			t.Errorf("must NOT record the misleading generic safety/quality note; got %q", got.recordFailNote)
		}
	})

	t.Run("weak backend + presentable + blocker → holds back AND forces TIER 1", func(t *testing.T) {
		// The other weak-backend branch: the build IS presentable on disk but the coder
		// emitted [BLOCKED] and never confirmed its script. Same remedy as above — the retry
		// must not re-attempt the script — so this branch must set forceTier1 too. It was the
		// easier one to miss, since the build looks complete.
		d := buildDecision{presentable: true, hasAuthoredScript: true, message: "review it"}
		got := reconcileBlockedOutcome(d, "ran out of turns", prompts.BackendToolCalling)
		if got.advance {
			t.Fatal("weak-gate build with an unverified script must not advance")
		}
		if !got.forceTier1 {
			t.Error("presentable+blocked weak-backend build must also force TIER 1 on the retry")
		}
	})

	t.Run("forceTier1 stays off when the script WAS verified, and for capable backends", func(t *testing.T) {
		// The override is a response to an unverified script specifically. A confirmed run,
		// or a backend that self-verifies reliably, must not be constrained — forcing TIER 1
		// there would forbid scripts on builds that legitimately need one.
		verified := buildDecision{presentable: false, hasAuthoredScript: true, scriptVerified: true,
			message: "x", recordFailNote: "n"}
		if got := reconcileBlockedOutcome(verified, "", prompts.BackendToolCalling); got.forceTier1 {
			t.Error("a verified script must not force TIER 1")
		}
		capable := buildDecision{presentable: false, hasAuthoredScript: true, message: "x", recordFailNote: "n"}
		if got := reconcileBlockedOutcome(capable, "", prompts.BackendFullCoder); got.forceTier1 {
			t.Error("a capable CLI backend must not force TIER 1")
		}
		noScript := buildDecision{presentable: false, message: "x", recordFailNote: "n"}
		if got := reconcileBlockedOutcome(noScript, "", prompts.BackendToolCalling); got.forceTier1 {
			t.Error("a build with no authored script has nothing to forbid — must not force TIER 1")
		}
	})

	t.Run("full coder + not-presentable + no blocker → uses the per-case steering note (not the old generic safety note)", func(t *testing.T) {
		// A genuine guardrail/ethics/no-AGENT.md hard-fail now carries an honest, steering
		// recordFailNote from decideBuildOutcome (e.g. "avoid the blocked construct", "write
		// AGENT.md first"). reconcileBlockedOutcome must pass it through verbatim so the next
		// attempt converges — the old generic "rejected by an automated safety/quality check"
		// note was misleading (it pushed the coder to "take a different approach" and rewrite
		// clean code, reopening the loop) and is deliberately gone.
		d := buildDecision{presentable: false, message: "One of the files didn't pass a check",
			recordFailNote: "a generated tool failed an internal code check. Next attempt: avoid the blocked construct."}
		got := reconcileBlockedOutcome(d, "", prompts.BackendFullCoder)
		if got.advance {
			t.Fatalf("genuine hard-fail must not advance; got advance=true")
		}
		if got.recordFailNote != d.recordFailNote {
			t.Errorf("must use the per-case steering note verbatim; got %q want %q", got.recordFailNote, d.recordFailNote)
		}
		if strings.Contains(strings.ToLower(got.recordFailNote), "safety/quality") {
			t.Errorf("the misleading generic safety/quality note must NOT survive; got %q", got.recordFailNote)
		}
	})

	t.Run("full coder + not-presentable + no blocker + no per-case note → plain non-empty fallback", func(t *testing.T) {
		// If decideBuildOutcome didn't set a recordFailNote, reconcileBlockedOutcome falls back
		// to a plain, non-jargony note (never the old "safety/quality" wording).
		d := buildDecision{presentable: false, message: "One of the files didn't pass a check"}
		got := reconcileBlockedOutcome(d, "", prompts.BackendFullCoder)
		if got.advance {
			t.Fatalf("non-presentable must not advance; got advance=true")
		}
		if got.recordFailNote == "" {
			t.Fatal("a failure note must still be recorded so a forgiving retry has context")
		}
		if strings.Contains(strings.ToLower(got.recordFailNote), "safety/quality") {
			t.Errorf("fallback must not use the old misleading safety/quality wording; got %q", got.recordFailNote)
		}
	})

	t.Run("weak backend + presentable + authored script + blocker → STAY (do not ship unverified)", func(t *testing.T) {
		// A clean [TEST_OUTPUT] made this presentable (thinProof=false), but the coder ALSO
		// emitted [BLOCKED] and it authored its own script — on the weak backend that means
		// the self-verify topped out, so we must not advance.
		d := buildDecision{presentable: true, message: "Here's what a test run produces: ...", hasAuthoredScript: true}
		got := reconcileBlockedOutcome(d, "What couldn't be done: the fetch timed out.", prompts.BackendToolCalling)
		if got.advance {
			t.Fatal("weak backend with an unverified authored script + blocker must NOT advance")
		}
		if got.recordFailNote == "" {
			t.Error("a failure note must be recorded so a forgiving retry re-runs with context")
		}
		if !strings.Contains(got.message, "keep going") {
			t.Errorf("message should invite the user to retry; got %q", got.message)
		}
	})

	t.Run("weak backend + presentable + NO authored script + blocker → still advances", func(t *testing.T) {
		// Pure-reasoning build (no script) has nothing unverified to hold back, even on weak.
		d := buildDecision{presentable: true, message: "Here's what a test run produces: X", hasAuthoredScript: false}
		got := reconcileBlockedOutcome(d, "What couldn't be done: minor caveat.", prompts.BackendToolCalling)
		if !got.advance {
			t.Fatal("a pure-reasoning presentable build must still advance on the weak backend")
		}
	})

	t.Run("presentable, no blocker → normal review message unchanged", func(t *testing.T) {
		d := buildDecision{presentable: true, message: "Here's what a test run produces: X"}
		got := reconcileBlockedOutcome(d, "", prompts.BackendFullCoder)
		if !got.advance || got.message != d.message {
			t.Errorf("clean presentable build must advance with the unchanged message; got advance=%v msg=%q", got.advance, got.message)
		}
	})

	t.Run("not presentable + blocker → stay, show plain blocker, record it for retry", func(t *testing.T) {
		d := buildDecision{presentable: false, message: "generic not-presentable", logReason: "internal detail"}
		got := reconcileBlockedOutcome(d, "What couldn't be done: the website blocks automated access.", prompts.BackendFullCoder)
		if got.advance {
			t.Fatal("a non-presentable blocked build must not advance")
		}
		if !strings.Contains(got.message, "blocks automated access") {
			t.Errorf("a genuine blocker should surface the coder's plain explanation; got %q", got.message)
		}
		if got.recordFailNote == "" {
			t.Error("a failure note must be recorded so a forgiving retry re-runs with context (C2)")
		}
	})

	t.Run("not presentable, no blocker → generic message, PLAIN recorded note (no jargon)", func(t *testing.T) {
		// logReason is technical because it feeds the server-side slog.Warn. The recorded
		// note, however, lands in History and is replayed to the text-only design coder that
		// talks to the user — so it must NOT carry guardrail/AST/subprocess jargon that the
		// design coder could echo to a non-technical user.
		d := buildDecision{
			presentable: false,
			message:     "One of the files didn't pass a check",
			logReason:   "generated tool failed guardrails: tools/x.py: ast check: forbidden: subprocess.run()",
		}
		got := reconcileBlockedOutcome(d, "", prompts.BackendFullCoder)
		if got.advance {
			t.Fatal("non-presentable must not advance")
		}
		if got.message != d.message {
			t.Errorf("without a blocker the generic message is shown; got %q", got.message)
		}
		if got.recordFailNote == "" {
			t.Fatal("a failure note must still be recorded so a forgiving retry has context")
		}
		lower := strings.ToLower(got.recordFailNote)
		for _, jargon := range []string{"subprocess", "ast check", "guardrail", "tools/", ".py", "forbidden:"} {
			if strings.Contains(lower, jargon) {
				t.Errorf("recorded note leaks jargon %q (it is replayed to the user-facing design coder): %q", jargon, got.recordFailNote)
			}
		}
	})
}

// TestIsRetryApproval covers the C1 forgiving-retry matcher used only after a failure.
func TestIsRetryApproval(t *testing.T) {
	// Accepted: everything isVerifyApproval takes, plus natural retry phrases.
	for _, in := range []string{
		"try again", "Try again please", "fix it", "just fix it", "retry",
		"yes", "ok", "do it", "go ahead", "approve",
	} {
		if !isRetryApproval(in) {
			t.Errorf("isRetryApproval(%q) = false, want true (post-failure retry should be forgiving)", in)
		}
	}
	// Rejected: a change request must still route to the design chat (not a blind re-run),
	// and clear negatives.
	for _, in := range []string{
		"change the schedule then try again", "don't retry yet", "not yet",
		"wait", "let's use a different service instead", "what went wrong?",
	} {
		if isRetryApproval(in) {
			t.Errorf("isRetryApproval(%q) = true, want false (change/negative must go to design chat)", in)
		}
	}
}
