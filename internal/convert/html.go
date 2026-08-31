package convert

import (
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// htmlToMarkdown converts an HTML document to markdown using a real parser.
// The previous approach (four regexes over the raw source) collapsed an entire
// page — nav, cookie banner, body, footer — into one whitespace-run with no
// structure. A parse tree lets us do the two things that actually matter for
// model context: drop chrome, and preserve headings, lists, links and tables.
func htmlToMarkdown(data []byte, opt Options) (Result, error) {
	doc, err := html.Parse(strings.NewReader(string(data)))
	if err != nil {
		return Result{}, fmt.Errorf("convert: parse html: %w", err)
	}
	res := Result{Kind: KindHTML, Extractor: "pure-go"}
	res.Title = strings.TrimSpace(textOf(findFirst(doc, atom.Title)))
	if res.Title == "" {
		res.Title = titleFromFilename(opt.Filename)
	}

	// Prefer the semantic content root when the page provides one; a page that
	// marks up <main> or <article> is telling us exactly where the content is.
	root := findFirst(doc, atom.Main)
	if root == nil {
		root = findFirst(doc, atom.Article)
	}
	if root == nil {
		root = findFirst(doc, atom.Body)
	}
	if root == nil {
		root = doc
	}

	var w mdWriter
	w.walk(root)
	body := normalizeText(collapseBlankLines(w.sb.String()))

	if strings.TrimSpace(body) == "" {
		body = "(no readable text content)\n"
		res.Warnings = append(res.Warnings, "no readable text extracted from HTML")
	}
	res.Markdown = body
	return res, nil
}

// skipTags are elements whose subtree is never content: page chrome, and code
// the browser executes rather than displays.
var skipTags = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Noscript: true, atom.Template: true,
	atom.Nav: true, atom.Header: true, atom.Footer: true, atom.Aside: true,
	atom.Form: true, atom.Svg: true,
}

// mdWriter accumulates markdown while walking the parse tree.
type mdWriter struct {
	sb           strings.Builder
	pendingSpace bool // previous fragment ended on whitespace; next emit needs a separator
	// listStack is one frame per open <ul>/<ol>, innermost last. It carries what
	// a list item needs and an <li> cannot see for itself: whether to write "-"
	// or a number, which number, and how deep to indent. Without it every list
	// was emitted as a flat sequence of "- " items, so an ordered list lost its
	// numbering and a nested list lost its nesting.
	listStack []*listFrame
}

// listFrame tracks one open list level.
type listFrame struct {
	ordered bool
	// n counts items emitted at this level, so an ordered list numbers 1., 2.,
	// 3. rather than repeating a marker.
	n int
}

// marker returns the bullet or number for the next item at this level and
// advances the counter.
func (f *listFrame) marker() string {
	f.n++
	if f.ordered {
		return fmt.Sprintf("%d. ", f.n)
	}
	return "- "
}

// listIndent is the indentation for the CURRENT nesting depth. Two spaces per
// enclosing level is what the editor's serializer emits and therefore what
// round-trips; a tab or four spaces would be re-parsed as a code block.
func (w *mdWriter) listIndent() string {
	if len(w.listStack) <= 1 {
		return ""
	}
	return strings.Repeat("  ", len(w.listStack)-1)
}

