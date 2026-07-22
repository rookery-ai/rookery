package convert

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
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
