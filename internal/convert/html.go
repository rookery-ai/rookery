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
		w.sb.WriteString(strings.Repeat("#", level) + " " + squeeze(textOf(n)))
		w.block()
		return
	case atom.P, atom.Div, atom.Section, atom.Blockquote:
		w.block()
		w.children(n)
		w.block()
		return
	case atom.Br:
		w.sb.WriteString("\n")
		return
	case atom.Hr:
		w.block()
		w.sb.WriteString("---")
		w.block()
		return
	case atom.Li:
		w.block()
		w.sb.WriteString("- ")
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
		w.sb.WriteString(fence + "\n" + body + "\n" + fence)
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
		if href == "" || strings.HasPrefix(href, "javascript:") {
			w.emit(text, leading)
			w.pendingSpace = trailing
			return
		}
		w.emit(fmt.Sprintf("[%s](%s)", text, href), leading)
		w.pendingSpace = trailing
		return
	case atom.Table:
		w.block()
		w.table(n)
		w.block()
		return
	case atom.Img:
		if alt := strings.TrimSpace(attr(n, "alt")); alt != "" {
			w.emit(fmt.Sprintf("![%s](%s)", alt, attr(n, "src")), false)
		}
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
	w.emit(sq, leading)
	w.pendingSpace = trailing
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