func (w *mdWriter) walk(n *html.Node) {
	if n == nil {
		return
	}
	if n.Type == html.TextNode {
		w.text(n.Data)
		return
	}
	if n.Type != html.ElementNode && n.Type != html.DocumentNode {
		return
	}
	if skipTags[n.DataAtom] {
		return
	}

	switch n.DataAtom {
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		level := int(n.Data[1] - '0')
		w.block()
		w.sb.WriteString(strings.Repeat("#", level) + " " + EscapeInline(squeeze(textOf(n))))
		w.block()
		return
	case atom.Blockquote:
		// Blockquote used to share a case with <p>, so it emitted no ">" at all
		// and a quotation was indistinguishable from the prose around it. The
		// editor supports blockquotes (and callouts, which are a blockquote with
		// a marker), so this is a construct it can represent and edit.
		w.block()
		var inner mdWriter
		inner.children(n)
		w.sb.WriteString(quotePrefix(strings.TrimSpace(inner.sb.String())))
		w.block()
		return
	case atom.Details:
		w.details(n)
		return
	case atom.Span:
		// Only a span carrying a colour is a construct the editor can hold; any
		// other span is presentational and falls through to its contents.
		if w.span(n) {
			return
		}
		w.children(n)
		return
	case atom.U, atom.Ins:
		// The editor has a real underline mark whose serialized form is literally
		// <u>…</u>, so this round-trips and stays editable.
		w.inlineHTML("u", n)
		return
	case atom.P, atom.Section:
		w.block()
		w.children(n)
		w.block()
		return
	case atom.Div:
		// A <div> carrying an alignment is the editor's kbAlign node, whose
		// serialized form is exactly this wrapper with a blank line inside.
		if a := alignmentOf(n); a != "" {
			w.block()
			w.sb.WriteString(`<div align="` + a + "\">\n\n")
			w.children(n)
			w.block()
			w.sb.WriteString("</div>")
			w.block()
			return
		}
		w.block()
		w.children(n)
		w.block()
		return
	case atom.Br:
		// Emit a CommonMark HARD break (backslash + newline), not a bare "\n".
		// A bare newline inside a paragraph is a SOFT break that the KB editor's
		// round-trip collapses to a space — which made converted documents with
		// <br> line breaks fail the fidelity check and open in raw mode. The
		// backslash form round-trips faithfully (a trailing-space hard break
		// would be stripped by the editor's whitespace normalization).
		w.sb.WriteString("\\\n")
		return
	case atom.Hr:
		w.block()
		w.sb.WriteString("---")
		w.block()
		return
	case atom.Ul, atom.Ol:
		// <ul>/<ol> previously had NO case at all, so only <li> was handled and
		// every list — ordered or not, nested or not — came out as a flat run of
		// "- " items. A numbered procedure imported as an unnumbered one.
		w.block()
		w.listStack = append(w.listStack, &listFrame{ordered: n.DataAtom == atom.Ol})
		w.children(n)
		w.listStack = w.listStack[:len(w.listStack)-1]
		w.block()
		return
	case atom.Li:
		// An <li> outside any list still has to render; a synthetic bullet frame
		// keeps it a list item rather than dropping its marker.
		if len(w.listStack) == 0 {
			w.listStack = append(w.listStack, &listFrame{})
			defer func() { w.listStack = w.listStack[:len(w.listStack)-1] }()
		}
		frame := w.listStack[len(w.listStack)-1]
		// A single newline, not a blank line: the items of one list are a single
		// block, and separating them with blank lines makes the list LOOSE,
		// which the editor's serializer then rewrites as tight.
		w.lineBreak()
		w.sb.WriteString(w.listIndent() + frame.marker())
		w.children(n)
		w.sb.WriteString("\n")
		return
	case atom.Strong, atom.B:
		w.inline("**", n)
		return
	case atom.Em, atom.I:
		w.inline("*", n)
		return
	case atom.Code:
		w.inline("`", n)
		return
	case atom.Pre:
		w.block()
		// A fixed "```" fence breaks the moment <pre> content itself contains a
		// line of three (or more) backticks — the closing fence would be matched
		// early, dumping the rest as loose markdown plus a stray fence. codeFence
		// sizes the fence past any run already present (see convert.go's JSON
		// branch for the identical flaw this mirrors).
		body := strings.TrimSpace(textOf(n))
		fence := codeFence(body)
		w.sb.WriteString(fence + codeLanguage(n) + "\n" + body + "\n" + fence)
		w.block()
		return
	case atom.A:
		raw := textOf(n)
		leading, trailing := hasEdgeSpace(raw)
		text := squeeze(raw)
		if text == "" {
			return
		}
		href := attr(n, "href")
		if href == "" || blockedHref(href) {
			w.emit(EscapeInline(text), leading)
			w.pendingSpace = trailing
			return
		}
		// The label is prose and takes the inline rules; the destination is a
		// path and takes its own (see escapeDestination) — inline-escaping a URL
		// would turn "&" into an entity and break the link.
		w.emit(fmt.Sprintf("[%s](%s)", EscapeInline(text), escapeDestination(href)), leading)
		w.pendingSpace = trailing
		return
	case atom.Table:
		w.block()
		w.table(n)
		w.block()
		return
	case atom.Img:
		// The alt text does NOT gate emission. This used to read
		// `if alt := …; alt != ""`, wrapping the whole case — so an <img> with
		// no alt attribute emitted nothing at all, src included, and the image
		// vanished from the note with no warning. Empty alt is the common case
		// in real pages and mail, so that silently dropped most imported
		// images; an image with no description is still an image.
		alt := strings.TrimSpace(attr(n, "alt"))
		src := attr(n, "src")
		if src == "" || blockedImageSrc(src) {
			// A blocked source keeps the alt text as plain prose, matching how
			// a blocked href keeps its link text: the destination is what is
			// unsafe, not the words describing it. With no alt there is nothing
			// to say, so nothing is written.
			if alt != "" {
				w.emit(EscapeInline(alt), false)
			}
			return
		}
		w.emit(fmt.Sprintf("![%s](%s)", EscapeInline(alt), escapeDestination(src)), false)
		return
	}
	w.children(n)
}

