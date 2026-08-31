package convert

import (
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var updateFidelity = flag.Bool("update-fidelity", false,
	"rewrite the testdata/fidelity corpus from current converter output")

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return b
}

// The fidelity corpus is the bridge between this package and the knowledge base
// editor, and it exists because the two halves cannot be tested in one process.
//
// The editor decides whether an imported note is EDITABLE by round-tripping its
// body through a real parse/serialize cycle (checkFidelity, editor.ts). That
// runs in vitest under jsdom; nothing in Go can execute it. So a converter
// change can make every imported note open read-only while `go test ./...`
// stays entirely green — which is exactly what had happened.
//
// The frontend already had a test asserting converter-shaped markdown survives
// the editor, but its fixtures were hand-written approximations of what this
// package emits. That pins a STRING, not this package: the Go code could drift
// away from those fixtures without breaking them, and did.
//
// So this test writes what ToMarkdown ACTUALLY produced into
// testdata/fidelity/*.md, and web/ui/src/pages/kb/convertFidelity.test.ts reads
// that directory and runs the real checkFidelity over each file. The bytes under
// test are the bytes the converter emitted, and neither side can drift silently:
// changing a converter without regenerating fails HERE, and emitting markdown the
// editor cannot open fails THERE.
//
// Regenerate with:
//
//	go test ./internal/convert/ -run TestFidelityCorpus -update-fidelity
func TestFidelityCorpus(t *testing.T) {
	// needsPDFText names the cases whose output depends on whether pdftotext is
	// installed on THIS host: the extractor and the extracted text both differ
	// from the pure-Go fallback's. Comparing such a file against a committed
	// golden would fail on every machine that does not match the one which
	// generated it — including CI, which has no poppler — so the comparison is
	// skipped there. The committed file is still read and checked by the vitest
	// side, which is the assertion that actually matters: whatever produced it,
	// is the result editable?
	needsPDFText := map[string]bool{"pdf-simple": true}

	cases := []struct {
		name     string
		filename string
		data     []byte
	}{
		// Prose carrying every character the editor's serializer treats as
		// significant. This is the case that broke real imports: a Word document
		// saying "a < b" produced a note nobody could edit.
		{"html-prose-specials", "specials.html", []byte(`<html><body>
			<h1>Report: a &lt; b</h1>
			<p>The value of a &lt; b and c &gt; d was measured.</p>
			<p>See the note[^1] and reference [12] for details.</p>
			<p>The rate is 5* higher, roughly ~50 items.</p>
			<p>Open C:\Users\ada and press the ` + "`" + ` key.</p>
			<p>Tom &amp; Jerry shipped user_name_field on ticket #42.</p>
			<p>-40 degrees was recorded.</p>
		</body></html>`)},

		// An image with NO alt attribute. Before this corpus existed the whole
		// <img> case was gated on non-empty alt, so this emitted nothing at all.
		{"html-image-no-alt", "img.html", []byte(
			`<html><body><p>Before</p><img src="uploads/photo.png"><p>After</p></body></html>`)},

		// A destination containing the two things that end it early.
		{"html-image-awkward-src", "img2.html", []byte(
			`<html><body><img src="uploads/my file(1).png" alt="Chart"></body></html>`)},

		{"html-links", "links.html", []byte(`<html><body>
			<p>Read the <a href="https://x.com/a?b=1&amp;c=2">docs [v2]</a> today.</p>
			<p>Contact <a href="mailto:ops@example.com">ops</a>.</p>
		</body></html>`)},

		{"html-structure", "structure.html", []byte(`<html><body>
			<h2>Metrics</h2>
			<ul><li>First point</li><li>Second with <strong>bold</strong> and <em>italic</em></li></ul>
			<table><tr><th>Name</th><th>Value</th></tr>
			<tr><td>Growth &amp; churn</td><td>a &lt; b</td></tr>
			<tr><td>Pipe | inside</td><td>[bracketed]</td></tr></table>
			<pre><code>if a &lt; b { return }</code></pre>
			<p>Line one<br>line two.</p>
		</body></html>`)},

		// A table cell carrying every awkward character, via the CSV path.
		{"csv-specials", "data.csv", []byte(
			"item,note\nGrowth & churn,a < b\nPipe | inside,\"[12] and 5*\"\nPath,C:\\Users\\ada\n")},

		{"tsv-basic", "data.tsv", []byte("item\tnote\nAlpha\tone\nBeta\ttwo\n")},

		// Arbitrary bytes in a fence: the JSON path must not let content close
		// the fence early.
		{"json-basic", "payload.json", []byte("{\n  \"a\": 1,\n  \"b\": \"x < y\"\n}\n")},

		{"text-passthrough", "notes.txt", []byte(
			"Meeting notes\n\nDiscussed a < b and the [12] reference.\n")},

		{"markdown-passthrough", "doc.md", []byte(
			"# Title\n\nA paragraph with **bold** text.\n\n- one\n- two\n")},

		// The editor constructs. Each is emitted in the editor's OWN serialized
		// spelling; getting one wrong round-trips to something different and
		// opens the note read-only, so these files are the check on that.
		{"html-constructs", "constructs.html", []byte(`<html><body>
			<ol><li>First step</li><li>Second step</li></ol>
			<ul><li>Top<ul><li>Nested</li></ul></li><li>Another</li></ul>
			<blockquote><p>A quoted claim.</p><p>And a second paragraph.</p></blockquote>
			<details><summary>More detail</summary><p>Hidden body.</p></details>
			<div align="center"><p>Centred text</p></div>
			<p>Some <u>underlined</u> words.</p>
			<p>A <span style="color: #EF4444">red</span> word.</p>
			<p>A <span style="background-color:#fef08a">highlighted</span> word.</p>
			<pre><code class="language-go">if a &lt; b { return }</code></pre>
		</body></html>`)},

		{"docx-specials", "report.docx", buildZip(t, map[string]string{
			"word/document.xml": `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
 <w:body>
  <w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Report [2026]</w:t></w:r></w:p>
  <w:p><w:r><w:t>Where a &lt; b and c &gt; d, see [12].</w:t></w:r></w:p>
  <w:p><w:r><w:t>Line one</w:t></w:r><w:r><w:br/></w:r><w:r><w:t>line two.</w:t></w:r></w:p>
  <w:p><w:r><w:t>-40 degrees was recorded.</w:t></w:r></w:p>
  <w:p><w:pPr><w:numPr><w:ilvl w:val="0"/></w:numPr></w:pPr><w:r><w:t>A 5* rating</w:t></w:r></w:p>
  <w:tbl>
   <w:tr><w:tc><w:p><w:r><w:t>Name</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>Value</w:t></w:r></w:p></w:tc></w:tr>
   <w:tr><w:tc><w:p><w:r><w:t>a &lt; b</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>x | y</w:t></w:r></w:p></w:tc></w:tr>
  </w:tbl>
 </w:body>
</w:document>`,
		})},

		{"pptx-specials", "deck.pptx", buildZip(t, map[string]string{
			"ppt/slides/slide1.xml": `<?xml version="1.0"?>
<p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
 <a:p><a:r><a:t>Results [Q1]</a:t></a:r></a:p>
 <a:p><a:r><a:t>Growth &gt; 10% where a &lt; b</a:t></a:r></a:p>
 <a:p><a:r><a:t>A 5* outcome</a:t></a:r></a:p>
</p:sld>`,
		})},

		{"xlsx-specials", "book.xlsx", buildZip(t, map[string]string{
			"xl/workbook.xml": `<?xml version="1.0"?>
<workbook xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
 <sheets><sheet name="Q1 [draft]" sheetId="1" r:id="rId1"/></sheets>
</workbook>`,
			"xl/_rels/workbook.xml.rels": `<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
 <Relationship Id="rId1" Target="worksheets/sheet1.xml"/>
</Relationships>`,
			"xl/worksheets/sheet1.xml": `<?xml version="1.0"?>
<worksheet><sheetData>
 <row><c t="inlineStr"><is><t>item</t></is></c><c t="inlineStr"><is><t>note</t></is></c></row>
 <row><c t="inlineStr"><is><t>a &lt; b</t></is></c><c t="inlineStr"><is><t>[12] and 5*</t></is></c></row>
</sheetData></worksheet>`,
		})},

		// A real PDF, so the corpus covers the pdftotext path (or its pure-Go
		// fallback on a host without poppler) rather than only the formats this
		// package can parse itself.
		{"pdf-simple", "simple.pdf", mustRead(t, filepath.Join("testdata", "simple.pdf"))},
	}

	dir := filepath.Join("testdata", "fidelity")
	if *updateFidelity {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create corpus dir: %v", err)
		}
	}

	seen := map[string]bool{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := ToMarkdown(tc.data, Options{Filename: tc.filename})
			if err != nil {
				t.Fatalf("ToMarkdown(%s): %v", tc.filename, err)
			}
			// Marked seen before any skip, so a host-dependent case is never
			// reported as an orphan by the sweep below.
			seen[tc.name+".md"] = true
			if needsPDFText[tc.name] && pdftotextPath() == "" {
				t.Skip("pdftotext is not installed; this case's output would differ from the committed corpus")
			}
			path := filepath.Join(dir, tc.name+".md")
			if *updateFidelity {
				if err := os.WriteFile(path, []byte(res.Markdown), 0o644); err != nil {
					t.Fatalf("write %s: %v", path, err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s (regenerate with -update-fidelity): %v", path, err)
			}
			if string(want) != res.Markdown {
				t.Errorf("corpus file %s is stale.\n got: %q\nwant: %q\n"+
					"Regenerate with: go test ./internal/convert/ -run TestFidelityCorpus -update-fidelity",
					path, res.Markdown, string(want))
			}
		})
	}

	// A corpus file nobody generates any more would still be read by the vitest
	// side, where it would silently keep asserting against output no converter
	// produces. Catching it here keeps the two directories honest.
	if !*updateFidelity {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read corpus dir: %v", err)
		}
		var orphans []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") && !seen[e.Name()] {
				orphans = append(orphans, e.Name())
			}
		}
		sort.Strings(orphans)
		if len(orphans) > 0 {
			t.Errorf("corpus files with no generating case: %v", orphans)
		}
	}
}
