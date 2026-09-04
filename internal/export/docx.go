package export

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"

	gast "github.com/yuin/goldmark/ast"
	xast "github.com/yuin/goldmark/extension/ast"
)

// ToDOCX renders a markdown note into a minimal OOXML (.docx) package, built
// pure-Go with archive/zip + encoding/xml so it is always available with no host
// tools. It mirrors how internal/convert READS docx (a zip of XML parts); this is
// the write side.
//
// The package is deliberately four parts — [Content_Types].xml, _rels/.rels,
// word/_rels/document.xml.rels, and word/document.xml. That means no
// numbering.xml and no styles.xml: lists render with literal markers ("• "/"1. ")
// rather than real <w:numPr> numbering (which would require numbering.xml), and
// headings use Word's built-in "HeadingN" styles by name (Word supplies them
// even without a styles part). Hyperlinks are the one feature needing a rels
// entry, and document.xml.rels is one of the four parts — so it stays coherent.
//
// Supported block set: headings, paragraphs, bold/italic/inline-code/strike runs,
// bullet & numbered lists (incl. nesting), blockquotes, code blocks (monospace),
// tables, horizontal rules, and external hyperlinks. Anything unrecognized
// degrades to a plain paragraph of its text rather than failing.
func ToDOCX(md []byte, opts Options) ([]byte, error) {
	root, source := parseMarkdown(md)

	d := &docxBuilder{}
	// A document title, when the note doesn't already open with its own H1.
	if title := opts.Title; title != "" && !firstIsHeading1(root) {
		d.body.WriteString(`<w:p><w:pPr><w:pStyle w:val="Title"/></w:pPr>`)
		writeRun(&d.body, title, runProps{})
		d.body.WriteString("</w:p>")
	}
	for c := root.FirstChild(); c != nil; c = c.NextSibling() {
		d.block(c, source, 0)
	}
	d.attachments(opts.Attachments)
	if d.body.Len() == 0 {
		// A body with no block-level content still needs one paragraph or Word
		// treats the file as damaged.
		d.body.WriteString("<w:p/>")
	}

	return d.zip()
}

// runProps is the accumulated inline formatting for a run. Nested inline nodes
// OR their property on, so bold-inside-italic stays both.
type runProps struct {
	bold, italic, code, strike, link bool
}

// docxRel is one relationship written into word/_rels/document.xml.rels.
//
// It carries its type because there are now two: an EXTERNAL hyperlink, and an
// INTERNAL image pointing at a media part inside the package. Word resolves a
// relationship by type, so an image registered with the hyperlink type is
// simply not found and the drawing renders as a missing-picture box.
type docxRel struct {
	id, target string
	relType    string
	external   bool
}

const (
	relTypeHyperlink = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink"
	relTypeImage     = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/image"
)

// docxMedia is one embedded image part.
type docxMedia struct {
	name string // part name relative to word/, e.g. "media/image1.png"
	ext  string // "png", "jpeg", "gif" — Word picks its decoder from this
	data []byte
}

// docxBuilder accumulates the document body XML plus the relationships and
// media parts discovered while walking the AST.
type docxBuilder struct {
	body     strings.Builder
	rels     []docxRel
	relSeq   int
	media    []docxMedia
	mediaSeq int
	// drawingSeq numbers the <wp:docPr> ids. Word requires them to be unique
	// within the document and treats a duplicate as a corrupt file, so this is
	// deliberately a document-wide counter rather than a per-paragraph one.
	drawingSeq int
}

// addRel registers an external hyperlink target and returns its relationship id,
// which the emitted <w:hyperlink r:id="…"> must match exactly.
func (d *docxBuilder) addRel(target string) string {
	d.relSeq++
	id := fmt.Sprintf("rId%d", d.relSeq)
	d.rels = append(d.rels, docxRel{id: id, target: target, relType: relTypeHyperlink, external: true})
	return id
}

// addImage stores an image as a media part and returns its relationship id.
func (d *docxBuilder) addImage(ext string, data []byte) string {
	d.mediaSeq++
	name := fmt.Sprintf("media/image%d.%s", d.mediaSeq, ext)
	d.media = append(d.media, docxMedia{name: name, ext: ext, data: data})

	d.relSeq++
	id := fmt.Sprintf("rId%d", d.relSeq)
	d.rels = append(d.rels, docxRel{id: id, target: name, relType: relTypeImage})
	return id
}

