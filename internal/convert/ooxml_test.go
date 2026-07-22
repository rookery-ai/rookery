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
