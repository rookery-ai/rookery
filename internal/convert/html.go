package convert

import (
	"fmt"
	"strings"

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
	sb       strings.Builder
	listItem bool // currently inside an <li>, so text is emitted after a bullet
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
		w.listItem = true
		w.children(n)
		w.listItem = false
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
		w.sb.WriteString("```\n" + strings.TrimSpace(textOf(n)) + "\n```")
		w.block()
		return
	case atom.A:
		text := squeeze(textOf(n))
		href := attr(n, "href")
		if text == "" {
			return
		}
		if href == "" || strings.HasPrefix(href, "javascript:") {
			w.sb.WriteString(text)
			return
		}
		fmt.Fprintf(&w.sb, "[%s](%s)", text, href)
		return
	case atom.Table:
		w.block()
		w.table(n)
		w.block()
		return
	case atom.Img:
		if alt := strings.TrimSpace(attr(n, "alt")); alt != "" {
			fmt.Fprintf(&w.sb, "![%s](%s)", alt, attr(n, "src"))
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
	text := squeeze(textOf(n))
	if text == "" {
		return
	}
	w.sb.WriteString(marker + text + marker)
}

// text writes a text node, collapsing whitespace. Leading whitespace directly
// after a block break is dropped so paragraphs do not start with a space.
func (w *mdWriter) text(s string) {
	s = squeeze(s)
	if s == "" {
		return
	}
	cur := w.sb.String()
	if cur != "" && !strings.HasSuffix(cur, "\n") && !strings.HasSuffix(cur, " ") &&
		!strings.HasPrefix(s, " ") {
		w.sb.WriteString(" ")
	}
	w.sb.WriteString(s)
}

// block ensures the output is at a blank-line boundary before the next block.
func (w *mdWriter) block() {
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
	writeRow := func(cells []string) {
		w.sb.WriteString("| " + strings.Join(cells, " | ") + " |\n")
	}
	writeRow(rows[0])
	sep := make([]string, len(rows[0]))
	for i := range sep {
		sep[i] = "---"
	}
	writeRow(sep)
	for _, r := range rows[1:] {
		writeRow(r)
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
