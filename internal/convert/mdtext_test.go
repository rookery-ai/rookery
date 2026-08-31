package convert

import "testing"

// The expectations here are not taste. Every case was measured by driving the
// real KB editor (web/ui/src/pages/kb/editor.ts checkFidelity) over the input
// and recording the form it round-trips to. A note whose body does not survive
// that round trip opens READ-ONLY and cannot be edited at all, which is the
// user-visible failure this escaper exists to prevent.
//
// The negative cases matter as much as the positive ones: over-escaping is its
// own regression, and each character below was verified to round-trip cleanly
// UNESCAPED. Escaping "_" would corrupt every snake_case identifier in an
// imported document; escaping "&" is actively harmful, because "&amp;" itself
// round-trips back to a bare "&".
func TestEscapeInline(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// ── must escape ──
		{"less than", "a < b", "a &lt; b"},
		{"greater than", "Growth > 10", "Growth &gt; 10"},
		{"open bracket", "see [12]", `see \[12\]`},
		{"footnote marker", "note[^1]", `note\[^1\]`},
		{"asterisk", "5* higher", `5\* higher`},
		{"backtick", "the ` key", "the \\` key"},
		{"tilde", "~50 items", `\~50 items`},
		{"backslash", `C:\Users`, `C:\\Users`},

		// ── must NOT escape (measured safe; escaping these corrupts prose) ──
		{"ampersand", "Tom & Jerry", "Tom & Jerry"},
		{"underscore", "user_name_field", "user_name_field"},
		{"pipe", "a | b", "a | b"},
		{"hash", "Ticket #42", "Ticket #42"},
		{"plus", "+15 percent", "+15 percent"},
		{"parentheses", "a (b) c", "a (b) c"},
		{"hyphen mid-text", "well-known", "well-known"},
		{"accented character", "Café", "Café"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EscapeInline(tc.in); got != tc.want {
				t.Errorf("EscapeInline(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// A leading "-" opens a bullet list, so a paragraph that genuinely starts with
// one ("-40 degrees was recorded") is re-parsed as a list and the note opens
// read-only. It is escaped ONLY at the start of a block: mid-sentence a hyphen
// is ordinary punctuation, and escaping it there would litter every hyphenated
// word with backslashes.
func TestEscapeLeadingMarker(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"leading hyphen digit", "-40 degrees", `\-40 degrees`},
		{"leading hyphen word", "-forty degrees", `\-forty degrees`},
		{"real bullet is left alone", "- an item", "- an item"},
		{"hyphen mid-text untouched", "a -40 reading", "a -40 reading"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := escapeLeadingMarker(tc.in); got != tc.want {
				t.Errorf("escapeLeadingMarker(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// A table cell has the inline rules PLUS its own: a literal pipe would split
// the cell and a newline would break the row. The pipe is escaped here and
// deliberately not in EscapeInline, because a bare pipe in ordinary prose was
// measured to round-trip cleanly and escaping it there would be noise.
func TestEscapeCell(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"pipe", "x | y", `x \| y`},
		{"angle bracket", "a < b", "a &lt; b"},
		{"bracket", "[12]", `\[12\]`},
		{"newline becomes a space", "a\nb", "a b"},
		{"carriage return becomes a space", "a\r\nb", "a b"},
		{"trimmed", "  padded  ", "padded"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := escapeCell(tc.in); got != tc.want {
				t.Errorf("escapeCell(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// The destination half of an image or link is not prose and must not be
// inline-escaped — "&lt;" in a path is a broken path. It needs exactly the two
// rules the editor's own serializer applies (kbImage.ts escapes a backslash and
// both parens) plus a space encoding, because a space ends the destination and
// turns the whole construct back into literal text.
func TestEscapeDestination(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"parens", "uploads/a(1).png", `uploads/a\(1\).png`},
		{"space", "uploads/my file.png", "uploads/my%20file.png"},
		{"backslash", `uploads\a.png`, `uploads\\a.png`},
		{"plain", "uploads/a.png", "uploads/a.png"},
		{"url untouched", "https://x.com/a", "https://x.com/a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := escapeDestination(tc.in); got != tc.want {
				t.Errorf("escapeDestination(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
