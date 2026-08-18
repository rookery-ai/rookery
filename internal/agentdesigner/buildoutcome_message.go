package agentdesigner

import "fmt"

// reviewMessage wraps the review sample shown at the end of a build.
//
// `executed` is the whole point. The sample has three possible origins — a
// [TEST_OUTPUT] marker, a verified script's captured stdout, or (when neither
// exists) a preview of the model's own prose — and only the first two are evidence
// that anything ran. A TIER 1 agent has no script at all, which is the correct tier
// for "call an API, compare, notify" and therefore the common case, so the prose
// branch is reached often.
//
// Presenting that prose as "here's what a test run produces" is the review step
// lying about the one thing the user is there to check. When nothing ran, say so.
func reviewMessage(sample string, executed bool) string {
	if executed {
		return fmt.Sprintf(
			"Here's what a test run produces:\n\n---\n%s\n---\n\nDoes this look right? Type **approve** to save the agent, or tell me what to change.",
			sample,
		)
	}
	return fmt.Sprintf(
		"I built the assistant and it passed the safety checks, but it didn't run — so this is its own description of what it will do, not real output:\n\n---\n%s\n---\n\nPlease look it over. Type **approve** to save it, or tell me what to change.",
		sample,
	)
}
