package coder

import (
	"testing"

	"github.com/rookery-ai/rookery/internal/browser"
)

// A run's step list is what an owner reads to see what their agent did, and
// "browser_fill({"ref": "f16e211", "value": "${CARD_NUMBER}"})" tells them
// nothing: the ref is an internal handle. The element's name is already known
// from the page listing the agent acted on, so the step names it.
func TestBrowserMilestoneNamesTheElement(t *testing.T) {
	h := &hostToolSet{}
	h.rememberRefs([]browser.Element{
		{Ref: "f16e211", Role: "textbox", Name: "Credit Card Number"},
		{Ref: "f16e223", Role: "button", Name: "Confirm"},
	})
	cases := map[string]struct{ name, args, want string }{
		"fill a secret": {"browser_fill", `{"ref":"f16e211","value":"${CARD_NUMBER}"}`,
			`🔧 browser_fill "Credit Card Number" ← ${CARD_NUMBER}`},
		"click": {"browser_click", `{"ref":"f16e223"}`, `🔧 browser_click "Confirm"`},
	}
	for label, c := range cases {
		if got := h.browserMilestone(toolCall(c.name, c.args)); got != c.want {
			t.Errorf("%s: got %q, want %q", label, got, c.want)
		}
	}
}

// When the name is not known the caller falls back to the generic line rather
// than guessing — a wrong name in a run log is worse than a ref.
func TestBrowserMilestoneFallsBackWhenUnknown(t *testing.T) {
	h := &hostToolSet{}
	h.rememberRefs([]browser.Element{{Ref: "e1", Name: "Search"}})
	for _, tc := range []struct{ name, args string }{
		{"browser_click", `{"ref":"e99"}`},
		{"browser_open", `{"url":"https://example.com"}`},
		{"read_file", `{"path":"a.md"}`},
	} {
		if got := h.browserMilestone(toolCall(tc.name, tc.args)); got != "" {
			t.Errorf("%s with unknown/irrelevant args produced %q, want fallback", tc.name, got)
		}
	}
}
