package prompts

import "testing"

func TestStripProtocolMarkersKeepsWhatTheyWrapped(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			// The reported bug: the KB rewrite panel showing the marker the API
			// engine's kickoff used to ask for.
			name: "wrapped passage",
			in:   "[CHAT]\nThe pipeline runs on every merge to main.\n[/CHAT]",
			want: "The pipeline runs on every merge to main.",
		},
		{
			// Weak models emit a stray close tag with no opener; agentrunner
			// already had to learn this.
			name: "unpaired close tag",
			in:   "The pipeline runs on merge.\n[/CHAT]",
			want: "The pipeline runs on merge.",
		},
		{
			name: "inline marker",
			in:   "[CHAT] The pipeline runs on merge.",
			want: "The pipeline runs on merge.",
		},
		{
			name: "bare silent line",
			in:   "The pipeline runs on merge.\n[SILENT]",
			want: "The pipeline runs on merge.",
		},
		{
			name: "state block",
			in:   "Rewritten text.\n[STATE]{\"a\":1}[/STATE]",
			want: "Rewritten text.\n{\"a\":1}",
		},
		{
			// The property that matters most. A strip that rewrites innocent prose
			// is worse than the leak it was added to catch — the endpoint returns a
			// passage the user is about to paste over their own writing.
			name: "clean prose is untouched",
			in:   "The pipeline runs on merge.\n\nIt is gated on review.",
			want: "The pipeline runs on merge.\n\nIt is gated on review.",
		},
		{
			name: "markdown with brackets survives",
			in:   "See [the docs](https://example.com) and the `[key]` field.",
			want: "See [the docs](https://example.com) and the `[key]` field.",
		},
		{name: "empty", in: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StripProtocolMarkers(tt.in); got != tt.want {
				t.Errorf("StripProtocolMarkers(%q)\n got %q\nwant %q", tt.in, got, tt.want)
			}
		})
	}
}
