package agentdesigner

import (
	"strings"
	"testing"
)

// The reported bug, at the layer that caused it.
//
// Moving [TECHNICAL SPEC] emission onto the proposal turn (so the code
// generator finally receives it) created a case that could not happen before:
// the model answers a small correction by re-emitting ONLY the updated block.
// Stripping that for display leaves "", and the browser rendered a blank
// assistant bubble — which reads as the assistant ignoring you, offers no way
// forward, and (because History stores the raw text) came back on every reload.
func TestUserFacingDesignTextNeverReturnsEmpty(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"whole reply is the spec block", specOpen + "\nTier: 1\n" + specClose},
		{"spec block with surrounding blank lines", "\n\n" + specOpen + "\nTier: 1\n" + specClose + "\n\n"},
		{"unterminated spec block only", specOpen + "\nTier: 1\nSched"},
		{"model returned nothing at all", ""},
		{"model returned only whitespace", "   \n\n\t"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := UserFacingDesignText(tt.raw)
			if strings.TrimSpace(got) == "" {
				t.Fatal("returned an empty turn — this is the blank bubble")
			}
		})
	}
}

// The two causes need opposite responses, so they must not share a message: a
// spec-only reply means the plan IS settled and should point at it, while a
// genuinely empty reply means nothing was said and must ask again rather than
// claim progress that did not happen.
func TestUserFacingDesignTextDistinguishesTheTwoFailures(t *testing.T) {
	specOnly := UserFacingDesignText(specOpen + "\nTier: 1\n" + specClose)
	if specOnly != specOnlyFallback {
		t.Errorf("spec-only reply got %q, want the plan-is-ready message", specOnly)
	}
	// It must point somewhere actionable rather than merely apologising.
	if !strings.Contains(specOnly, "View spec") || !strings.Contains(specOnly, "approve") {
		t.Errorf("the plan-is-ready message names no next step: %q", specOnly)
	}

	nothing := UserFacingDesignText("")
	if nothing != emptyReplyFallback {
		t.Errorf("empty reply got %q, want the ask-again message", nothing)
	}
	// Claiming a plan exists when the model said nothing would be a lie the user
	// cannot check.
	if strings.Contains(nothing, "View spec") {
		t.Errorf("the empty-reply message claims a plan is ready: %q", nothing)
	}
}

// Ordinary replies must be untouched — the fallback is a last resort, not a
// rewrite. A helper that "improved" real prose would be worse than the bug.
func TestUserFacingDesignTextLeavesRealProseAlone(t *testing.T) {
	raw := "Here's the plan.\n\n- Watch your notes\n- Stay silent\n\n" +
		specOpen + "\nTier: 1\n" + specClose
	got := UserFacingDesignText(raw)
	want := "Here's the plan.\n\n- Watch your notes\n- Stay silent"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	for _, s := range []string{specOnlyFallback, emptyReplyFallback} {
		if strings.Contains(got, s) {
			t.Error("a fallback leaked into a reply that had real prose")
		}
	}
}
