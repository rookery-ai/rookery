package convert

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// maxPartBytes bounds how much a single archive part may inflate to. An OOXML
// file is a zip, so an untrusted upload can be a decompression bomb; refusing
// an oversized part is cheaper and safer than discovering it after allocation.
const maxPartBytes = 32 << 20 // 32 MiB

func openOOXML(data []byte) (*zip.Reader, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("convert: not a readable archive: %w", err)
	}
	return zr, nil
}

// readZipPart reads one named part, refusing anything that inflates past the cap.
func readZipPart(zr *zip.Reader, name string) ([]byte, error) {
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		if f.UncompressedSize64 > maxPartBytes {
			return nil, fmt.Errorf("convert: archive part %s is too large (%d bytes)", name, f.UncompressedSize64)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("convert: open %s: %w", name, err)
		}
		defer rc.Close()
		data, err := io.ReadAll(io.LimitReader(rc, maxPartBytes+1))
		if err != nil {
			return nil, fmt.Errorf("convert: read %s: %w", name, err)
		}
		if len(data) > maxPartBytes {
			return nil, fmt.Errorf("convert: archive part %s exceeded the size cap", name)
		}
		return data, nil
	}
	return nil, fmt.Errorf("convert: archive is missing %s", name)
}

// detectOOXMLKind identifies which OOXML format an archive holds by looking for
// the part each format defines. This is what lets detection survive a wrong or
// missing extension — all three formats share the same zip magic bytes.
func detectOOXMLKind(data []byte) Kind {
	zr, err := openOOXML(data)
	if err != nil {
		return KindUnknown
	}
	for _, f := range zr.File {
		switch {
		case f.Name == "word/document.xml":
			return KindDOCX
		case f.Name == "xl/workbook.xml":
			return KindXLSX
		case strings.HasPrefix(f.Name, "ppt/slides/slide"):
			return KindPPTX
		}
	}
	return KindUnknown
}

// docxParagraph is one <w:p>, decoded far enough to know its style and text.
type docxParagraph struct {
	Style   string
	IsList  bool
	Text    string
	IsTable bool
	Rows    [][]string
}

// docxToMarkdown converts word/document.xml. It walks the XML as a token stream
// rather than binding a struct: WordprocessingML nests runs, bookmarks, and
// revision marks unpredictably, and a token walk collects text correctly
// regardless of what wraps it.
func docxToMarkdown(data []byte, opt Options) (Result, error) {
	zr, err := openOOXML(data)
	if err != nil {
		return Result{}, err
	}
	part, err := readZipPart(zr, "word/document.xml")
	if err != nil {
		return Result{}, err
	}

	paras, err := parseDocxParagraphs(part)
	if err != nil {
		return Result{}, err
	}

	res := Result{Kind: KindDOCX, Extractor: "pure-go", Title: titleFromFilename(opt.Filename)}
	var sb strings.Builder
	for _, p := range paras {
		if p.IsTable {
			writeTable(&sb, p.Rows)
			sb.WriteString("\n")
			continue
		}
		text := strings.TrimSpace(p.Text)
		if text == "" {
			continue
		}
		switch {
		case p.IsList:
			sb.WriteString("- " + text + "\n")
		case strings.HasPrefix(p.Style, "Heading"):
			level := headingLevel(p.Style)
			if res.Title == titleFromFilename(opt.Filename) || res.Title == "" {
				res.Title = text
			}
			sb.WriteString("\n" + strings.Repeat("#", level) + " " + text + "\n\n")
		default:
			sb.WriteString(text + "\n\n")
		}
	}
	body := collapseBlankLines(sb.String())
	if strings.TrimSpace(body) == "" {
		return Result{}, fmt.Errorf("convert: docx contained no readable text")
	}
	res.Markdown = normalizeText(body)
	return res, nil
}

