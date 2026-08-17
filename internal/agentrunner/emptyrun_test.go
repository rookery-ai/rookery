package agentrunner

import "testing"

// TestCoderProducedNothing separates two outcomes the runner used to conflate,
// and reported as the same cheerful exit 0.
//
// A run whose coder returns ZERO bytes did not "produce no notification" — it did
// not run. Nothing was fetched, no state was written, and the agent's whole job
// was skipped. Reporting that as a successful-but-quiet run is what produced a
// real user's twice-daily "⚠️ Ran but produced no notification" while state.md
// stayed at {} and nothing in the server log explained why.
//
// The distinction has to be drawn on RAW output, not on the parsed result: a
// forgotten [CHAT] marker still leaves prose behind to deliver, whereas an empty
// response leaves nothing to parse at all.
func TestCoderProducedNothing(t *testing.T) {
	cases := []struct {
		name      string
		chatLines []string
		rawChunks []string
		silent    bool
		wantEmpty bool
	}{
		{
			name:      "coder returned literally nothing",
			rawChunks: nil,
			wantEmpty: true,
		},
		{
			name:      "coder returned only whitespace",
			rawChunks: []string{"", "  \n\t ", "\n"},
			wantEmpty: true,
		},
		{
			name:      "forgot the marker but wrote prose — recoverable, not empty",
			rawChunks: []string{"No new domains overnight."},
			wantEmpty: false,
		},
		{
			name:      "delivered real chat content",
			chatLines: []string{"3 new domains"},
			rawChunks: []string{"[CHAT] 3 new domains"},
			wantEmpty: false,
		},
		{
			name:      "intentionally silent runs are NOT failures",
			rawChunks: []string{"[SILENT]"},
			silent:    true,
			wantEmpty: false,
		},
		{
			// The important boundary: a silent run whose only output WAS the marker.
			// The marker is meaningful output — the agent decided and said so.
			name:      "silent with no other output is still not a failure",
			rawChunks: []string{"  [SILENT]  "},
			silent:    true,
			wantEmpty: false,
		},
		{
			// State was written but the model said nothing else. It ran; it simply
			// forgot to speak. That is the prose/warning path, not a dead run.
			name:      "state-only output is not empty",
			rawChunks: []string{"[STATE]{\"seen\":[\"a.com\"]}[/STATE]"},
			wantEmpty: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := coderProducedNothing(tc.chatLines, tc.rawChunks, tc.silent)
			if got != tc.wantEmpty {
				t.Fatalf("coderProducedNothing() = %v, want %v", got, tc.wantEmpty)
			}
		})
	}
}
