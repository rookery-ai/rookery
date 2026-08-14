package convert

import (
	"strings"
	"testing"
)

// The anchor guard used to be a literal, case-sensitive HasPrefix on
// "javascript:". HTML attribute values reach here already entity-decoded by
// x/net/html, and browsers ignore ASCII whitespace and C0 controls anywhere in
// a URL — including inside the scheme — so every one of these is a live link
// that the old check waved through into a note.
func TestDangerousLinkSchemesAreStripped(t *testing.T) {
	cases := []struct {
		name string
		href string
	}{
		{"plain", "javascript:alert(1)"},
		{"uppercase", "JAVASCRIPT:alert(1)"},
		{"mixed case", "JaVaScRiPt:alert(1)"},
		{"leading space", "  javascript:alert(1)"},
		{"leading newline", "\njavascript:alert(1)"},
		{"tab inside the scheme", "java\tscript:alert(1)"},
		{"newline inside the scheme", "java\nscript:alert(1)"},
		{"vbscript", "vbscript:msgbox(1)"},
		{"data html", "data:text/html;base64,PHNjcmlwdD4="},
		{"data svg", "data:image/svg+xml,<svg onload=alert(1)>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mdOf(t, `<a href="`+attrEscape(tc.href)+`">click</a>`)
			if strings.Contains(got, "](") {
				t.Fatalf("dangerous href survived as a link: %q", got)
			}
			// The text is still worth keeping — only the destination is dropped.
			if !strings.Contains(got, "click") {
				t.Fatalf("link text was lost: %q", got)
			}
		})
	}
}

// A NUL inside the scheme ("java\x00script:") is deliberately NOT in the table
// above, and its absence is the finding: x/net/html substitutes U+FFFD for NUL
// while parsing, so what reaches the writer is "java�script:" — an
// unknown scheme no browser executes. The parser defuses this one before the
// scheme check ever sees it, and stripping U+FFFD here would only ever cause
// false blocking, never close a hole.
func TestOrdinaryLinkSchemesSurvive(t *testing.T) {
	for _, href := range []string{
		"https://example.com/a?b=1#c",
		"http://example.com",
		"mailto:someone@example.com",
		"/relative/path",
		"#anchor",
		"ftp://files.example.com/x",
		// A colon inside a path segment is not a scheme.
		"/a:b/c",
	} {
		got := mdOf(t, `<a href="`+attrEscape(href)+`">click</a>`)
		if !strings.Contains(got, "]("+href+")") {
			t.Fatalf("href %q should have survived, got %q", href, got)
		}
	}
}

// An <img src> carries the same schemes and had no check at all. Inline
// raster data URIs are legitimate and common in scraped HTML, so those stay;
// SVG does not, because an SVG payload can carry script.
func TestImageSourceSchemes(t *testing.T) {
	blocked := []string{
		"javascript:alert(1)",
		"vbscript:msgbox(1)",
		"data:text/html,<script>alert(1)</script>",
		"data:image/svg+xml,<svg onload=alert(1)>",
	}
	for _, src := range blocked {
		got := mdOf(t, `<img alt="pic" src="`+attrEscape(src)+`">`)
		if strings.Contains(got, "](") {
			t.Fatalf("dangerous img src survived: %q", got)
		}
	}
	allowed := []string{
		"https://example.com/a.png",
		"/local/a.png",
		"data:image/png;base64,iVBORw0KGgo=",
	}
	for _, src := range allowed {
		got := mdOf(t, `<img alt="pic" src="`+attrEscape(src)+`">`)
		if !strings.Contains(got, "]("+src+")") {
			t.Fatalf("img src %q should have survived, got %q", src, got)
		}
	}
}

func attrEscape(s string) string {
	return strings.ReplaceAll(s, `"`, "&quot;")
}

func mdOf(t *testing.T, in string) string {
	t.Helper()
	res, err := ToMarkdown([]byte(in), Options{Filename: "x.html"})
	if err != nil {
		t.Fatal(err)
	}
	return res.Markdown
}
