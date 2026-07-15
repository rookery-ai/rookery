package render

import (
	"strconv"
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

// escapeMDV2Link escapes the characters special inside a MarkdownV2 link
// destination: backslash and the closing paren.
func escapeMDV2Link(s string) string {
	return strings.NewReplacer("\\", "\\\\", ")", "\\)").Replace(s)
}

// writeCodeBlockTelegram emits a ```-fenced MarkdownV2 code block from raw source lines.
func writeCodeBlockTelegram(b *strings.Builder, lines *text.Segments, src []byte) {
	var body strings.Builder
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		body.WriteString(string(seg.Value(src)))
	}
	b.WriteString("```\n")
	b.WriteString(escapeMDV2Code(strings.TrimRight(body.String(), "\n")))
	b.WriteString("\n```\n\n")
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
		b.WriteString(escapeMDV2Link(string(node.Destination))) // URL: MarkdownV2 needs only ) and \ escaped
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
	case *ast.FencedCodeBlock:
		writeCodeBlockTelegram(b, node.Lines(), src)
		return
	case *ast.CodeBlock:
		writeCodeBlockTelegram(b, node.Lines(), src)
		return
	case *ast.List:
		i := 1
		for li := node.FirstChild(); li != nil; li = li.NextSibling() {
			if node.IsOrdered() {
				b.WriteString(escapeMDV2Text(strconv.Itoa(i)) + "\\. ")
			} else {
				b.WriteString("• ")
			}
			renderChildrenTelegram(b, li, src) // ListItem children: TextBlock/Paragraph
			b.WriteString("\n")
			i++
		}
		b.WriteString("\n")
		return
	case *ast.Image:
		renderChildrenTelegram(b, node, src) // emit alt text (its inline children)
		return
	}
	renderChildrenTelegram(b, n, src)
}

func renderChildrenTelegram(b *strings.Builder, n ast.Node, src []byte) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		renderNodeTelegram(b, c, src)
	}
}
