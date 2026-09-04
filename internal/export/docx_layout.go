package export

import (
	"fmt"
	"strings"

	gast "github.com/yuin/goldmark/ast"
)

// This file is the DOCX half of the export-fidelity work: embedded images, the
// grid, and alignment. Everything here walks the SAME AST the HTML renderer
// does, which is the property that lets one transformer fix all three formats.

// image emits an inline <w:drawing> for an image, storing its bytes as a media
// part.
//
// The source arrives as a data: URI, because web/api_kb.go's inlineVaultAssets
// has already rewritten every vault-relative image by this point — that is what
// makes an exported HTML file self-contained. Word cannot render a data URI, so
// the bytes are decoded back out and stored as a real part.
//
// Three ways an image is DEGRADED TO ITS ALT TEXT rather than embedded, all
// deliberate: a source that is not a data URI (an external http image — fetching
// it would turn an export into a network operation), a media type OOXML does not
// carry, and an image whose dimensions cannot be read. The last is the important
// one: DOCX requires an explicit extent in EMU, so an unreadable image would
// need a guessed aspect ratio, and a visibly stretched picture is worse than an
// absent one and much harder to attribute to the exporter.
func (d *docxBuilder) image(b *strings.Builder, node *gast.Image, source []byte, rp runProps) {
	altText, width := SplitAltWidth(string(node.Text(source)))

	mediaType, data, ok := dataURIPayload(string(node.Destination))
	if !ok {
		writeRun(b, altText, rp)
		return
	}
	ext, ok := imageExtension(mediaType)
	if !ok {
		writeRun(b, altText, rp)
		return
	}
	naturalW, naturalH, ok := imageDimensions(data)
	if !ok {
		writeRun(b, altText, rp)
		return
	}

	dispW, dispH := scaleToWidth(naturalW, naturalH, width)
	relID := d.addImage(ext, data)
	d.drawingSeq++

	// The alt text becomes the picture's name and description, which is what a
	// screen reader announces — the same text a sighted reader would have seen
	// had the image failed to load.
	name := altText
	if name == "" {
		name = fmt.Sprintf("Image %d", d.drawingSeq)
	}

	fmt.Fprintf(b,
		`<w:r><w:drawing><wp:inline distT="0" distB="0" distL="0" distR="0">`+
			`<wp:extent cx="%d" cy="%d"/><wp:effectExtent l="0" t="0" r="0" b="0"/>`+
			`<wp:docPr id="%d" name="%s" descr="%s"/>`+
			`<wp:cNvGraphicFramePr><a:graphicFrameLocks xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" noChangeAspect="1"/></wp:cNvGraphicFramePr>`+
			`<a:graphic xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">`+
			`<a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture">`+
			`<pic:pic xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture">`+
			`<pic:nvPicPr><pic:cNvPr id="%d" name="%s" descr="%s"/><pic:cNvPicPr/></pic:nvPicPr>`+
			`<pic:blipFill><a:blip r:embed="%s"/><a:stretch><a:fillRect/></a:stretch></pic:blipFill>`+
			`<pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="%d" cy="%d"/></a:xfrm>`+
			`<a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr>`+
			`</pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r>`,
		pixelsToEMU(dispW), pixelsToEMU(dispH),
		d.drawingSeq, escXMLAttr(name), escXMLAttr(altText),
		d.drawingSeq, escXMLAttr(name), escXMLAttr(altText),
		relID,
		pixelsToEMU(dispW), pixelsToEMU(dispH),
	)
}

// columns renders a grid as a BORDERLESS single-row table, one cell per child.
//
// Word has no CSS grid, and a table is the only construct that puts blocks side
// by side — which is also why a converter that maps the div to a div could never
// have produced a grid here, only stacked cells.
//
// Every border is explicitly `none`. A w:tbl with no tblBorders element does not
// inherit "no borders": Word applies the document default, so the layout would
// arrive with visible gridlines the author never asked for.
func (d *docxBuilder) columns(node *columnsNode, source []byte) {
	cells := make([]gast.Node, 0, node.Cols)
	for c := node.FirstChild(); c != nil; c = c.NextSibling() {
		cells = append(cells, c)
	}
	if len(cells) == 0 {
		return
	}

	// Widths are fiftieths of a percent (the w:pct unit), so the row totals
	// 5000. Integer division leaves a remainder on the last cell rather than
	// letting the row fall short of full width.
	share := 5000 / len(cells)

	d.body.WriteString(`<w:tbl><w:tblPr><w:tblW w:w="5000" w:type="pct"/><w:tblLayout w:type="fixed"/><w:tblBorders>`)
	for _, edge := range []string{"top", "left", "bottom", "right", "insideH", "insideV"} {
		fmt.Fprintf(&d.body, `<w:%s w:val="none" w:sz="0" w:space="0" w:color="auto"/>`, edge)
	}
	d.body.WriteString(`</w:tblBorders></w:tblPr><w:tr>`)

	for i, cell := range cells {
		w := share
		if i == len(cells)-1 {
			w = 5000 - share*(len(cells)-1)
		}
		fmt.Fprintf(&d.body, `<w:tc><w:tcPr><w:tcW w:w="%d" w:type="pct"/></w:tcPr>`, w)

		// A cell's content is rendered through the ordinary block path, so a
		// nested align wrapper or an image works exactly as it does outside a
		// grid.
		before := d.body.Len()
		d.block(cell, source, 0)
		if d.body.Len() == before {
			// A table cell with no paragraph makes Word treat the file as
			// damaged, so an empty cell still gets one.
			d.body.WriteString("<w:p/>")
		}
		d.body.WriteString(`</w:tc>`)
	}
	d.body.WriteString(`</w:tr></w:tbl>`)
	// A table immediately followed by another, or ending the document, needs a
	// separating paragraph or Word merges them.
	d.body.WriteString(`<w:p/>`)
}

