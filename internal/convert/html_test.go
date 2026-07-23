package convert

import "strings"

import "testing"

func TestHTMLToMarkdown(t *testing.T) {
	doc := `<!DOCTYPE html><html><head><title>Q3 Report</title>
	<style>.x{color:red}</style><script>var a=1;</script></head>
	<body>
	  <nav>Home About Contact</nav>
	  <header>Site banner</header>
	  <main>
	    <h1>Revenue</h1>
	    <p>Revenue grew by <strong>12%</strong> this quarter.</p>
	    <ul><li>EMEA up</li><li>APAC flat</li></ul>
	    <a href="https://example.com/detail">Full detail</a>
	  </main>
	  <footer>Copyright 2026</footer>
	</body></html>`

	got, err := ToMarkdown([]byte(doc), Options{})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if got.Kind != KindHTML {
		t.Errorf("Kind = %q, want html", got.Kind)
	}
	// Pins the real converter in place of Task 1's compile-time stub, which
	// also set Kind/Markdown but tagged Extractor "stub" — a regression back to
	// it would pass every other assertion here and go unnoticed.
	if got.Extractor != "pure-go" {
		t.Errorf("Extractor = %q, want %q (stub must not come back)", got.Extractor, "pure-go")
	}
	if got.Title != "Q3 Report" {
		t.Errorf("Title = %q, want %q", got.Title, "Q3 Report")
	}
	for _, want := range []string{
		"# Revenue",
		"**12%**",
		"- EMEA up",
		"- APAC flat",
		"[Full detail](https://example.com/detail)",
	} {
		if !strings.Contains(got.Markdown, want) {
			t.Errorf("markdown missing %q, got:\n%s", want, got.Markdown)
		}
	}
	// <main> is present, so chrome outside it is dropped entirely.
	for _, unwanted := range []string{"Home About Contact", "Site banner", "Copyright 2026", "var a=1", "color:red"} {
		if strings.Contains(got.Markdown, unwanted) {
			t.Errorf("markdown should not contain %q, got:\n%s", unwanted, got.Markdown)
		}
	}
}

func TestHTMLToMarkdownWithoutMain(t *testing.T) {
	// No <main>/<article>: fall back to <body> but still drop nav/footer/script.
	doc := `<html><body><nav>Skip me</nav><p>Keep this.</p><footer>Not this</footer></body></html>`
	got, err := ToMarkdown([]byte(doc), Options{})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if !strings.Contains(got.Markdown, "Keep this.") {
		t.Errorf("missing body text, got:\n%s", got.Markdown)
	}
	if strings.Contains(got.Markdown, "Skip me") || strings.Contains(got.Markdown, "Not this") {
		t.Errorf("chrome leaked, got:\n%s", got.Markdown)
	}
}

func TestHTMLTable(t *testing.T) {
	doc := `<table>
	  <tr><th>Region</th><th>Sales</th></tr>
	  <tr><td>EMEA</td><td>120</td></tr>
	</table>`
	got, err := ToMarkdown([]byte(doc), Options{MIME: "text/html"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	for _, want := range []string{"| Region | Sales |", "| --- | --- |", "| EMEA | 120 |"} {
		if !strings.Contains(got.Markdown, want) {
			t.Errorf("table missing %q, got:\n%s", want, got.Markdown)
		}
	}
}

// A literal pipe in a table cell (a price range like "50 | 100") must be
// escaped, not left to split the cell into a phantom extra column — the same
// corruption class escapeCell exists to prevent for the CSV/TSV renderer.
func TestHTMLTablePipeInCell(t *testing.T) {
	doc := `<table>
	  <tr><th>Range</th><th>Label</th></tr>
	  <tr><td>50 | 100</td><td>mid</td></tr>
	</table>`
	got, err := ToMarkdown([]byte(doc), Options{MIME: "text/html"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if !strings.Contains(got.Markdown, `50 \| 100`) {
		t.Errorf("pipe in cell must be escaped, got:\n%s", got.Markdown)
	}
	if !strings.Contains(got.Markdown, `| 50 \| 100 | mid |`) {
		t.Errorf("row structure broken by an unescaped pipe, got:\n%s", got.Markdown)
	}
}

func TestHTMLInlineSpacing(t *testing.T) {
	tests := []struct{ name, html, want string }{
		{"inline between words", "<p>Revenue grew by <strong>12%</strong> this quarter.</p>", "Revenue grew by **12%** this quarter."},
		{"adjacent inline spans", "<p><strong>A</strong> <em>B</em></p>", "**A** *B*"},
		{"no spurious space before punctuation", "<p>See <code>here</code>.</p>", "See `here`."},
		{"link between words", `<p>Read <a href="https://x.co">the docs</a> now.</p>`, "Read [the docs](https://x.co) now."},
		{"no space where source had none", "<p><strong>Bold</strong>text</p>", "**Bold**text"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ToMarkdown([]byte(tc.html), Options{MIME: "text/html"})
			if err != nil {
				t.Fatalf("ToMarkdown: %v", err)
			}
			if strings.TrimSpace(got.Markdown) != tc.want {
				t.Errorf("got  %q\nwant %q", strings.TrimSpace(got.Markdown), tc.want)
			}
		})
	}
}

// A <pre> block's text is rendered inside a fixed "```" fence with no
// escaping; content containing its own standalone-``` line closes that fence
// early, the same defect fixed in convert.go's JSON branch.
func TestHTMLPreFenceNotBrokenByContent(t *testing.T) {
	doc := "<pre>before\n```\nfence-breaking line\n```\nafter</pre>"
	got, err := ToMarkdown([]byte(doc), Options{MIME: "text/html"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(got.Markdown), "\n")
	if len(lines) < 2 {
		t.Fatalf("markdown too short:\n%s", got.Markdown)
	}
	openFence := lines[0]
	closeFence := lines[len(lines)-1]
	if !strings.HasPrefix(openFence, "```") {
		t.Fatalf("expected an opening fence, got %q", openFence)
	}
	if closeFence != openFence {
		t.Errorf("closing fence %q does not match opening fence %q — content broke out early, got:\n%s", closeFence, openFence, got.Markdown)
	}
	if len(openFence) < 4 {
		t.Errorf("fence %q is not longer than the embedded ``` run", openFence)
	}
	if strings.Count(got.Markdown, openFence) != 2 {
		t.Errorf("expected exactly 2 fence lines (open+close), got:\n%s", got.Markdown)
	}
	if !strings.Contains(got.Markdown, "fence-breaking line") || !strings.Contains(got.Markdown, "after") {
		t.Errorf("embedded content lost, got:\n%s", got.Markdown)
	}
}

func TestHTMLNeverEmpty(t *testing.T) {
	// A document with no extractable text must still not produce an empty body.
	got, err := ToMarkdown([]byte("<html><body><script>x=1</script></body></html>"), Options{})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if strings.TrimSpace(got.Markdown) == "" {
		t.Error("Markdown must never be empty on a nil error")
	}
}
