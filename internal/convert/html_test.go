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
