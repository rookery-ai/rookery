package convert

import (
	"strings"
	"testing"
)

// The <img> case used to be wrapped in `if alt := …; alt != ""`, so an image
// with no alt attribute emitted NOTHING — the src was discarded with it and the
// image simply vanished from the note, with no warning to say so. Empty or
// absent alt is the common case in real web pages and HTML mail, so in practice
// this stripped most imported images.
func TestHTMLImageWithoutAltIsStillEmitted(t *testing.T) {
	cases := []struct{ name, html, want string }{
		{"no alt attribute", `<img src="uploads/a.png">`, `![](uploads/a.png)`},
		{"empty alt", `<img src="uploads/a.png" alt="">`, `![](uploads/a.png)`},
		{"whitespace alt", `<img src="uploads/a.png" alt="  ">`, `![](uploads/a.png)`},
		{"with alt", `<img src="uploads/a.png" alt="Chart">`, `![Chart](uploads/a.png)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := ToMarkdown([]byte("<html><body>"+tc.html+"</body></html>"),
				Options{Filename: "page.html"})
			if err != nil {
				t.Fatalf("ToMarkdown: %v", err)
			}
			if !strings.Contains(res.Markdown, tc.want) {
				t.Errorf("got %q, want it to contain %q", res.Markdown, tc.want)
			}
		})
	}
}

// A destination is a path, not prose. A space ends it — turning the whole
// construct back into literal text, so the image stops being an image — and an
// unescaped ")" ends it early, truncating the path.
func TestHTMLImageDestinationIsEscaped(t *testing.T) {
	res, err := ToMarkdown(
		[]byte(`<html><body><img src="uploads/my file(1).png" alt="Chart"></body></html>`),
		Options{Filename: "page.html"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	const want = `![Chart](uploads/my%20file\(1\).png)`
	if !strings.Contains(res.Markdown, want) {
		t.Errorf("got %q, want it to contain %q", res.Markdown, want)
	}
}

// A blocked source still has nothing safe to link to, so the alt text is kept
// as prose — but with no alt there is nothing to say, and emitting an empty
// image reference to a source we refused would be worse than silence.
func TestHTMLImageWithBlockedSource(t *testing.T) {
	cases := []struct{ name, html, want, notWant string }{
		{
			name:    "blocked src keeps alt as prose",
			html:    `<img src="javascript:alert(1)" alt="Chart">`,
			want:    "Chart",
			notWant: "![",
		},
		{
			name:    "blocked src with no alt emits nothing",
			html:    `<img src="javascript:alert(1)">`,
			notWant: "javascript:",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := ToMarkdown([]byte("<html><body><p>Before</p>"+tc.html+"</body></html>"),
				Options{Filename: "page.html"})
			if err != nil {
				t.Fatalf("ToMarkdown: %v", err)
			}
			if tc.want != "" && !strings.Contains(res.Markdown, tc.want) {
				t.Errorf("got %q, want it to contain %q", res.Markdown, tc.want)
			}
			if tc.notWant != "" && strings.Contains(res.Markdown, tc.notWant) {
				t.Errorf("got %q, want it NOT to contain %q", res.Markdown, tc.notWant)
			}
		})
	}
}
