package convert

import (
	"strings"
	"testing"
)

func convertHTML(t *testing.T, body string) string {
	t.Helper()
	res, err := ToMarkdown([]byte("<html><body>"+body+"</body></html>"),
		Options{Filename: "page.html"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	return res.Markdown
}

// <ul> and <ol> had NO case at all — only <li> was handled, always as "- ". So
// a numbered procedure imported as an unnumbered list and a nested list came
// out flat, both irrecoverably.
func TestHTMLOrderedAndNestedLists(t *testing.T) {
	cases := []struct {
		name, in string
		want     []string
	}{
		{
			name: "ordered list is numbered",
			in:   "<ol><li>First</li><li>Second</li><li>Third</li></ol>",
			want: []string{"1. First", "2. Second", "3. Third"},
		},
		{
			name: "unordered list keeps bullets",
			in:   "<ul><li>One</li><li>Two</li></ul>",
			want: []string{"- One", "- Two"},
		},
		{
			name: "nested list is indented",
			in:   "<ul><li>Top<ul><li>Inner</li></ul></li></ul>",
			want: []string{"- Top", "  - Inner"},
		},
		{
			name: "ordered nested in unordered",
			in:   "<ul><li>Top<ol><li>Step</li></ol></li></ul>",
			want: []string{"- Top", "  1. Step"},
		},
		{
			name: "two sibling ordered lists each restart",
			in:   "<ol><li>A</li></ol><p>Break</p><ol><li>B</li></ol>",
			want: []string{"1. A", "1. B"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := convertHTML(t, tc.in)
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in:\n%s", w, got)
				}
			}
		})
	}
}

// A list's items are ONE block. Separating them with blank lines makes the list
// loose, and the editor's serializer writes it tight — a difference that opens
// the note read-only.
func TestHTMLListItemsAreTight(t *testing.T) {
	got := convertHTML(t, "<ul><li>One</li><li>Two</li></ul>")
	if !strings.Contains(got, "- One\n- Two") {
		t.Errorf("list items are not tight:\n%q", got)
	}
}

// Blockquote used to share a case with <p> and emit no ">" at all, so a
// quotation was indistinguishable from the prose around it.
func TestHTMLBlockquote(t *testing.T) {
	got := convertHTML(t, "<blockquote><p>A claim.</p><p>And another.</p></blockquote>")
	if !strings.Contains(got, "> A claim.") {
		t.Errorf("no quote marker in:\n%s", got)
	}
	// Paragraphs inside a quote are separated by a blank QUOTED line, and each
	// paragraph is one line: a multi-line quote round-trips as a joined line, so
	// emitting the source's own line breaks would open the note read-only.
	if !strings.Contains(got, "> A claim.\n>\n> And another.") {
		t.Errorf("quoted paragraphs are not separated correctly:\n%q", got)
	}
}

// The canonical toggle has <details> and <summary> on SEPARATE lines with a
// blank line before the body. The glued spelling parses the same but is
// explicitly not a fixed point, so emitting it would open the note read-only.
func TestHTMLDetailsBecomesAToggle(t *testing.T) {
	got := convertHTML(t, "<details><summary>Show</summary><p>Body.</p></details>")
	const want = "<details>\n<summary>Show</summary>\n\nBody.\n\n</details>"
	if !strings.Contains(got, want) {
		t.Errorf("got:\n%q\nwant it to contain:\n%q", got, want)
	}
}

func TestHTMLDetailsWithoutSummary(t *testing.T) {
	got := convertHTML(t, "<details><p>Body.</p></details>")
	if !strings.Contains(got, "<summary>Details</summary>") {
		t.Errorf("no fallback summary in:\n%s", got)
	}
}

// Alignment is read from BOTH spellings, because real documents use both — but
// only the attribute form is emitted, since that is the one the editor
// serializes and therefore the only fixed point.
func TestHTMLAlignment(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"align attribute", `<div align="center"><p>Hi</p></div>`, `<div align="center">`},
		{"style property", `<div style="text-align: right"><p>Hi</p></div>`, `<div align="right">`},
		{"uppercase value", `<div align="CENTER"><p>Hi</p></div>`, `<div align="center">`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := convertHTML(t, tc.in)
			if !strings.Contains(got, tc.want) {
				t.Errorf("missing %q in:\n%s", tc.want, got)
			}
			if !strings.Contains(got, "</div>") {
				t.Errorf("wrapper not closed in:\n%s", got)
			}
		})
	}
}

// A div carrying data-cols belongs to the columns node, which owns that
// attribute. Claiming it for alignment would produce a wrapper with both, and
// the editor drops one of them.
func TestHTMLAlignmentDeclinesAColumnsDiv(t *testing.T) {
	got := convertHTML(t, `<div data-cols="2" align="center"><p>Hi</p></div>`)
	if strings.Contains(got, `align="center"`) {
		t.Errorf("claimed a columns div for alignment:\n%s", got)
	}
}

func TestHTMLUnderline(t *testing.T) {
	got := convertHTML(t, "<p>Some <u>underlined</u> words.</p>")
	if !strings.Contains(got, "<u>underlined</u>") {
		t.Errorf("underline lost in:\n%s", got)
	}
}

// The exact spelling is not a preference: no space after the colon, lowercase
// hex, and a highlight pins its own foreground. Any other spelling is rewritten
// by the editor on first save.
func TestHTMLColourMarks(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{
			name: "foreground normalises to lowercase",
			in:   `<p>A <span style="color: #EF4444">red</span> word.</p>`,
			want: `<span style="color:#ef4444">red</span>`,
		},
		{
			name: "highlight pins a foreground",
			in:   `<p>A <span style="background-color:#fef08a">hot</span> word.</p>`,
			want: `<span style="background-color:#fef08a;color:#18181b">hot</span>`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := convertHTML(t, tc.in)
			if !strings.Contains(got, tc.want) {
				t.Errorf("got:\n%s\nwant it to contain:\n%s", got, tc.want)
			}
		})
	}
}

// A span with no colour, or a colour the editor cannot store, must fall through
// to its plain contents rather than emit a mark that gets rewritten.
func TestHTMLSpanWithoutAStorableColour(t *testing.T) {
	for _, in := range []string{
		`<p>A <span>plain</span> word.</p>`,
		`<p>A <span style="font-weight:bold">styled</span> word.</p>`,
		`<p>A <span style="color: red">named</span> word.</p>`,
		`<p>A <span style="color: rgb(1,2,3)">rgb</span> word.</p>`,
	} {
		got := convertHTML(t, in)
		if strings.Contains(got, "<span") {
			t.Errorf("emitted a span for %q:\n%s", in, got)
		}
	}
}

// The info string was dropped, so an imported code block lost its language and
// its highlighting for good.
func TestHTMLCodeFenceLanguage(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"language- prefix", `<pre><code class="language-go">x := 1</code></pre>`, "```go\n"},
		{"lang- prefix", `<pre><code class="lang-python">x = 1</code></pre>`, "```python\n"},
		{"no class", `<pre><code>plain</code></pre>`, "```\n"},
		{"unrelated class", `<pre><code class="highlight">plain</code></pre>`, "```\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := convertHTML(t, tc.in)
			if !strings.Contains(got, tc.want) {
				t.Errorf("got:\n%q\nwant it to contain %q", got, tc.want)
			}
		})
	}
}