// parseDocxParagraphs walks the document body, emitting one entry per paragraph
// and one per table.
//
// Table state is a STACK, not a single pointer. A <w:tbl> can appear inside a
// <w:tc> of an enclosing table (layout tables, signature blocks, pasted
// sub-tables — all common in real Word documents), and a single pointer gets
// overwritten by the inner table before the outer one's rows are closed out:
// the inner table's closing </w:tbl> then nils the pointer out from under the
// outer table's still-pending end-tags, which silently no-op against a nil
// table. A dropped table is unrecoverable — the converted note is what every
// agent sees, so pushing/popping a stack (restoring the enclosing table when
// the inner one closes) is required, not just tidier.
//
// A nested table is rendered INLINE within its containing cell as flattened
// text (rows joined with "; ", cells within a row joined with " | "), rather
// than as a second markdown table emitted after the outer one. Two reasons:
// (1) writeRow/pad render exactly one table level — a real nested markdown
// table inside a cell isn't representable (a table cell can't contain the
// newlines a table needs), and (2) inlining keeps the inner content anchored
// at the exact position it occupied in the source, rather than detached to a
// location after the whole outer table that no longer reflects which cell it
// came from.
func parseDocxParagraphs(part []byte) ([]docxParagraph, error) {
	dec := xml.NewDecoder(bytes.NewReader(part))
	var out []docxParagraph
	var cur *docxParagraph

	// Parallel stacks, one frame per currently-open <w:tbl>: tableStack holds
	// the table being assembled, rowStack holds the row currently being
	// assembled for that same table. A nested <w:tbl> pushes a new frame onto
	// both without disturbing the enclosing table's frame beneath it.
	var tableStack []*docxParagraph
	var rowStack [][]string

	// One frame per currently-open <w:tc>, innermost last. Text/br/tab always
	// target the top frame, so an inner cell's content can never leak into the
	// enclosing cell that's still mid-flight around it.
	var cellStack []*strings.Builder

	// target returns the innermost open cell's builder, or nil when we're not
	// inside any cell — callers fall back to writing into `cur.Text` (the
	// current top-level paragraph) in that case.
	target := func() *strings.Builder {
		if len(cellStack) > 0 {
			return cellStack[len(cellStack)-1]
		}
		return nil
	}

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("convert: parse docx xml: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "tbl":
				tableStack = append(tableStack, &docxParagraph{IsTable: true})
				rowStack = append(rowStack, nil)
			case "tr":
				if n := len(rowStack); n > 0 {
					rowStack[n-1] = nil
				}
			case "tc":
				cellStack = append(cellStack, &strings.Builder{})
			case "p":
				if b := target(); b != nil {
					// A second (or later) paragraph within the same cell must
					// not fuse onto the previous one (Finding 3) — a markdown
					// table cell can't hold a newline, so a single space is
					// the separator writeRow's flattening leaves intact.
					sepIfNeeded(b)
				} else {
					cur = &docxParagraph{}
				}
			case "pStyle":
				if cur != nil {
					cur.Style = attrValue(t, "val")
				}
			case "numPr":
				if cur != nil {
					cur.IsList = true
				}
			case "t":
				var text string
				if err := dec.DecodeElement(&text, &t); err == nil {
					if b := target(); b != nil {
						b.WriteString(text)
					} else if cur != nil {
						cur.Text += text
					}
				}
			case "br":
				// A manual line break (Shift+Enter) is a real separator, not
				// adjacent text — without this, "Line1<br/>Line2" fuses into
				// "Line1Line2". html.go's atom.Br handling is the same fix for
				// the same defect class.
				if b := target(); b != nil {
					b.WriteString("\n")
				} else if cur != nil {
					cur.Text += "\n"
				}
			case "tab":
				// A tab stop (e.g. "Name:<tab/>John") separates two spans just
				// as much as a space would; without this it fuses into
				// "Name:John".
				if b := target(); b != nil {
					b.WriteString(" ")
				} else if cur != nil {
					cur.Text += " "
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "tc":
				n := len(cellStack)
				if n == 0 {
					break
				}
				cellText := strings.TrimSpace(cellStack[n-1].String())
				cellStack = cellStack[:n-1]
				if rn := len(rowStack); rn > 0 {
					rowStack[rn-1] = append(rowStack[rn-1], cellText)
				}
			case "tr":
				ti, ri := len(tableStack)-1, len(rowStack)-1
				if ti >= 0 && ri >= 0 && len(rowStack[ri]) > 0 {
					tableStack[ti].Rows = append(tableStack[ti].Rows, rowStack[ri])
				}
			case "tbl":
				n := len(tableStack)
				if n == 0 {
					break
				}
				finished := tableStack[n-1]
				tableStack = tableStack[:n-1]
				rowStack = rowStack[:len(rowStack)-1]
				if len(finished.Rows) == 0 {
					break
				}
				if b := target(); b != nil {
					// Nested inside an enclosing cell that's still open:
					// inline it there (see the function-level comment for why).
					sepIfNeeded(b)
					b.WriteString(flattenTableInline(finished.Rows))
				} else {
					out = append(out, *finished)
				}
			case "p":
				if len(cellStack) == 0 && cur != nil {
					out = append(out, *cur)
					cur = nil
				}
			}
		}
	}
	return out, nil
}

// sepIfNeeded inserts a single space before the next chunk of cell text, but
// only when the builder already holds content that doesn't already end on
// whitespace — so it never introduces a leading space on an empty cell nor
// doubles up a separator already written (e.g. by an intervening empty
// paragraph).
func sepIfNeeded(b *strings.Builder) {
	s := b.String()
	if s == "" {
		return
	}
	if !strings.HasSuffix(s, " ") && !strings.HasSuffix(s, "\n") {
		b.WriteString(" ")
	}
}