// block renders one top-level (or nested) block node into the body. indent is
// the list-nesting depth, used only by lists.
func (d *docxBuilder) block(n gast.Node, source []byte, indent int) {
	switch node := n.(type) {
	case *gast.Heading:
		level := node.Level
		if level < 1 {
			level = 1
		}
		if level > 6 {
			level = 6
		}
		d.body.WriteString(fmt.Sprintf(`<w:p><w:pPr><w:pStyle w:val="Heading%d"/></w:pPr>`, level))
		d.inlineChildren(&d.body, n, source, runProps{})
		d.body.WriteString("</w:p>")
	case *gast.Paragraph, *gast.TextBlock:
		d.body.WriteString("<w:p>")
		d.inlineChildren(&d.body, n, source, runProps{})
		d.body.WriteString("</w:p>")
	case *columnsNode:
		d.columns(node, source)
	case *alignNode:
		d.align(node, source)
	case *gast.List:
		d.list(node, source, indent)
	case *gast.Blockquote:
		// Render each contained block as a Quote-styled paragraph (Word's
		// built-in "Quote" style), so quotes read as quotes without a styles
		// part of our own.
		for c := node.FirstChild(); c != nil; c = c.NextSibling() {
			d.body.WriteString(`<w:p><w:pPr><w:pStyle w:val="Quote"/></w:pPr>`)
			d.inlineChildren(&d.body, c, source, runProps{italic: true})
			d.body.WriteString("</w:p>")
		}
	case *gast.FencedCodeBlock:
		d.codeBlock(n, source)
	case *gast.CodeBlock:
		d.codeBlock(n, source)
	case *gast.ThematicBreak:
		// A horizontal rule is an empty paragraph carrying a bottom border.
		d.body.WriteString(`<w:p><w:pPr><w:pBdr><w:bottom w:val="single" w:sz="6" w:space="1" w:color="auto"/></w:pBdr></w:pPr></w:p>`)
	case *xast.Table:
		d.table(node, source)
	case *gast.HTMLBlock:
		// Drop raw HTML blocks (safe — never emit unsanitized markup).
	default:
		// Unrecognized block: degrade to a plain paragraph of its text rather
		// than losing it.
		d.body.WriteString("<w:p>")
		if n.HasChildren() {
			d.inlineChildren(&d.body, n, source, runProps{})
		} else {
			writeRun(&d.body, nodeText(n, source), runProps{})
		}
		d.body.WriteString("</w:p>")
	}
}

// list renders an ordered or unordered list, recursing for nested lists. Markers
// are literal text runs ("• " / "1. ") because real numbering needs a
// numbering.xml part this package deliberately omits.
func (d *docxBuilder) list(list *gast.List, source []byte, depth int) {
	ordered := list.IsOrdered()
	num := list.Start
	if ordered && num == 0 {
		num = 1
	}
	for item := list.FirstChild(); item != nil; item = item.NextSibling() {
		li, ok := item.(*gast.ListItem)
		if !ok {
			continue
		}
		prefix := "•  "
		if ordered {
			prefix = fmt.Sprintf("%d.  ", num)
			num++
		}
		d.listItem(li, source, depth, prefix)
	}
}

// listItem renders one <li>: its first text block carries the marker prefix and
// an indent proportional to depth; nested lists recurse one level deeper.
func (d *docxBuilder) listItem(li *gast.ListItem, source []byte, depth int, prefix string) {
	ind := fmt.Sprintf(`<w:ind w:left="%d"/>`, 360*(depth+1))
	first := true
	for c := li.FirstChild(); c != nil; c = c.NextSibling() {
		switch c.(type) {
		case *gast.List:
			d.list(c.(*gast.List), source, depth+1)
		case *gast.TextBlock, *gast.Paragraph:
			d.body.WriteString("<w:p><w:pPr>" + ind + "</w:pPr>")
			if first {
				writeRun(&d.body, prefix, runProps{})
			}
			d.inlineChildren(&d.body, c, source, runProps{})
			d.body.WriteString("</w:p>")
			first = false
		default:
			d.body.WriteString("<w:p><w:pPr>" + ind + "</w:pPr>")
			if first {
				writeRun(&d.body, prefix, runProps{})
			}
			writeRun(&d.body, nodeText(c, source), runProps{})
			d.body.WriteString("</w:p>")
			first = false
		}
	}
}

