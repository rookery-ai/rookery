package render

import (
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

func init() { Register("slack", RendererFunc(RenderSlack)) }

// escapeSlackText HTML-escapes the three characters Slack mrkdwn reserves in text.
func escapeSlackText(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// writeCodeBlockSlack emits a ```-fenced Slack mrkdwn code block from raw source lines.
func writeCodeBlockSlack(b *strings.Builder, lines *text.Segments, src []byte) {
	var body strings.Builder
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		body.WriteString(string(seg.Value(src)))
	}
	b.WriteString("```\n")
	b.WriteString(strings.TrimRight(body.String(), "\n"))
	b.WriteString("\n```\n\n")
}

// RenderSlack converts neutral CommonMark to Slack mrkdwn.
func RenderSlack(commonMark string) string {
	src := []byte(commonMark)
	root := goldmark.DefaultParser().Parse(text.NewReader(src))
	var b strings.Builder
	renderNodeSlack(&b, root, src)
	return strings.TrimRight(b.String(), "\n")
}

func renderNodeSlack(b *strings.Builder, n ast.Node, src []byte) {
	switch node := n.(type) {
	case *ast.Text:
		b.WriteString(escapeSlackText(string(node.Segment.Value(src))))
		if node.SoftLineBreak() || node.HardLineBreak() {
			b.WriteByte('\n')
		}
		return
	case *ast.String:
		b.WriteString(escapeSlackText(string(node.Value)))
		return
	case *ast.RawHTML:
		for i := 0; i < node.Segments.Len(); i++ {
			seg := node.Segments.At(i)
			b.WriteString(escapeSlackText(string(seg.Value(src))))
		}
		return
	case *ast.AutoLink:
		u := string(node.URL(src))
		b.WriteString("<" + u + ">")
		return
	case *ast.CodeSpan:
		b.WriteByte('`')
		b.WriteString(string(node.Text(src)))
		b.WriteByte('`')
		return
	case *ast.Emphasis:
		delim := "_" // italic (Level 1)
		if node.Level == 2 {
			delim = "*" // bold (single star in mrkdwn)
		}
		b.WriteString(delim)
		renderChildrenSlack(b, node, src)
		b.WriteString(delim)
		return
	case *ast.Link:
		b.WriteString("<" + string(node.Destination) + "|")
		renderChildrenSlack(b, node, src)
		b.WriteString(">")
		return
	case *ast.Paragraph:
		renderChildrenSlack(b, node, src)
		if _, ok := node.NextSibling().(*ast.List); ok {
			b.WriteString("\n")
		} else {
			b.WriteString("\n\n")
		}
		return
	case *ast.Heading:
		b.WriteByte('*')
		renderChildrenSlack(b, node, src)
		b.WriteString("*\n\n")
		return
	case *ast.FencedCodeBlock:
		writeCodeBlockSlack(b, node.Lines(), src)
		return
	case *ast.CodeBlock:
		writeCodeBlockSlack(b, node.Lines(), src)
		return
	case *ast.List:
		i := node.Start
		if i == 0 {
			i = 1
		}
		for li := node.FirstChild(); li != nil; li = li.NextSibling() {
			if node.IsOrdered() {
				b.WriteString(strconv.Itoa(i) + ". ")
			} else {
				b.WriteString("• ")
			}
			renderChildrenSlack(b, li, src)
			b.WriteString("\n")
			i++
		}
		b.WriteString("\n")
		return
	case *ast.Image:
		renderChildrenSlack(b, node, src)
		return
	}
	renderChildrenSlack(b, n, src)
}

func renderChildrenSlack(b *strings.Builder, n ast.Node, src []byte) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		renderNodeSlack(b, c, src)
	}
}