func (w *mdWriter) children(n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		w.walk(c)
	}
}

func (w *mdWriter) inline(marker string, n *html.Node) {
	raw := textOf(n)
	leading, trailing := hasEdgeSpace(raw)
	text := squeeze(raw)
	if text == "" {
		return
	}
	// Inside a code span the content is literal — the editor's serializer writes
	// that mark with escaping disabled, so escaping here would put visible
	// backslashes into the user's code. Emphasis content is ordinary prose and
	// takes the inline rules like any other text.
	if marker != "`" {
		text = EscapeInline(text)
	}
	w.emit(marker+text+marker, leading)
	w.pendingSpace = trailing
}

// text writes a text node. Whitespace at a fragment's boundaries is the only
// record HTML keeps that two words are separate, and squeeze() discards it —
// so capture it first and re-apply it as an explicit separator.
func (w *mdWriter) text(s string) {
	leading, trailing := hasEdgeSpace(s)
	sq := squeeze(s)
	if sq == "" {
		// A whitespace-only node still separates the spans on either side of it.
		if leading || trailing {
			w.pendingSpace = true
		}
		return
	}
	esc := EscapeInline(sq)
	// A hyphen is only list syntax at the start of a block, so the check is made
	// here — where the writer knows whether anything precedes it on this line —
	// rather than inside EscapeInline, which sees a bare string and could not
	// tell "-40 degrees" (prose) from a hyphen inside a sentence.
	if w.atBlockStart() {
		esc = escapeLeadingMarker(esc)
	}
	w.emit(esc, leading)
	w.pendingSpace = trailing
}

// atBlockStart reports whether the next fragment would begin a line, which is
// the only position where a leading "-" would be misread as a bullet.
func (w *mdWriter) atBlockStart() bool {
	cur := w.sb.String()
	return cur == "" || strings.HasSuffix(cur, "\n")
}

// emit appends a fragment, inserting a single separator when the previous
// fragment ended on whitespace or this one began on it. Every writer — text
// and inline element alike — goes through here, which is what keeps an inline
// span from fusing with the prose around it.
func (w *mdWriter) emit(s string, leadingSpace bool) {
	cur := w.sb.String()
	if (w.pendingSpace || leadingSpace) && cur != "" &&
		!strings.HasSuffix(cur, "\n") && !strings.HasSuffix(cur, " ") {
		w.sb.WriteString(" ")
	}
	w.pendingSpace = false
	w.sb.WriteString(s)
}