// codeBlock renders a fenced or indented code block as a single shaded paragraph
// whose lines are monospace runs separated by manual breaks.
func (d *docxBuilder) codeBlock(n gast.Node, source []byte) {
	var sb strings.Builder
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		sb.Write(seg.Value(source))
	}
	text := strings.TrimRight(sb.String(), "\n")
	d.body.WriteString(`<w:p><w:pPr><w:shd w:val="clear" w:color="auto" w:fill="F6F8FA"/></w:pPr>`)
	for i, line := range strings.Split(text, "\n") {
		if i > 0 {
			d.body.WriteString("<w:r><w:br/></w:r>")
		}
		writeRun(&d.body, line, runProps{code: true})
	}
	d.body.WriteString("</w:p>")
}

// table renders a GFM table with bordered cells; the header row's runs are bold.
func (d *docxBuilder) table(t *xast.Table, source []byte) {
	cols := len(t.Alignments)
	d.body.WriteString(`<w:tbl><w:tblPr><w:tblStyle w:val="TableGrid"/><w:tblW w:w="0" w:type="auto"/><w:tblBorders>`)
	for _, side := range []string{"top", "left", "bottom", "right", "insideH", "insideV"} {
		d.body.WriteString(fmt.Sprintf(`<w:%s w:val="single" w:sz="4" w:space="0" w:color="D0D7DE"/>`, side))
	}
	d.body.WriteString(`</w:tblBorders></w:tblPr><w:tblGrid>`)
	for i := 0; i < cols; i++ {
		d.body.WriteString("<w:gridCol/>")
	}
	d.body.WriteString("</w:tblGrid>")
	for row := t.FirstChild(); row != nil; row = row.NextSibling() {
		header := false
		switch row.(type) {
		case *xast.TableHeader:
			header = true
		case *xast.TableRow:
		default:
			continue
		}
		d.tableRow(row, source, header)
	}
	d.body.WriteString("</w:tbl>")
	// Word wants a paragraph after a table, or a table at the very end of the
	// body renders oddly.
	d.body.WriteString("<w:p/>")
}

// tableRow renders one row; every cell holds at least one paragraph (Word treats
// a cell with none as damaged).
func (d *docxBuilder) tableRow(row gast.Node, source []byte, header bool) {
	d.body.WriteString("<w:tr>")
	for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
		if _, ok := cell.(*xast.TableCell); !ok {
			continue
		}
		d.body.WriteString(`<w:tc><w:tcPr><w:tcW w:w="0" w:type="auto"/></w:tcPr><w:p>`)
		d.inlineChildren(&d.body, cell, source, runProps{bold: header})
		d.body.WriteString("</w:p></w:tc>")
	}
	d.body.WriteString("</w:tr>")
}

// inlineChildren walks a node's inline children, emitting runs (and hyperlink
// wrappers) into b. rp is the formatting inherited from enclosing inline nodes.
func (d *docxBuilder) inlineChildren(b *strings.Builder, parent gast.Node, source []byte, rp runProps) {
	for c := parent.FirstChild(); c != nil; c = c.NextSibling() {
		switch node := c.(type) {
		case *gast.Text:
			writeRun(b, string(node.Segment.Value(source)), rp)
			if node.HardLineBreak() {
				b.WriteString("<w:r><w:br/></w:r>")
			} else if node.SoftLineBreak() {
				// A soft wrap in the source is a space between words, not a
				// break — same fix convert makes for <w:br> on the read side.
				writeRun(b, " ", rp)
			}
		case *gast.String:
			writeRun(b, string(node.Value), rp)
		case *gast.CodeSpan:
			rp2 := rp
			rp2.code = true
			writeRun(b, nodeText(node, source), rp2)
		case *gast.Emphasis:
			rp2 := rp
			if node.Level == 2 {
				rp2.bold = true
			} else {
				rp2.italic = true
			}
			d.inlineChildren(b, node, source, rp2)
		case *xast.Strikethrough:
			rp2 := rp
			rp2.strike = true
			d.inlineChildren(b, node, source, rp2)
		case *gast.Link:
			id := d.addRel(string(node.Destination))
			b.WriteString(fmt.Sprintf(`<w:hyperlink r:id="%s">`, id))
			rp2 := rp
			rp2.link = true
			d.inlineChildren(b, node, source, rp2)
			b.WriteString("</w:hyperlink>")
		case *gast.AutoLink:
			url := string(node.URL(source))
			id := d.addRel(url)
			b.WriteString(fmt.Sprintf(`<w:hyperlink r:id="%s">`, id))
			rp2 := rp
			rp2.link = true
			writeRun(b, url, rp2)
			b.WriteString("</w:hyperlink>")
		case *gast.Image:
			d.image(b, node, source, rp)
		case *gast.RawHTML:
			// Drop raw inline HTML (safe).
		default:
			if c.HasChildren() {
				d.inlineChildren(b, c, source, rp)
			} else {
				writeRun(b, nodeText(c, source), rp)
			}
		}
	}
}

