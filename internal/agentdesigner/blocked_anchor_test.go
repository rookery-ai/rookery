package agentdesigner

import "testing"

// A model that writes ABOUT the marker ("whether a `[BLOCKED]` block is warranted")
// must not be read as having emitted it. Same rule as skilldesigner's copy.
func TestParseBlockedOutputIsLineAnchored(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"real block", "[BLOCKED]\nno API key\n[/BLOCKED]", "no API key"},
		{"inline reason", "work done\n[BLOCKED] cannot reach host", "cannot reach host"},
		{"glued block", "[BLOCKED]I could not reach it.[/BLOCKED]", "I could not reach it."},
		{"mention mid-sentence", "I considered whether a `[BLOCKED]` block is warranted: it is not.\n[TEST_OUTPUT]\nok\n[/TEST_OUTPUT]", ""},
		{"decorated mention at line start", "`[BLOCKED]` is not needed here", ""},
		{"bold mention", "**[BLOCKED]** would be wrong", ""},
		{"no marker", "all good", ""},
	}
	for _, c := range cases {
		if got := parseBlockedOutput(c.in); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}
