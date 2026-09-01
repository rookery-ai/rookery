package convert

import (
	"strings"
	"testing"
)

// A table written straight after a list item was absorbed INTO that bullet:
// markdown continues a list item across a single newline, so "- A rating\n|
// Name | Value |…" parsed as one long bullet and every row of the table was
// lost. Not merely a formatting blemish — the data was gone from the note, and
// the preserved original was the only remaining copy.
//
// Pinned here as well as in the fidelity corpus because the corpus asserts a
// whole-file byte comparison, where this would read as an incidental
// whitespace change rather than the destructive bug it is.
func TestDocxTableAfterListItemStartsItsOwnBlock(t *testing.T) {
	data := buildZip(t, map[string]string{
		"word/document.xml": `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
 <w:body>
  <w:p><w:pPr><w:numPr><w:ilvl w:val="0"/></w:numPr></w:pPr><w:r><w:t>A bullet</w:t></w:r></w:p>
  <w:tbl>
   <w:tr><w:tc><w:p><w:r><w:t>Name</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>Value</w:t></w:r></w:p></w:tc></w:tr>
   <w:tr><w:tc><w:p><w:r><w:t>Ada</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>42</w:t></w:r></w:p></w:tc></w:tr>
  </w:tbl>
 </w:body>
</w:document>`,
	})

	res, err := ToMarkdown(data, Options{Filename: "doc.docx"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if !strings.Contains(res.Markdown, "- A bullet\n\n| Name | Value |") {
		t.Errorf("table did not start a new block after the list item:\n%q", res.Markdown)
	}
	// The rows must survive as a real table, not as text inside the bullet.
	for _, want := range []string{"| --- | --- |", "| Ada | 42 |"} {
		if !strings.Contains(res.Markdown, want) {
			t.Errorf("missing table row %q in:\n%q", want, res.Markdown)
		}
	}
}

// A paragraph following a list item has the identical problem and the identical
// fix; without a blank line it is read as a continuation of the bullet rather
// than as prose of its own.
func TestDocxParagraphAfterListItemStartsItsOwnBlock(t *testing.T) {
	data := buildZip(t, map[string]string{
		"word/document.xml": `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
 <w:body>
  <w:p><w:pPr><w:numPr><w:ilvl w:val="0"/></w:numPr></w:pPr><w:r><w:t>A bullet</w:t></w:r></w:p>
  <w:p><w:r><w:t>Ordinary prose.</w:t></w:r></w:p>
 </w:body>
</w:document>`,
	})

	res, err := ToMarkdown(data, Options{Filename: "doc.docx"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if !strings.Contains(res.Markdown, "- A bullet\n\nOrdinary prose.") {
		t.Errorf("paragraph did not start a new block after the list item:\n%q", res.Markdown)
	}
}

// The heading branch used to write a literal leading "\n", so a document that
// opens with a heading — which is most documents — produced a note whose first
// byte was a blank line.
func TestDocxDoesNotStartWithABlankLine(t *testing.T) {
	data := buildZip(t, map[string]string{
		"word/document.xml": `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
 <w:body>
  <w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Title</w:t></w:r></w:p>
  <w:p><w:r><w:t>Body.</w:t></w:r></w:p>
 </w:body>
</w:document>`,
	})

	res, err := ToMarkdown(data, Options{Filename: "doc.docx"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if strings.HasPrefix(res.Markdown, "\n") {
		t.Errorf("output begins with a blank line: %q", res.Markdown)
	}
	if !strings.HasPrefix(res.Markdown, "# Title") {
		t.Errorf("expected the heading first, got: %q", res.Markdown)
	}
}
