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
func parseDocxParagraphs(part []byte) ([]docxParagraph, error) {
	dec := xml.NewDecoder(bytes.NewReader(part))
	var out []docxParagraph
	var cur *docxParagraph
	var table *docxParagraph
	var row []string
	var cell strings.Builder
	inCell := false

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
				table = &docxParagraph{IsTable: true}
			case "tr":
				row = nil
			case "tc":
				inCell = true
				cell.Reset()
			case "p":
				if !inCell {
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
					if inCell {
						cell.WriteString(text)
					} else if cur != nil {
						cur.Text += text
					}
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "tc":
				inCell = false
				row = append(row, strings.TrimSpace(cell.String()))
			case "tr":
				if table != nil && len(row) > 0 {
					table.Rows = append(table.Rows, row)
				}
			case "tbl":
				if table != nil && len(table.Rows) > 0 {
					out = append(out, *table)
				}
				table = nil
			case "p":
				if !inCell && cur != nil {
					out = append(out, *cur)
					cur = nil
				}
			}
		}
	}
	return out, nil
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
