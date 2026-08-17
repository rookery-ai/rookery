package agentdesigner

import (
	"strings"

	"github.com/rookery-ai/rookery/internal/db"
)

// The designer appends a [TECHNICAL SPEC] block to the message in which it
// proposes a plan (see prompts.BuildDesignSystemPrompt's <your_job>). The block
// serves two purposes at once, which is why it is handled here rather than at
// either end:
//
//   - It is the code generator's brief. BuildImplementationPrompt reads the
//     conversation through dbMessagesToPrompt and refers to "the design's
//     [TECHNICAL SPEC]" by name, so the block MUST survive into History.
//   - It is the only reliable signal that the conversation has moved from
//     questions to a settled plan. Flow.Snapshot derives PlanReady from it, and
//     the browser uses that to decide whether to offer the approve-and-build
//     button at all.
//
// So History stores the raw assistant text, block included, and the block is
// stripped at the two edges the USER reads from: callCoder's return value and
// the web layer's history DTO. Stripping before storage instead would be the
// tempting simplification and would silently re-break the implementation
// prompt, which is exactly the state this code was written to repair — the
// prompt asked for the block "after the user approves", a turn that never
// happens because approval routes straight to startGeneration, so the generator
// had been reading a block nothing ever wrote.
const (
	specOpen  = "[TECHNICAL SPEC]"
	specClose = "[/TECHNICAL SPEC]"
)

// extractTechnicalSpec returns the body of the LAST well-formed
// [TECHNICAL SPEC]…[/TECHNICAL SPEC] block in s, trimmed. It returns "" when
// there is no opener, or when the last opener has no closer after it.
//
// Requiring a CLOSER is what makes PlanReady honest: a response truncated by a
// token cap can end mid-block, and treating that as a finished plan would put
// the build button under a proposal the model never got to finish writing.
//
// The LAST block wins because a long conversation can accumulate several — one
// per proposal turn — and the current plan is the most recent one.
func extractTechnicalSpec(s string) string {
	open := strings.LastIndex(s, specOpen)
	if open < 0 {
		return ""
	}
	rest := s[open+len(specOpen):]
	end := strings.Index(rest, specClose)
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}

// stripTechnicalSpec removes every [TECHNICAL SPEC] block from s, inclusive of
// its markers, and tidies the whitespace the removal leaves behind.
//
// An UNTERMINATED opener is removed to end-of-string (stripBlock's existing
// behaviour). That asymmetry with extractTechnicalSpec is deliberate: a
// half-written block is not a plan (so it must not arm the build button) but it
// is also not prose (so it must not be shown to the user).
// Exported because the web layer's designHistoryDTO replays stored History to
// the browser and must strip the same block callCoder strips from a live turn.
// Two copies of this rule would be two chances for the transcript and the
// resumed transcript to disagree about what the user was shown.
//
// Deprecated for new call sites: use UserFacingDesignText, which also refuses to
// hand back an empty turn. Kept because stripping and "what the user sees" are
// genuinely different questions and a caller may want only the former.
func StripTechnicalSpec(s string) string { return stripTechnicalSpec(s) }

// The two recovery messages below. Both are deliberately TRUE without knowing
// anything about the conversation — neither summarises or invents a plan.
const (
	// specOnlyFallback covers a reply that was ENTIRELY the machine-facing block.
	// The model did answer; it just wrote nothing for the human. A spec block
	// exists, so PlanReady is true and the Spec view has something real to show —
	// which is exactly what this points at.
	specOnlyFallback = "The plan is ready — open **View spec** to see the details, then type approve and I'll build it."
	// emptyReplyFallback covers a reply with no content at all. Nothing can be
	// claimed about a plan here, so it asks rather than asserts.
	emptyReplyFallback = "Sorry — I didn't manage to put that into words. Could you say that again?"
)

// UserFacingDesignText turns one raw designer reply into the text the user sees.
//
// It exists because stripping alone can empty a turn: when the model's whole
// reply IS the [TECHNICAL SPEC] block — which happens when a small correction
// lands on an already-settled plan and it re-emits just the updated block — the
// strip leaves "" and the browser renders a BLANK BUBBLE. That is a reported
// bug, and it was introduced by moving spec emission onto the proposal turn:
// before that the block was never written at all, and a stray one would have
// rendered as ugly text rather than as nothing.
//
// A blank turn is the worst available outcome. It reads as the assistant
// ignoring you, gives no way forward, and (because History stores the raw text)
// survives a reload. So an empty result is always replaced, distinguishing the
// two causes because they need opposite responses: a spec-only reply means the
// plan IS ready and should point at it, while a genuinely empty reply means
// nothing was said and must ask again rather than claim progress.
func UserFacingDesignText(raw string) string {
	shown := stripTechnicalSpec(raw)
	if shown != "" {
		return shown
	}
	if extractTechnicalSpec(raw) != "" {
		return specOnlyFallback
	}
	return emptyReplyFallback
}

func stripTechnicalSpec(s string) string {
	out := stripBlock(s, specOpen, specClose)
	for strings.Contains(out, "\n\n\n") {
		out = strings.ReplaceAll(out, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(out)
}

// planFromHistory derives the plan-ready signal from a conversation.
//
// It reads the LAST assistant turn only, never "any turn that ever carried a
// block". That is what lets the signal RETRACT: a user who answers a settled
// proposal with "actually, make it hourly" gets a fresh assistant turn, and if
// that turn is another question it carries no block, so the button withdraws. A
// latch-once-true flag would be a worse defect than the one this replaces.
//
// roleNote turns are skipped. They are coder-facing steering recorded after a
// failed build, never a proposal, and folding them in would let a failure note
// mask the real last proposal.
func planFromHistory(history []db.ChatMessage) (spec string, ready bool) {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role != "assistant" {
			continue
		}
		spec = extractTechnicalSpec(history[i].Content)
		return spec, spec != ""
	}
	return "", false
}
