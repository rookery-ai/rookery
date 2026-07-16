package render

import "testing"

func TestRenderSlack(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"bold single star", "**bold**", "*bold*"},
		{"italic", "_italic_", "_italic_"},
		{"code span", "run `x.y()`", "run `x.y()`"},
		{"link becomes angle form", "[docs](https://x.io/a)", "<https://x.io/a|docs>"},
		{"html-escape angle+amp in text", "a < b & c > d", "a &lt; b &amp; c &gt; d"},
		{"plain punctuation not escaped", "Done. Ready!", "Done. Ready!"},
		{"bullet list", "- one\n- two", "• one\n• two"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RenderSlack(tc.in); got != tc.want {
				t.Fatalf("RenderSlack(%q)\n got: %q\nwant: %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRenderSlackRegistered(t *testing.T) {
	if got := For("slack").Render("**b**"); got != "*b*" {
		t.Fatalf("slack renderer not registered, got %q", got)
	}
}
