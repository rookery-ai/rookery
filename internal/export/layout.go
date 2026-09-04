package export

import (
	"regexp"
	"strconv"

	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// The KB editor expresses the two constructs markdown cannot — a grid and an
// alignment — as div wrappers around blank-line-separated markdown:
//
//	<div data-cols="2">
//
//	![before|420](before.png)
//
//	![after|420](after.png)
//
//	</div>
//
// goldmark runs WITHOUT html.WithUnsafe() here, deliberately, so those two
// lines are dropped as `<!-- raw HTML omitted -->` and the cells stack. That is
// the reported bug: a two-column layout of resized images exported as two
// full-size images, one above the other.
//
// The fix is a transformer rather than WithUnsafe, and the distinction is the
// whole point: **we never pass user HTML through.** We recognise exactly two
// shapes and emit our own markup from a fixed whitelist. Every other raw HTML
// block — a <script>, an arbitrary <div class> — still renders as the omitted
// placeholder, so the security property this repo states in two places is
// unchanged. TestOtherRawHTMLIsStillDropped pins that.
//
// This works at all because of a measured fact about goldmark: it parses the
// wrapper as a CommonMark type-6 HTML block that CLOSES at the blank line, so
// the opener and the closer arrive as SEPARATE sibling HTMLBlock nodes with the
// body between them as ordinary block nodes carrying real inline marks. The
// wrapper is addressable and the content is not trapped inside it. (markdown-it
// behaves the same way, which is why the editor's format round-trips there.)
//
// One transformation serves BOTH renderers, because the DOCX writer walks this
// same AST — which is what a converter-based approach could not have given us.

var (
	// The editor writes a canonical form, but a note can also be hand-edited or
	// written by an agent, so both patterns tolerate single or double quotes and
	// surrounding whitespace. They deliberately do NOT tolerate extra
	// attributes: a div carrying something we do not understand is not one of
	// our two shapes and must keep falling through to the omitted placeholder.
	columnsOpenRE = regexp.MustCompile(`^<div\s+data-cols=["']([^"']*)["']\s*>$`)
	alignOpenRE   = regexp.MustCompile(`^<div\s+align=["']([^"']*)["']\s*>$`)
	divCloseRE    = regexp.MustCompile(`^</div>$`)
)

// The editor's own bounds (web/ui/src/pages/kb/nodes/columns.ts). A value
// outside them is not a layout instruction, so it is left as raw HTML rather
// than clamped — clamping would silently render a grid the author did not ask
// for.
const (
	minColumns = 2
	maxColumns = 4
)

var kindColumns = gast.NewNodeKind("KBColumns")
var kindAlign = gast.NewNodeKind("KBAlign")

// columnsNode holds one cell per CHILD — there is no cell node, mirroring the
// editor's own shape (see nodes/columns.ts).
type columnsNode struct {
	gast.BaseBlock
	Cols int
}

func (*columnsNode) Kind() gast.NodeKind { return kindColumns }
func (n *columnsNode) Dump(src []byte, level int) {
	gast.DumpHelper(n, src, level, map[string]string{"cols": strconv.Itoa(n.Cols)}, nil)
}

// alignNode wraps blocks that are aligned as a group.
type alignNode struct {
	gast.BaseBlock
	Align string
}

func (*alignNode) Kind() gast.NodeKind { return kindAlign }
func (n *alignNode) Dump(src []byte, level int) {
	gast.DumpHelper(n, src, level, map[string]string{"align": n.Align}, nil)
}

// layoutTransformer rewrites the two wrapper shapes into the nodes above.
type layoutTransformer struct{}

func (t layoutTransformer) Transform(doc *gast.Document, reader text.Reader, _ parser.Context) {
	collapseWrappers(doc, reader.Source())
}

// collapseWrappers scans one node's children for a wrapper opener, finds its
// matching closer, and moves everything between into a new node.
//
// It recurses into each node it builds, because the two wrappers NEST — the
// editor produces an align inside a columns cell and that round-trips there —
// so a single pass over the document would leave the inner one as raw HTML.
func collapseWrappers(parent gast.Node, source []byte) {
	child := parent.FirstChild()
	for child != nil {
		next := child.NextSibling()

		block, ok := child.(*gast.HTMLBlock)
		if !ok {
			// A node that can itself contain blocks (a blockquote, a list item)
			// may hold a wrapper of its own.
			collapseWrappers(child, source)
			child = next
			continue
		}

		wrapper, matched := matchOpener(block, source)
		if !matched {
			child = next
			continue
		}
		closer := findCloser(child, source)
		if closer == nil {
			// An opener with no closer is left ALONE. Consuming the rest of the
			// document into a wrapper its author never closed would turn a
			// cosmetic defect into a document that has visibly lost its ending.
			child = next
			continue
		}

		// Move the nodes between opener and closer into the new wrapper. The
		// list is collected first: RemoveChild rewires the very links being
		// walked, so iterating and mutating together drops every other node.
		var inner []gast.Node
		for n := child.NextSibling(); n != nil && n != closer; n = n.NextSibling() {
			inner = append(inner, n)
		}
		for _, n := range inner {
			parent.RemoveChild(parent, n)
			wrapper.AppendChild(wrapper, n)
		}

		parent.InsertBefore(parent, child, wrapper)
		parent.RemoveChild(parent, child)  // the opener
		parent.RemoveChild(parent, closer) // the closer

		// Recurse into what we just built, for the nested case.
		collapseWrappers(wrapper, source)

		child = wrapper.NextSibling()
	}
}

// matchOpener reports whether an HTML block is one of our two wrapper openers,
// returning the node to replace it with.
func matchOpener(block *gast.HTMLBlock, source []byte) (gast.Node, bool) {
	line := htmlBlockText(block, source)

	if m := columnsOpenRE.FindStringSubmatch(line); m != nil {
		cols, err := strconv.Atoi(m[1])
		if err != nil || cols < minColumns || cols > maxColumns {
			return nil, false
		}
		return &columnsNode{Cols: cols}, true
	}
	if m := alignOpenRE.FindStringSubmatch(line); m != nil {
		switch m[1] {
		case "left", "center", "right":
			return &alignNode{Align: m[1]}, true
		}
		return nil, false
	}
	return nil, false
}

// findCloser returns the sibling `</div>` that closes the wrapper opened at
// start, honouring nesting so an inner wrapper's closer is not mistaken for the
// outer one's.
func findCloser(start gast.Node, source []byte) gast.Node {
	depth := 0
	for n := start.NextSibling(); n != nil; n = n.NextSibling() {
		block, ok := n.(*gast.HTMLBlock)
		if !ok {
			continue
		}
		line := htmlBlockText(block, source)
		switch {
		case divCloseRE.MatchString(line):
			if depth == 0 {
				return n
			}
			depth--
		case columnsOpenRE.MatchString(line) || alignOpenRE.MatchString(line):
			depth++
		}
	}
	return nil
}

// htmlBlockText returns an HTML block's source text, trimmed.
//
// A block's content lives in its Lines segments rather than in the node, and a
// type-6 block that ended at a blank line has no closure line — so a naive read
// of ClosureLine on every block would append stray bytes.
func htmlBlockText(block *gast.HTMLBlock, source []byte) string {
	var buf []byte
	for i := 0; i < block.Lines().Len(); i++ {
		// Assigned to a variable because Segment.Value has a POINTER receiver
		// and At returns a value, so the result is not addressable inline.
		seg := block.Lines().At(i)
		buf = append(buf, seg.Value(source)...)
	}
	if block.HasClosure() {
		closure := block.ClosureLine
		buf = append(buf, closure.Value(source)...)
	}
	return string(util.TrimRightSpace(util.TrimLeftSpace(buf)))
}
