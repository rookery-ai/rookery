package convert

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// buildZip assembles an in-memory zip from name→content pairs. Building the
// fixture in code rather than committing a binary keeps the test readable and
// lets each case state exactly the XML shape it is exercising.
func buildZip(t *testing.T, parts map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range parts {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

const docxBody = `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
 <w:body>
  <w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Quarterly Review</w:t></w:r></w:p>
  <w:p><w:r><w:t>Revenue was </w:t></w:r><w:r><w:t>up 12%.</w:t></w:r></w:p>
  <w:p><w:pPr><w:numPr><w:ilvl w:val="0"/></w:numPr></w:pPr><w:r><w:t>EMEA up</w:t></w:r></w:p>
  <w:p><w:pPr><w:numPr><w:ilvl w:val="0"/></w:numPr></w:pPr><w:r><w:t>APAC flat</w:t></w:r></w:p>
  <w:p/>
  <w:tbl>
   <w:tr><w:tc><w:p><w:r><w:t>Region</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>Sales</w:t></w:r></w:p></w:tc></w:tr>
   <w:tr><w:tc><w:p><w:r><w:t>EMEA</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>120</w:t></w:r></w:p></w:tc></w:tr>
  </w:tbl>
 </w:body>
</w:document>`

func TestDOCXToMarkdown(t *testing.T) {
	data := buildZip(t, map[string]string{"word/document.xml": docxBody})
	got, err := ToMarkdown(data, Options{Filename: "review.docx"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if got.Kind != KindDOCX {
		t.Errorf("Kind = %q", got.Kind)
	}
	for _, want := range []string{
		"# Quarterly Review",
		"Revenue was up 12%.", // runs inside one paragraph must join
		"- EMEA up",
		"- APAC flat",
		"| Region | Sales |",
		"| EMEA | 120 |",
	} {
		if !strings.Contains(got.Markdown, want) {
			t.Errorf("missing %q, got:\n%s", want, got.Markdown)
		}
	}
}

func TestDOCXTitleFromFirstHeading(t *testing.T) {
	data := buildZip(t, map[string]string{"word/document.xml": docxBody})
	got, _ := ToMarkdown(data, Options{Filename: "review.docx"})
	if got.Title != "Quarterly Review" {
		t.Errorf("Title = %q, want the first heading", got.Title)
	}
}

func TestDetectOOXMLFromArchiveParts(t *testing.T) {
	// The extension is missing/wrong; the archive's parts identify the format.
	data := buildZip(t, map[string]string{"word/document.xml": docxBody})
	if got := Detect(data, "mystery.bin", ""); got != KindDOCX {
		t.Errorf("Detect = %q, want docx from the archive parts", got)
	}
}

func TestDOCXMissingPartIsError(t *testing.T) {
	data := buildZip(t, map[string]string{"docProps/app.xml": "<x/>"})
	if _, err := ToMarkdown(data, Options{Filename: "broken.docx"}); err == nil {
		t.Error("a docx with no document.xml must error, not return a blank note")
	}
}

func TestZipBombRefused(t *testing.T) {
	// A part that inflates beyond the cap must be refused rather than read.
	huge := strings.Repeat("A", maxPartBytes+1)
	data := buildZip(t, map[string]string{"word/document.xml": huge})
	if _, err := ToMarkdown(data, Options{Filename: "bomb.docx"}); err == nil {
		t.Error("an oversized part must be refused")
	}
}

// nestedTableBody reproduces Finding 1: a 2x2 outer table whose cell (1,1)
// also holds a one-cell inner table sandwiched between two paragraphs. With a
// single (non-stack) table pointer, the inner table clobbers the outer one in
// progress and its closing </w:tbl> nils the pointer out from under the
// outer table's still-pending end-tags — silently dropping four of the six
// outer cells with no error.
const nestedTableBody = `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
 <w:body>
  <w:tbl>
   <w:tr>
    <w:tc>
     <w:p><w:r><w:t>OuterR1C1-before</w:t></w:r></w:p>
     <w:tbl>
      <w:tr><w:tc><w:p><w:r><w:t>InnerR1C1</w:t></w:r></w:p></w:tc></w:tr>
     </w:tbl>
     <w:p><w:r><w:t>OuterR1C1-after</w:t></w:r></w:p>
    </w:tc>
    <w:tc><w:p><w:r><w:t>OuterR1C2</w:t></w:r></w:p></w:tc>
   </w:tr>
   <w:tr>
    <w:tc><w:p><w:r><w:t>OuterR2C1</w:t></w:r></w:p></w:tc>
    <w:tc><w:p><w:r><w:t>OuterR2C2</w:t></w:r></w:p></w:tc>
   </w:tr>
  </w:tbl>
 </w:body>
</w:document>`

func TestDOCXNestedTablePreservesOuterTable(t *testing.T) {
	data := buildZip(t, map[string]string{"word/document.xml": nestedTableBody})
	got, err := ToMarkdown(data, Options{Filename: "nested.docx"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	// All six outer cells, plus the inner cell's own content, must survive.
	// The inner table is rendered inline in cell (1,1) rather than as a
	// second markdown table (see parseDocxParagraphs' doc comment) — so
	// "InnerR1C1" is expected to appear alongside the before/after text, not
	// as a standalone "| InnerR1C1 |" row.
	for _, want := range []string{
		"OuterR1C1-before",
		"InnerR1C1",
		"OuterR1C1-after",
		"OuterR1C2",
		"OuterR2C1",
		"OuterR2C2",
	} {
		if !strings.Contains(got.Markdown, want) {
			t.Errorf("missing %q, got:\n%s", want, got.Markdown)
		}
	}
	// The outer table must still render as one coherent 2x2 markdown table,
	// not have collapsed to just the inner cell's row.
	if !strings.Contains(got.Markdown, "| OuterR2C1 | OuterR2C2 |") {
		t.Errorf("outer table's second row missing, got:\n%s", got.Markdown)
	}
}

// docxBrTabBody exercises Finding 2 in both the paragraph and table-cell text
// paths: a manual line break (<w:br/>) and a tab stop (<w:tab/>) must
// separate the text runs on either side of them, not fuse them together.
const docxBrTabBody = `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
 <w:body>
  <w:p><w:r><w:t>Line1</w:t></w:r><w:r><w:br/></w:r><w:r><w:t>Line2</w:t></w:r></w:p>
  <w:p><w:r><w:t>Name:</w:t></w:r><w:r><w:tab/></w:r><w:r><w:t>John</w:t></w:r></w:p>
  <w:tbl>
   <w:tr>
    <w:tc><w:p><w:r><w:t>CellA</w:t></w:r><w:r><w:br/></w:r><w:r><w:t>CellB</w:t></w:r></w:p></w:tc>
    <w:tc><w:p><w:r><w:t>Tab1</w:t></w:r><w:r><w:tab/></w:r><w:r><w:t>Tab2</w:t></w:r></w:p></w:tc>
   </w:tr>
  </w:tbl>
 </w:body>
</w:document>`

func TestDOCXBreakAndTabDoNotFuseText(t *testing.T) {
	data := buildZip(t, map[string]string{"word/document.xml": docxBrTabBody})
	got, err := ToMarkdown(data, Options{Filename: "breaks.docx"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if strings.Contains(got.Markdown, "Line1Line2") {
		t.Errorf("<w:br/> fused paragraph text, got:\n%s", got.Markdown)
	}
	if !strings.Contains(got.Markdown, "Line1") || !strings.Contains(got.Markdown, "Line2") {
		t.Errorf("Line1/Line2 missing, got:\n%s", got.Markdown)
	}
	if strings.Contains(got.Markdown, "Name:John") {
		t.Errorf("<w:tab/> fused paragraph text, got:\n%s", got.Markdown)
	}
	if !strings.Contains(got.Markdown, "Name:") || !strings.Contains(got.Markdown, "John") {
		t.Errorf("Name:/John missing, got:\n%s", got.Markdown)
	}
	if strings.Contains(got.Markdown, "CellACellB") {
		t.Errorf("<w:br/> fused table-cell text, got:\n%s", got.Markdown)
	}
	if strings.Contains(got.Markdown, "Tab1Tab2") {
		t.Errorf("<w:tab/> fused table-cell text, got:\n%s", got.Markdown)
	}
}

// docxMultiParaCellBody exercises Finding 3: a single <w:tc> holding more
// than one <w:p> (common for addresses/notes columns) must not fuse them.
const docxMultiParaCellBody = `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
 <w:body>
  <w:tbl>
   <w:tr>
    <w:tc>
     <w:p><w:r><w:t>Line one</w:t></w:r></w:p>
     <w:p><w:r><w:t>Line two</w:t></w:r></w:p>
    </w:tc>
   </w:tr>
  </w:tbl>
 </w:body>
</w:document>`

func TestDOCXMultiParagraphCellDoesNotFuse(t *testing.T) {
	data := buildZip(t, map[string]string{"word/document.xml": docxMultiParaCellBody})
	got, err := ToMarkdown(data, Options{Filename: "multipara.docx"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if strings.Contains(got.Markdown, "Line oneLine two") {
		t.Errorf("multi-paragraph cell fused, got:\n%s", got.Markdown)
	}
	if !strings.Contains(got.Markdown, "Line one Line two") {
		t.Errorf("want \"Line one Line two\" (space-separated) in one cell, got:\n%s", got.Markdown)
	}
}
