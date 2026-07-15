package render

import (
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

func init() { Register("telegram", RendererFunc(RenderTelegram)) }

// mdV2Special are the characters MarkdownV2 requires escaping in text nodes.
const mdV2Special = "_*[]()~`>#+-=|{}.!"

// escapeMDV2Text backslash-escapes every MarkdownV2-special rune in plain text.
func escapeMDV2Text(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(mdV2Special, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// escapeMDV2Code escapes only backslash and backtick (code-span content rules).
func escapeMDV2Code(s string) string {
	r := strings.NewReplacer("\\", "\\\\", "`", "\\`")
	return r.Replace(s)
}

// RenderTelegram converts neutral CommonMark to Telegram MarkdownV2.
func RenderTelegram(commonMark string) string {
	src := []byte(commonMark)
	root := goldmark.DefaultParser().Parse(text.NewReader(src))
	var b strings.Builder
	renderNodeTelegram(&b, root, src)
	return strings.TrimRight(b.String(), "\n")
}

func renderNodeTelegram(b *strings.Builder, n ast.Node, src []byte) {
	switch node := n.(type) {
	case *ast.Text:
		b.WriteString(escapeMDV2Text(string(node.Segment.Value(src))))
		if node.SoftLineBreak() || node.HardLineBreak() {
			b.WriteByte('\n')
		}
		return
	case *ast.String:
		b.WriteString(escapeMDV2Text(string(node.Value)))
		return
	case *ast.RawHTML:
		// Angle-bracket runs like "<name>" parse as raw inline HTML in CommonMark.
		// Emit their literal source text, MarkdownV2-escaped (so ">" becomes "\>").
		for i := 0; i < node.Segments.Len(); i++ {
			seg := node.Segments.At(i)
			b.WriteString(escapeMDV2Text(string(seg.Value(src))))
		}
		return
	case *ast.AutoLink:
		b.WriteString(escapeMDV2Text(string(node.URL(src))))
		return
	case *ast.CodeSpan:
		b.WriteByte('`')
		b.WriteString(escapeMDV2Code(string(node.Text(src))))
		b.WriteByte('`')
		return
	case *ast.Emphasis:
		delim := "_" // italic (Level 1)
		if node.Level == 2 {
			delim = "*" // bold
		}
		b.WriteString(delim)
		renderChildrenTelegram(b, node, src)
		b.WriteString(delim)
		return
	case *ast.Link:
		b.WriteByte('[')
		renderChildrenTelegram(b, node, src)
		b.WriteString("](")
		b.WriteString(string(node.Destination)) // URL: MarkdownV2 needs only ) and \ escaped
		b.WriteByte(')')
		return
	case *ast.Paragraph:
		renderChildrenTelegram(b, node, src)
		b.WriteString("\n\n")
		return
	case *ast.Heading:
		// No heading syntax in MarkdownV2: bold the line.
		b.WriteByte('*')
		renderChildrenTelegram(b, node, src)
		b.WriteString("*\n\n")
		return
	}
	renderChildrenTelegram(b, n, src)
}

func renderChildrenTelegram(b *strings.Builder, n ast.Node, src []byte) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		renderNodeTelegram(b, c, src)
	}
}
