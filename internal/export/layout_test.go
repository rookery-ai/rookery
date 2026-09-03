package export

import (
	"strings"
	"testing"

	gast "github.com/yuin/goldmark/ast"
)

// kinds returns the node kinds of a document's top-level children, which is the
// level the transformer operates at.
func kinds(root gast.Node) []string {
	var out []string
	for c := root.FirstChild(); c != nil; c = c.NextSibling() {
		out = append(out, c.Kind().String())
	}
	return out
}

// A columns wrapper collapses into ONE node holding its cells.
//
// goldmark hands back the opener and the closer as separate sibling HTMLBlocks
// with the body as real block nodes between them — measured, and the whole
// reason this approach works. Without the transformer those two HTMLBlocks
// render as `<!-- raw HTML omitted -->` and the cells stack.
func TestColumnsWrapperBecomesOneNode(t *testing.T) {
	root, _ := parseMarkdown([]byte("Intro.\n\n" +
		"<div data-cols=\"2\">\n\n" +
		"![a|100](a.png)\n\n" +
		"![b|100](b.png)\n\n" +
		"</div>\n"))

	got := kinds(root)
	want := []string{"Paragraph", kindColumns.String()}
	if len(got) != len(want) {
		t.Fatalf("top-level kinds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("top-level kinds = %v, want %v", got, want)
		}
	}

	node := root.LastChild().(*columnsNode)
	if node.Cols != 2 {
		t.Errorf("Cols = %d, want 2", node.Cols)
	}
	if n := node.ChildCount(); n != 2 {
		t.Errorf("the columns node holds %d children, want 2 (one per cell)", n)
	}
}

// An align wrapper does the same, carrying its alignment.
func TestAlignWrapperBecomesOneNode(t *testing.T) {
	root, _ := parseMarkdown([]byte("<div align=\"center\">\n\nCentred **bold**.\n\n</div>\n"))

	node, ok := root.FirstChild().(*alignNode)
	if !ok {
		t.Fatalf("first child is %s, want %s", root.FirstChild().Kind(), kindAlign)
	}
	if node.Align != "center" {
		t.Errorf("Align = %q, want %q", node.Align, "center")
	}
	if node.ChildCount() != 1 {
		t.Errorf("the align node holds %d children, want 1", node.ChildCount())
	}
}

// The two wrappers NEST — the editor produces align inside columns and it
// round-trips in both directions there — so the transformer must recurse into a
// node it has just built rather than scanning the document once.
func TestWrappersNest(t *testing.T) {
	root, _ := parseMarkdown([]byte("<div data-cols=\"2\">\n\n" +
		"<div align=\"center\">\n\n![a|100](a.png)\n\n</div>\n\n" +
		"Plain cell.\n\n" +
		"</div>\n"))

	cols, ok := root.FirstChild().(*columnsNode)
	if !ok {
		t.Fatalf("first child is %s, want %s", root.FirstChild().Kind(), kindColumns)
	}
	if cols.ChildCount() != 2 {
		t.Fatalf("outer node holds %d children, want 2", cols.ChildCount())
	}
	if _, ok := cols.FirstChild().(*alignNode); !ok {
		t.Errorf("the first cell is %s, want a nested %s", cols.FirstChild().Kind(), kindAlign)
	}
}

// An opener with no closer must leave the document ALONE.
//
// Degrading to today's behaviour is correct here; the alternative is swallowing
// the rest of the note into a wrapper its author never closed, which turns a
// cosmetic problem into a document that has visibly lost its ending.
func TestAnUnbalancedOpenerIsLeftAlone(t *testing.T) {
	root, _ := parseMarkdown([]byte("<div data-cols=\"2\">\n\nOne.\n\nTwo.\n"))

	for c := root.FirstChild(); c != nil; c = c.NextSibling() {
		if c.Kind() == kindColumns || c.Kind() == kindAlign {
			t.Fatalf("an unbalanced opener produced a %s node; it must be left as raw HTML", c.Kind())
		}
	}
}

// Every OTHER raw HTML block must still be dropped. This is the property that
// makes the transformer safe: we are not enabling raw HTML, we are recognising
// two known shapes and emitting our own markup for them.
func TestOtherRawHTMLIsStillDropped(t *testing.T) {
	out, err := ToHTML([]byte("<script>alert(1)</script>\n\n<div class=\"other\">\n\nx\n\n</div>\n"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "<script>") {
		t.Error("a script tag reached the exported document")
	}
	if strings.Contains(string(out), `class="other"`) {
		t.Error("an unrecognised div was rendered; only the two known wrappers may be")
	}
	if !strings.Contains(string(out), "raw HTML omitted") {
		t.Error("unrecognised raw HTML should still render as the omitted-comment placeholder")
	}
}

// Only 2..4 columns are legal in the editor, so a value outside that range is
// not a layout instruction and must not produce a grid.
func TestAnOutOfRangeColumnCountIsNotALayout(t *testing.T) {
	for _, spec := range []string{"1", "9", "0", "abc"} {
		root, _ := parseMarkdown([]byte("<div data-cols=\"" + spec + "\">\n\nx\n\n</div>\n"))
		if _, ok := root.FirstChild().(*columnsNode); ok {
			t.Errorf("data-cols=%q produced a columns node; only 2..4 are valid", spec)
		}
	}
}

// Likewise for an alignment that is not one of the three the editor emits.
func TestAnUnknownAlignmentIsNotALayout(t *testing.T) {
	root, _ := parseMarkdown([]byte("<div align=\"sideways\">\n\nx\n\n</div>\n"))
	if _, ok := root.FirstChild().(*alignNode); ok {
		t.Error("align=\"sideways\" produced an align node; only left/center/right are valid")
	}
}
