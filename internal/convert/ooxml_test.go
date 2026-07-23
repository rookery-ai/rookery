package convert

import (
	"archive/zip"
	"bytes"
	"fmt"
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

const xlsxWorkbook = `<?xml version="1.0"?>
<workbook xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
 <sheets><sheet name="Q3" sheetId="1" r:id="rId1"/></sheets>
</workbook>`

// Cells with t="s" reference sharedStrings by index; inline numbers do not.
const xlsxSheet = `<?xml version="1.0"?>
<worksheet><sheetData>
 <row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row>
 <row r="2"><c r="A2" t="s"><v>2</v></c><c r="B2"><v>120</v></c></row>
</sheetData></worksheet>`

const xlsxShared = `<?xml version="1.0"?>
<sst><si><t>Region</t></si><si><t>Sales</t></si><si><t>EMEA</t></si></sst>`

func TestXLSXToMarkdown(t *testing.T) {
	data := buildZip(t, map[string]string{
		"xl/workbook.xml":          xlsxWorkbook,
		"xl/worksheets/sheet1.xml": xlsxSheet,
		"xl/sharedStrings.xml":     xlsxShared,
	})
	got, err := ToMarkdown(data, Options{Filename: "sales.xlsx"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if got.Kind != KindXLSX {
		t.Errorf("Kind = %q", got.Kind)
	}
	for _, want := range []string{"## Q3", "| Region | Sales |", "| EMEA | 120 |"} {
		if !strings.Contains(got.Markdown, want) {
			t.Errorf("missing %q, got:\n%s", want, got.Markdown)
		}
	}
}

func TestXLSXSparseRowsAlign(t *testing.T) {
	// A row that skips column A must not shift its values left — the cell
	// reference (r="B2") is what places a value, not its position in the XML.
	sheet := `<worksheet><sheetData>
	 <row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row>
	 <row r="2"><c r="B2"><v>7</v></c></row>
	</sheetData></worksheet>`
	data := buildZip(t, map[string]string{
		"xl/workbook.xml":          xlsxWorkbook,
		"xl/worksheets/sheet1.xml": sheet,
		"xl/sharedStrings.xml":     xlsxShared,
	})
	got, err := ToMarkdown(data, Options{Filename: "sparse.xlsx"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if !strings.Contains(got.Markdown, "|  | 7 |") {
		t.Errorf("sparse row should keep its column position, got:\n%s", got.Markdown)
	}
}

// xlsxReorderedWorkbook declares Finance before Marketing in <sheets> — the
// order the user sees as tabs — but its rels deliberately point the OTHER
// way round from physical file numbering: rId1 (Finance, declared first)
// resolves to sheet2.xml, and rId2 (Marketing, declared second) resolves to
// sheet1.xml. This reproduces a workbook whose sheets were reordered/renamed
// after creation, which Excel does not renumber the underlying parts for.
const xlsxReorderedWorkbook = `<?xml version="1.0"?>
<workbook xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
 <sheets>
  <sheet name="Finance" sheetId="1" r:id="rId1"/>
  <sheet name="Marketing" sheetId="2" r:id="rId2"/>
 </sheets>
</workbook>`

const xlsxReorderedRels = `<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
 <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet2.xml"/>
 <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>
</Relationships>`

// sheet1.xml physically holds Marketing's data; sheet2.xml physically holds
// Finance's data — the inverse of what positional pairing would assume.
const xlsxReorderedSheet1 = `<worksheet><sheetData>
 <row r="1"><c r="A1" t="inlineStr"><is><t>MarketingNum</t></is></c></row>
 <row r="2"><c r="A2"><v>100</v></c></row>
</sheetData></worksheet>`

const xlsxReorderedSheet2 = `<worksheet><sheetData>
 <row r="1"><c r="A1" t="inlineStr"><is><t>FinanceNum</t></is></c></row>
 <row r="2"><c r="A2"><v>200</v></c></row>
</sheetData></worksheet>`

// TestXLSXSheetNamesFollowRelsNotPosition is Finding 1: the heading a sheet's
// data appears under must come from resolving r:id through
// xl/_rels/workbook.xml.rels, not from pairing <sheets> position i with
// sheetN+1.xml. Under the old positional pairing this test fails: "Finance"
// would head Marketing's data (in sheet1.xml) and vice versa.
func TestXLSXSheetNamesFollowRelsNotPosition(t *testing.T) {
	data := buildZip(t, map[string]string{
		"xl/workbook.xml":            xlsxReorderedWorkbook,
		"xl/_rels/workbook.xml.rels": xlsxReorderedRels,
		"xl/worksheets/sheet1.xml":   xlsxReorderedSheet1,
		"xl/worksheets/sheet2.xml":   xlsxReorderedSheet2,
	})
	got, err := ToMarkdown(data, Options{Filename: "mismatch.xlsx"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if !strings.Contains(got.Markdown, "## Finance\n\n| FinanceNum |") {
		t.Errorf("Finance heading must be followed by Finance's own data, got:\n%s", got.Markdown)
	}
	if !strings.Contains(got.Markdown, "## Marketing\n\n| MarketingNum |") {
		t.Errorf("Marketing heading must be followed by Marketing's own data, got:\n%s", got.Markdown)
	}
	if strings.Contains(got.Markdown, "## Finance\n\n| MarketingNum |") {
		t.Errorf("Finance heading must not be followed by Marketing's data, got:\n%s", got.Markdown)
	}
}

// TestXLSXMalformedSharedStringIndexRendersEmpty is Finding 2: a non-numeric
// t="s" index must render an empty cell, never shared[0] — a real but
// unrelated string that would otherwise look like a plausible value.
func TestXLSXMalformedSharedStringIndexRendersEmpty(t *testing.T) {
	sheet := `<worksheet><sheetData>
	 <row r="1"><c r="A1" t="inlineStr"><is><t>Region</t></is></c><c r="B1" t="inlineStr"><is><t>Note</t></is></c></row>
	 <row r="2"><c r="A2" t="s"><v>not-a-number</v></c><c r="B2" t="inlineStr"><is><t>ok</t></is></c></row>
	</sheetData></worksheet>`
	shared := `<sst><si><t>ZEROTH-SHOULD-NOT-APPEAR</t></si></sst>`
	data := buildZip(t, map[string]string{
		"xl/workbook.xml":          xlsxWorkbook,
		"xl/worksheets/sheet1.xml": sheet,
		"xl/sharedStrings.xml":     shared,
	})
	got, err := ToMarkdown(data, Options{Filename: "badidx.xlsx"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if strings.Contains(got.Markdown, "ZEROTH-SHOULD-NOT-APPEAR") {
		t.Errorf("a malformed shared-string index must not render shared[0], got:\n%s", got.Markdown)
	}
	if !strings.Contains(got.Markdown, "|  | ok |") {
		t.Errorf("malformed-index cell should render empty, other cell unaffected, got:\n%s", got.Markdown)
	}
}

// TestXLSXCellsWithoutRefTrackPosition is Finding 3: cells that omit the r=
// A1 reference (legal per ECMA-376; some non-Excel writers rely on document
// order) must advance a running column position, not all collapse onto
// column A and overwrite one another.
func TestXLSXCellsWithoutRefTrackPosition(t *testing.T) {
	sheet := `<worksheet><sheetData>
	 <row r="1"><c r="A1" t="inlineStr"><is><t>Field</t></is></c><c r="B1" t="inlineStr"><is><t>Value</t></is></c></row>
	 <row><c t="inlineStr"><is><t>Region</t></is></c><c t="inlineStr"><is><t>EMEA</t></is></c></row>
	</sheetData></worksheet>`
	data := buildZip(t, map[string]string{
		"xl/workbook.xml":          xlsxWorkbook,
		"xl/worksheets/sheet1.xml": sheet,
	})
	got, err := ToMarkdown(data, Options{Filename: "noref.xlsx"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if !strings.Contains(got.Markdown, "| Region | EMEA |") {
		t.Errorf("ref-less cells must keep their sequential positions, got:\n%s", got.Markdown)
	}
}

// TestXLSXEmptySheetIsWarned is Finding 4: an empty sheet must not vanish
// without a trace — a Warnings entry names it, even though no data was lost.
func TestXLSXEmptySheetIsWarned(t *testing.T) {
	workbook := `<?xml version="1.0"?>
<workbook xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
 <sheets>
  <sheet name="Empty" sheetId="1" r:id="rId1"/>
  <sheet name="Data" sheetId="2" r:id="rId2"/>
 </sheets>
</workbook>`
	rels := `<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
 <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>
 <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet2.xml"/>
</Relationships>`
	emptySheet := `<worksheet><sheetData></sheetData></worksheet>`
	dataSheet := `<worksheet><sheetData>
	 <row r="1"><c r="A1" t="inlineStr"><is><t>Col</t></is></c></row>
	 <row r="2"><c r="A2"><v>1</v></c></row>
	</sheetData></worksheet>`
	data := buildZip(t, map[string]string{
		"xl/workbook.xml":            workbook,
		"xl/_rels/workbook.xml.rels": rels,
		"xl/worksheets/sheet1.xml":   emptySheet,
		"xl/worksheets/sheet2.xml":   dataSheet,
	})
	got, err := ToMarkdown(data, Options{Filename: "empty.xlsx"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	found := false
	for _, w := range got.Warnings {
		if strings.Contains(w, "Empty") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a warning naming the empty sheet, got: %v", got.Warnings)
	}
	if !strings.Contains(got.Markdown, "## Data") {
		t.Errorf("the populated sheet must still render, got:\n%s", got.Markdown)
	}
}

// TestXLSXTruncationWarningIncludesCounts is Finding 5: the row-cap warning
// must report both the omitted and total row counts, matching tabular.go's
// wording rather than a bare "truncated to N rows".
func TestXLSXTruncationWarningIncludesCounts(t *testing.T) {
	const extra = 10
	total := maxTableRows + extra
	var sheet strings.Builder
	sheet.WriteString("<worksheet><sheetData>")
	sheet.WriteString(`<row r="1"><c r="A1" t="inlineStr"><is><t>N</t></is></c></row>`)
	for i := 1; i <= total; i++ {
		fmt.Fprintf(&sheet, `<row r="%d"><c r="A%d"><v>%d</v></c></row>`, i+1, i+1, i)
	}
	sheet.WriteString("</sheetData></worksheet>")
	data := buildZip(t, map[string]string{
		"xl/workbook.xml":          xlsxWorkbook,
		"xl/worksheets/sheet1.xml": sheet.String(),
	})
	got, err := ToMarkdown(data, Options{Filename: "big.xlsx"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	want := fmt.Sprintf(
		"row limit reached: %d of %d rows are not in this note — read the preserved original for the full data",
		extra, total)
	found := false
	for _, w := range got.Warnings {
		if w == want {
			found = true
		}
	}
	if !found {
		t.Errorf("want warning %q, got: %v", want, got.Warnings)
	}
}

const pptxSlide = `<?xml version="1.0"?>
<p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
       xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">
 <p:cSld><p:spTree>
  <p:sp><p:txBody><a:p><a:r><a:t>Roadmap</a:t></a:r></a:p></p:txBody></p:sp>
  <p:sp><p:txBody><a:p><a:r><a:t>Ship phase one</a:t></a:r></a:p></p:txBody></p:sp>
 </p:spTree></p:cSld>
</p:sld>`

func TestPPTXToMarkdown(t *testing.T) {
	data := buildZip(t, map[string]string{
		"ppt/slides/slide1.xml": pptxSlide,
		"ppt/slides/slide2.xml": strings.Replace(pptxSlide, "Roadmap", "Risks", 1),
	})
	got, err := ToMarkdown(data, Options{Filename: "deck.pptx"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	if got.Kind != KindPPTX {
		t.Errorf("Kind = %q", got.Kind)
	}
	for _, want := range []string{"## Slide 1", "Roadmap", "Ship phase one", "## Slide 2", "Risks"} {
		if !strings.Contains(got.Markdown, want) {
			t.Errorf("missing %q, got:\n%s", want, got.Markdown)
		}
	}
}

func TestPPTXSlidesInNumericOrder(t *testing.T) {
	// Zip entry order is arbitrary and slide10 sorts before slide2 lexically;
	// slides must come out in presentation order.
	parts := map[string]string{}
	for _, n := range []string{"1", "2", "10"} {
		parts["ppt/slides/slide"+n+".xml"] = strings.Replace(pptxSlide, "Roadmap", "S"+n, 1)
	}
	data := buildZip(t, parts)
	got, err := ToMarkdown(data, Options{Filename: "d.pptx"})
	if err != nil {
		t.Fatalf("ToMarkdown: %v", err)
	}
	i1, i2, i10 := strings.Index(got.Markdown, "S1"), strings.Index(got.Markdown, "S2"), strings.Index(got.Markdown, "S10")
	if !(i1 < i2 && i2 < i10) {
		t.Errorf("slides out of order: S1=%d S2=%d S10=%d\n%s", i1, i2, i10, got.Markdown)
	}
}