// writeRun emits one <w:r> with the given formatting. xml:space="preserve" is
// mandatory: without it Word strips a run's trailing space, fusing "hello " and
// "world" from "hello **world**" into "helloworld".
func writeRun(b *strings.Builder, s string, rp runProps) {
	if s == "" {
		return
	}
	b.WriteString("<w:r>")
	// rPr is now unconditional because every run names its font. DOCX can only
	// NAME a font: embedding one in the OOXML package is out of scope, so Word
	// substitutes when the reader has not installed Inter. That is a stated
	// limitation, not an oversight — unlike the HTML/PDF path, which embeds the
	// woff2 outright (see fontFaceCSS in html.go).
	//
	// There is no styles.xml part in this package (see the file header), so a
	// document-level default is not available; per-run rFonts is the only place
	// the font can be declared without adding a fifth part.
	b.WriteString("<w:rPr>")
	if !rp.code {
		b.WriteString(`<w:rFonts w:ascii="Inter" w:hAnsi="Inter" w:cs="Inter"/>`)
	}
	{
		if rp.link {
			b.WriteString(`<w:rStyle w:val="Hyperlink"/>`)
		}
		if rp.bold {
			b.WriteString("<w:b/>")
		}
		if rp.italic {
			b.WriteString("<w:i/>")
		}
		if rp.strike {
			b.WriteString("<w:strike/>")
		}
		if rp.code {
			b.WriteString(`<w:rFonts w:ascii="Consolas" w:hAnsi="Consolas" w:cs="Consolas"/>`)
		}
		if rp.link {
			b.WriteString(`<w:color w:val="0563C1"/><w:u w:val="single"/>`)
		}
	}
	b.WriteString("</w:rPr>")
	b.WriteString(`<w:t xml:space="preserve">`)
	b.WriteString(escXML(s))
	b.WriteString("</w:t></w:r>")
}

// nodeText returns the concatenated plain text of a node and its descendants —
// used for unsupported nodes and for content (code spans, image alt text) whose
// value lives in child Text/String nodes.
func nodeText(n gast.Node, source []byte) string {
	var sb strings.Builder
	_ = gast.Walk(n, func(node gast.Node, entering bool) (gast.WalkStatus, error) {
		if !entering {
			return gast.WalkContinue, nil
		}
		switch t := node.(type) {
		case *gast.Text:
			sb.Write(t.Segment.Value(source))
		case *gast.String:
			sb.Write(t.Value)
		case *gast.AutoLink:
			sb.Write(t.URL(source))
		}
		return gast.WalkContinue, nil
	})
	return sb.String()
}

// firstIsHeading1 reports whether the document opens with a level-1 heading —
// in which case the note supplies its own title and ToDOCX must not prepend one.
func firstIsHeading1(root gast.Node) bool {
	first := root.FirstChild()
	if h, ok := first.(*gast.Heading); ok {
		return h.Level == 1
	}
	return false
}