// hasEdgeSpace reports whether s begins and/or ends with whitespace.
func hasEdgeSpace(s string) (leading, trailing bool) {
	if s == "" {
		return false, false
	}
	return unicode.IsSpace(rune(s[0])), unicode.IsSpace(rune(s[len(s)-1]))
}

// lineBreak ensures the output is at the start of a line WITHOUT forcing a
// blank one. List items need this: block() would separate them with a blank
// line, making the list loose, which the editor then rewrites as tight — a
// difference that opens the note read-only.
func (w *mdWriter) lineBreak() {
	w.pendingSpace = false
	cur := w.sb.String()
	if cur == "" || strings.HasSuffix(cur, "\n") {
		return
	}
	w.sb.WriteString("\n")
}

// block ensures the output is at a blank-line boundary before the next block.
func (w *mdWriter) block() {
	w.pendingSpace = false
	cur := w.sb.String()
	if cur == "" {
		return
	}
	if !strings.HasSuffix(cur, "\n\n") {
		if strings.HasSuffix(cur, "\n") {
			w.sb.WriteString("\n")
		} else {
			w.sb.WriteString("\n\n")
		}
	}
}

// table renders a <table> as a markdown table. The first row becomes the header
// (whether it uses <th> or <td>), which is what nearly every real table means.
func (w *mdWriter) table(n *html.Node) {
	var rows [][]string
	var collect func(*html.Node)
	collect = func(node *html.Node) {
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			if c.DataAtom == atom.Tr {
				var cells []string
				for cell := c.FirstChild; cell != nil; cell = cell.NextSibling {
					if cell.DataAtom == atom.Td || cell.DataAtom == atom.Th {
						cells = append(cells, squeeze(textOf(cell)))
					}
				}
				if len(cells) > 0 {
					rows = append(rows, cells)
				}
				continue
			}
			collect(c)
		}
	}
	collect(n)
	if len(rows) == 0 {
		return
	}
	// Use the package-level writeRow (tabular.go), which escapes a literal "|"
	// and scrubs embedded newlines in each cell. The previous local closure
	// here did neither, so an HTML cell containing a pipe (e.g. a price range
	// like "50 | 100") silently split into an extra column and misaligned the
	// whole rendered table — the same bug class escapeCell exists to prevent.
	writeRow(&w.sb, rows[0])
	sep := make([]string, len(rows[0]))
	for i := range sep {
		sep[i] = "---"
	}
	writeRow(&w.sb, sep)
	for _, r := range rows[1:] {
		writeRow(&w.sb, r)
	}
}

// findFirst returns the first element with the given tag, depth-first.
func findFirst(n *html.Node, a atom.Atom) *html.Node {
	if n == nil {
		return nil
	}
	if n.Type == html.ElementNode && n.DataAtom == a {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findFirst(c, a); found != nil {
			return found
		}
	}
	return nil
}

// textOf returns the concatenated text of a subtree, skipping chrome elements.
func textOf(n *html.Node) string {
	if n == nil {
		return ""
	}
	var sb strings.Builder
	var rec func(*html.Node)
	rec = func(node *html.Node) {
		if node.Type == html.TextNode {
			sb.WriteString(node.Data)
			return
		}
		if node.Type == html.ElementNode && skipTags[node.DataAtom] {
			return
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			rec(c)
		}
	}
	rec(n)
	return sb.String()
}

func attr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

// squeeze collapses all runs of whitespace to single spaces and trims.
func squeeze(s string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

// collapseBlankLines reduces runs of 3+ newlines to exactly two, so block
// handling that over-produces separators still yields clean markdown.
func collapseBlankLines(s string) string {
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return s
}