// align renders an alignment wrapper by rendering its children and setting the
// justification on every paragraph produced.
//
// Word has no block-level alignment container — justification is a PARAGRAPH
// property — so this cannot wrap, it has to reach each paragraph. The children
// are rendered into a scratch buffer and their <w:p> openings rewritten, which
// keeps every block type working inside an aligned region without teaching each
// one about alignment.
func (d *docxBuilder) align(node *alignNode, source []byte) {
	saved := d.body
	d.body = strings.Builder{}
	for c := node.FirstChild(); c != nil; c = c.NextSibling() {
		d.block(c, source, 0)
	}
	inner := d.body.String()
	d.body = saved
	d.body.WriteString(applyJustification(inner, node.Align))
}

// applyJustification inserts a <w:jc> into every paragraph of a fragment.
//
// It handles both shapes a paragraph can arrive in: one that already opens a
// <w:pPr> (a heading, a quote) gains the justification inside it, and a bare
// <w:p> gains a <w:pPr> of its own. Getting the second case wrong is the
// tempting simplification — a <w:jc> outside <w:pPr> is invalid and Word
// reports the whole document as damaged rather than ignoring it.
func applyJustification(fragment, align string) string {
	jc := fmt.Sprintf(`<w:jc w:val="%s"/>`, docxAlignValue(align))

	var out strings.Builder
	rest := fragment
	for {
		i := strings.Index(rest, "<w:p>")
		j := strings.Index(rest, "<w:p ")
		// A <w:p/> self-closing empty paragraph has nothing to justify.
		switch {
		case i < 0 && j < 0:
			out.WriteString(rest)
			return out.String()
		case i >= 0 && (j < 0 || i < j):
			out.WriteString(rest[:i])
			rest = rest[i+len("<w:p>"):]
			if strings.HasPrefix(rest, "<w:pPr>") {
				out.WriteString("<w:p><w:pPr>" + jc)
				rest = rest[len("<w:pPr>"):]
			} else {
				out.WriteString("<w:p><w:pPr>" + jc + "</w:pPr>")
			}
		default:
			// "<w:p " with attributes: copy through to the end of the tag and
			// continue; these are not produced by this builder today, so the
			// safe action is to leave them untouched rather than guess.
			end := strings.Index(rest[j:], ">")
			if end < 0 {
				out.WriteString(rest)
				return out.String()
			}
			out.WriteString(rest[:j+end+1])
			rest = rest[j+end+1:]
		}
	}
}

// attachments renders the closing list of files the note links to.
//
// The same content the HTML path appends, so a reader who receives the Word
// version is told exactly what the web version would have told them. The path
// is shown as well as the name because a reader holding only this file cannot
// follow the link, and the path is what lets them ask for the right thing.
func (d *docxBuilder) attachments(list []Attachment) {
	if len(list) == 0 {
		return
	}
	d.body.WriteString(`<w:p><w:pPr><w:pBdr><w:top w:val="single" w:sz="6" w:space="1" w:color="auto"/></w:pBdr></w:pPr></w:p>`)
	d.body.WriteString(`<w:p><w:pPr><w:pStyle w:val="Heading2"/></w:pPr>`)
	writeRun(&d.body, "Attachments", runProps{})
	d.body.WriteString(`</w:p>`)

	for _, a := range list {
		d.body.WriteString(`<w:p>`)
		// A real hyperlink, so the link still works when the document travels
		// beside its uploads folder — the same behaviour as the HTML export.
		id := d.addRel(a.Path)
		fmt.Fprintf(&d.body, `<w:hyperlink r:id="%s">`, id)
		writeRun(&d.body, "• "+a.Name, runProps{link: true})
		d.body.WriteString(`</w:hyperlink>`)
		writeRun(&d.body, "  "+a.Path, runProps{code: true})
		d.body.WriteString(`</w:p>`)
	}
}

// docxAlignValue maps a CSS alignment to WordprocessingML's own vocabulary,
// which spells centre "center" and calls the edges "left"/"right".
func docxAlignValue(align string) string {
	switch align {
	case "center":
		return "center"
	case "right":
		return "right"
	default:
		return "left"
	}
}