// escXML escapes text for use inside an XML element. encoding/xml is the house
// tool for OOXML on the read side; xml.EscapeText is its write-side complement.
func escXML(s string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

// escXMLAttr escapes text for use inside an XML attribute value (hyperlink
// targets in document.xml.rels).
func escXMLAttr(s string) string {
	// xml.EscapeText handles quotes/&/</> which is sufficient for an attribute
	// value written between double quotes.
	return escXML(s)
}

// zip assembles the four OOXML parts into the final .docx byte stream.
func (d *docxBuilder) zip() ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	parts := []struct {
		name string
		data string
	}{
		{"[Content_Types].xml", d.contentTypesXML()},
		{"_rels/.rels", packageRelsXML},
		{"word/_rels/document.xml.rels", d.documentRelsXML()},
		{"word/document.xml", d.documentXML()},
	}
	for _, p := range parts {
		w, err := zw.Create(p.name)
		if err != nil {
			return nil, fmt.Errorf("export: docx: create %s: %w", p.name, err)
		}
		if _, err := w.Write([]byte(p.data)); err != nil {
			return nil, fmt.Errorf("export: docx: write %s: %w", p.name, err)
		}
	}
	// Media parts are binary, so they are written separately from the XML parts
	// above rather than being forced through a string.
	for _, m := range d.media {
		w, err := zw.Create("word/" + m.name)
		if err != nil {
			return nil, fmt.Errorf("export: docx: create media %s: %w", m.name, err)
		}
		if _, err := w.Write(m.data); err != nil {
			return nil, fmt.Errorf("export: docx: write media %s: %w", m.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("export: docx: finalize archive: %w", err)
	}
	return buf.Bytes(), nil
}

// documentXML wraps the accumulated body in the WordprocessingML document
// envelope, declaring the w: and r: namespaces the body relies on.
func (d *docxBuilder) documentXML() string {
	var sb strings.Builder
	sb.WriteString(xmlDecl)
	// wp: and a: are required by the <w:drawing> an embedded image emits. They
	// are declared here rather than on each drawing because an undeclared
	// prefix makes the document XML malformed, which Word reports as a corrupt
	// file rather than as a missing image.
	sb.WriteString(`<w:document ` +
		`xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" ` +
		`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" ` +
		`xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing" ` +
		`xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" ` +
		`xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture"` +
		`><w:body>`)
	sb.WriteString(d.body.String())
	sb.WriteString(`<w:sectPr><w:pgSz w:w="12240" w:h="15840"/><w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440" w:header="720" w:footer="720" w:gutter="0"/></w:sectPr>`)
	sb.WriteString(`</w:body></w:document>`)
	return sb.String()
}

// documentRelsXML builds word/_rels/document.xml.rels with one external-target
// relationship per collected hyperlink.
func (d *docxBuilder) documentRelsXML() string {
	var sb strings.Builder
	sb.WriteString(xmlDecl)
	sb.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for _, r := range d.rels {
		// TargetMode="External" is emitted for hyperlinks ONLY. An image
		// relationship carrying it makes Word look for the media part outside
		// the package, where it is not, and the picture renders as a missing
		// image box with no error.
		mode := ""
		if r.external {
			mode = ` TargetMode="External"`
		}
		sb.WriteString(fmt.Sprintf(
			`<Relationship Id="%s" Type="%s" Target="%s"%s/>`,
			r.id, r.relType, escXMLAttr(r.target), mode))
	}
	sb.WriteString(`</Relationships>`)
	return sb.String()
}

const xmlDecl = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"

// contentTypesXML declares a content type for every part in the package.
//
// It is built rather than constant now that images exist: OPC requires each
// extension present in the package to be declared, and Word rejects the whole
// file as unreadable when one is missing — it does not skip the offending part.
// Each image extension is emitted at most once.
func (d *docxBuilder) contentTypesXML() string {
	var sb strings.Builder
	sb.WriteString(xmlDecl)
	sb.WriteString(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	sb.WriteString(`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`)
	sb.WriteString(`<Default Extension="xml" ContentType="application/xml"/>`)

	seen := map[string]bool{}
	for _, m := range d.media {
		if seen[m.ext] {
			continue
		}
		seen[m.ext] = true
		fmt.Fprintf(&sb, `<Default Extension="%s" ContentType="image/%s"/>`, m.ext, m.ext)
	}

	sb.WriteString(`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>`)
	sb.WriteString(`</Types>`)
	return sb.String()
}

const packageRelsXML = xmlDecl + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
	`</Relationships>`