// flattenTableInline renders a nested table's rows as a single line of text:
// cells within a row joined by " | ", rows joined by "; ". This is deliberately
// NOT a markdown table — it is spliced into an already-open enclosing cell,
// which cannot contain the newlines a real table needs.
func flattenTableInline(rows [][]string) string {
	parts := make([]string, len(rows))
	for i, r := range rows {
		parts[i] = strings.Join(r, " | ")
	}
	return strings.Join(parts, "; ")
}

// writeTable renders collected rows as a markdown table with the first row as
// the header — shared by the docx, xlsx and pptx converters.
func writeTable(sb *strings.Builder, rows [][]string) {
	if len(rows) == 0 {
		return
	}
	width := 0
	for _, r := range rows {
		if len(r) > width {
			width = len(r)
		}
	}
	writeRow(sb, pad(rows[0], width))
	sep := make([]string, width)
	for i := range sep {
		sep[i] = "---"
	}
	writeRow(sb, sep)
	for _, r := range rows[1:] {
		writeRow(sb, pad(r, width))
	}
}

// headingLevel maps a Word style name ("Heading2") to a markdown level,
// clamped to 1-6.
func headingLevel(style string) int {
	digits := strings.TrimPrefix(style, "Heading")
	if digits == "" {
		return 1
	}
	n := int(digits[0] - '0')
	if n < 1 {
		return 1
	}
	if n > 6 {
		return 6
	}
	return n
}

