package agentdesigner

import "testing"

// TestIsDesignQuestion pins the asymmetry that makes the post-failure rebuild branch safe:
// only a genuine question escapes to the design chat, and everything ambiguous rebuilds.
//
// The bug this guards against: after a failed build the user typed a change request, it was
// routed to a design-chat turn, and the designer re-interrogated them for details the
// conversation had already settled — forcing a manual "Build it" click to apply a change they
// had already described. Any input misclassified as a question re-opens that trap.
func TestIsDesignQuestion(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		// ── The real transcript. These MUST rebuild. ──────────────────────────────
		{
			// The message that regressed: a plain directive, no question mark. It was
			// routed to chat, which then asked for a URL given three times already.
			name:  "reported regression: no-script directive",
			input: "Dont build python script you can fetch the pages and figure out which are on discount",
			want:  false,
		},
		{
			name:  "reported transcript: test request",
			input: "Test if it can output real data",
			want:  false,
		},
		{
			name:  "reported transcript: test the built agent",
			input: "Test the agent that youwe build",
			want:  false,
		},

		// ── Genuine questions. These route to chat. ───────────────────────────────
		{name: "why did it fail", input: "why did that fail?", want: true},
		{name: "what went wrong", input: "What went wrong?", want: true},
		{name: "how does it work", input: "how does the state tracking work?", want: true},
		{name: "explain request", input: "Can you explain what happened?", want: true},
		{name: "which page", input: "which page did it end up fetching?", want: true},
		{name: "did it run", input: "did it actually run the script?", want: true},

		// ── Question-shaped but directive. These rebuild: the "?" is politeness, the
		//    imperative cue is the real content. Treating them as questions would answer
		//    a request instead of acting on it — the reported bug in miniature. ────────
		{name: "polite change request", input: "can you use web_fetch instead?", want: false},
		{name: "polite no-script request", input: "why don't you just fetch the page?", want: false},
		{name: "change with question mark", input: "what if you change it to run hourly?", want: false},
		{name: "add with question mark", input: "how about you add the price too?", want: false},
		{name: "remove request", input: "could you remove the script?", want: false},

		// ── Statements without a question mark always rebuild. ────────────────────
		{name: "bare statement", input: "the discount calculation is wrong", want: false},
		{name: "imperative", input: "use the sorted listing page", want: false},
		{name: "empty", input: "", want: false},
		{name: "whitespace", input: "   ", want: false},

		// ── Non-interrogative opener + "?" rebuilds: fail toward acting. ──────────
		{
			name:  "statement ending in a question mark",
			input: "the page you fetched was the wrong one?",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isDesignQuestion(tt.input); got != tt.want {
				verb := "rebuild"
				if tt.want {
					verb = "route to chat"
				}
				t.Errorf("isDesignQuestion(%q) = %v, want %v — this input should %s",
					tt.input, got, tt.want, verb)
			}
		})
	}
}

// TestIsDesignQuestionDoesNotShadowRetryPhrases guards the branch ORDER in stepDesigning:
// isRetryApproval is checked first, so a bare retry never reaches isDesignQuestion. If a
// retry phrase were also classified as a question, reordering the branches later would
// silently break the retry path.
func TestIsDesignQuestionDoesNotShadowRetryPhrases(t *testing.T) {
	for _, in := range []string{"try again", "keep going", "fix it", "retry?"} {
		if isDesignQuestion(in) {
			t.Errorf("retry phrase %q must never classify as a question", in)
		}
	}
}
