package render

import "testing"

func TestRenderTelegram(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain with dot and bang", "Hello world. Done!", "Hello world\\. Done\\!"},
		{"bold", "**bold**", "*bold*"},
		{"italic", "_italic_", "_italic_"},
		{"inline code not escaped inside", "run `a.b()!` now.", "run `a.b()!` now\\."},
		{"link", "[docs](https://x.io/a_b)", "[docs](https://x.io/a_b)"},
		{"hyphen and paren in text", "a-b (c)", "a\\-b \\(c\\)"},
		{"plus and equals", "1 + 1 = 2", "1 \\+ 1 \\= 2"},
		// Angle-bracket placeholders: goldmark parses <name> as raw inline HTML
		// (valid tag name) and <agent_name> as literal text (underscore is not a
		// valid tag char). Both must survive; > is a MarkdownV2 special and IS escaped, < is not.
		{"angle placeholder (parses as raw HTML)", "Use /run <name> now.", "Use /run <name\\> now\\."},
		{"angle placeholder with underscore (literal text)", "Usage: <agent_name>", "Usage: <agent\\_name\\>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RenderTelegram(tc.in); got != tc.want {
				t.Fatalf("RenderTelegram(%q)\n got: %q\nwant: %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRenderTelegramRegistered(t *testing.T) {
	if got := For("telegram").Render("a."); got != "a\\." {
		t.Fatalf("telegram renderer not registered/used, got %q", got)
	}
}