func attrValue(se xml.StartElement, name string) string {
	for _, a := range se.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// xlsxToMarkdown renders each worksheet as a markdown table under a heading.
// Values live in xl/worksheets/sheetN.xml, but text values are stored once in
// xl/sharedStrings.xml and referenced by index (t="s"), so the shared table
// must be resolved or every text cell reads as a bare number.
func xlsxToMarkdown(data []byte, opt Options) (Result, error) {
	zr, err := openOOXML(data)
	if err != nil {
		return Result{}, err
	}
	shared := readSharedStrings(zr)
	names := sheetNames(zr)

	res := Result{Kind: KindXLSX, Extractor: "pure-go", Title: titleFromFilename(opt.Filename)}
	var sb strings.Builder
	sheets := 0
	for i := 1; ; i++ {
		part, err := readZipPart(zr, fmt.Sprintf("xl/worksheets/sheet%d.xml", i))
		if err != nil {
			break
		}
		rows, err := parseSheetRows(part, shared)
		if err != nil {
			return Result{}, err
		}
		if len(rows) == 0 {
			continue
		}
		sheets++
		name := fmt.Sprintf("Sheet%d", i)
		if i-1 < len(names) && names[i-1] != "" {
			name = names[i-1]
		}
		fmt.Fprintf(&sb, "## %s\n\n", name)
		if len(rows) > maxTableRows+1 {
			rows = rows[:maxTableRows+1]
			res.Warnings = append(res.Warnings, fmt.Sprintf("sheet %s truncated to %d rows", name, maxTableRows))
		}
		writeTable(&sb, rows)
		sb.WriteString("\n")
	}
	if sheets == 0 {
		return Result{}, fmt.Errorf("convert: xlsx contained no readable sheets")
	}
	res.Markdown = normalizeText(collapseBlankLines(sb.String()))
	return res, nil
}

// readSharedStrings returns the shared string table, or nil when absent (a
// workbook of pure numbers legitimately has none).
func readSharedStrings(zr *zip.Reader) []string {
	part, err := readZipPart(zr, "xl/sharedStrings.xml")
	if err != nil {
		return nil
	}
	var sst struct {
		SI []struct {
			T string   `xml:"t"`
			R []string `xml:"r>t"` // rich text splits a value across runs
		} `xml:"si"`
	}
	if err := xml.Unmarshal(part, &sst); err != nil {
		return nil
	}
	out := make([]string, 0, len(sst.SI))
	for _, si := range sst.SI {
		if si.T != "" {
			out = append(out, si.T)
			continue
		}
		out = append(out, strings.Join(si.R, ""))
	}
	return out
}

// sheetNames returns worksheet display names in workbook order.
func sheetNames(zr *zip.Reader) []string {
	part, err := readZipPart(zr, "xl/workbook.xml")
	if err != nil {
		return nil
	}
	var wb struct {
		Sheets []struct {
			Name string `xml:"name,attr"`
		} `xml:"sheets>sheet"`
	}
	if err := xml.Unmarshal(part, &wb); err != nil {
		return nil
	}
	out := make([]string, 0, len(wb.Sheets))
	for _, s := range wb.Sheets {
		out = append(out, s.Name)
	}
	return out
}

// parseSheetRows decodes one worksheet into a dense grid. Cells carry an A1
// reference and sparse rows omit empty cells entirely, so column position comes
// from the reference — not from the cell's index in the XML.
func parseSheetRows(part []byte, shared []string) ([][]string, error) {
	var ws struct {
		Rows []struct {
			Cells []struct {
				Ref    string `xml:"r,attr"`
				Type   string `xml:"t,attr"`
				Value  string `xml:"v"`
				Inline string `xml:"is>t"`
			} `xml:"c"`
		} `xml:"sheetData>row"`
	}
	if err := xml.Unmarshal(part, &ws); err != nil {
		return nil, fmt.Errorf("convert: parse worksheet: %w", err)
	}
	var grid [][]string
	for _, r := range ws.Rows {
		row := []string{}
		for _, c := range r.Cells {
			col := columnIndex(c.Ref)
			for len(row) <= col {
				row = append(row, "")
			}
			row[col] = cellValue(c.Type, c.Value, c.Inline, shared)
		}
		grid = append(grid, row)
	}
	return grid, nil
}

func cellValue(typ, value, inline string, shared []string) string {
	switch typ {
	case "s":
		idx := 0
		fmt.Sscanf(value, "%d", &idx)
		if idx >= 0 && idx < len(shared) {
			return shared[idx]
		}
		return ""
	case "inlineStr":
		return inline
	default:
		return value
	}
}

// columnIndex converts the letter part of an A1 reference to a 0-based index
// ("A"→0, "B"→1, "AA"→26). An unparseable reference yields 0.
func columnIndex(ref string) int {
	idx := 0
	for _, ch := range ref {
		if ch < 'A' || ch > 'Z' {
			break
		}
		idx = idx*26 + int(ch-'A') + 1
	}
	if idx == 0 {
		return 0
	}
	return idx - 1
}

// pptxToMarkdown renders each slide as a heading followed by its text. Slides
// are numbered in the part name; zip entry order is arbitrary and lexical
// sorting puts slide10 before slide2, so the actual parts present are found
// and sorted numerically rather than assumed to be a dense 1,2,3... sequence
// (a sequential probe that stops at the first gap would, e.g., never reach
// slide10 in a deck whose slides are numbered 1, 2, 10).
func pptxToMarkdown(data []byte, opt Options) (Result, error) {
	zr, err := openOOXML(data)
	if err != nil {
		return Result{}, err
	}
	res := Result{Kind: KindPPTX, Extractor: "pure-go", Title: titleFromFilename(opt.Filename)}
	var sb strings.Builder
	slides := 0
	for _, n := range slideNumbers(zr) {
		part, err := readZipPart(zr, fmt.Sprintf("ppt/slides/slide%d.xml", n))
		if err != nil {
			continue
		}
		texts := extractDrawingText(part)
		if len(texts) == 0 {
			continue
		}
		slides++
		fmt.Fprintf(&sb, "## Slide %d\n\n", n)
		for j, t := range texts {
			if j == 0 {
				sb.WriteString("**" + t + "**\n\n")
				continue
			}
			sb.WriteString("- " + t + "\n")
		}
		sb.WriteString("\n")
	}
	if slides == 0 {
		return Result{}, fmt.Errorf("convert: pptx contained no readable slide text")
	}
	res.Markdown = normalizeText(collapseBlankLines(sb.String()))
	return res, nil
}

// slideNumbers returns the slide numbers actually present in the archive
// (from part names like "ppt/slides/slide12.xml"), sorted numerically —
// never lexically, and never assumed contiguous from 1.
func slideNumbers(zr *zip.Reader) []int {
	const prefix, suffix = "ppt/slides/slide", ".xml"
	var nums []int
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, prefix) || !strings.HasSuffix(f.Name, suffix) {
			continue
		}
		mid := strings.TrimSuffix(strings.TrimPrefix(f.Name, prefix), suffix)
		n, err := strconv.Atoi(mid)
		if err != nil {
			continue // defensively skip a slideN.xml-shaped name whose middle isn't actually numeric
		}
		nums = append(nums, n)
	}
	sort.Ints(nums)
	return nums
}

// extractDrawingText collects <a:t> values, one entry per <a:p> paragraph, so a
// shape's runs join into a single line instead of fragmenting.
func extractDrawingText(part []byte) []string {
	dec := xml.NewDecoder(bytes.NewReader(part))
	var out []string
	var cur strings.Builder
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return out
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "t" {
				var text string
				if err := dec.DecodeElement(&text, &t); err == nil {
					cur.WriteString(text)
				}
			}
		case xml.EndElement:
			if t.Name.Local == "p" {
				if s := strings.TrimSpace(cur.String()); s != "" {
					out = append(out, s)
				}
				cur.Reset()
			}
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, s)
	}
	return out
}
