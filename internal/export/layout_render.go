package export

import (
	"fmt"
	"html"

	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
)

// layoutRendererOption registers HTML renderers for the two layout nodes and
// for images, whose width the default renderer cannot know about.
func layoutRendererOption() interface {
	SetConfig(*renderer.Config)
} {
	return renderer.WithNodeRenderers(util.Prioritized(&layoutHTMLRenderer{}, 100))
}

type layoutHTMLRenderer struct{}

func (r *layoutHTMLRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindColumns, r.renderColumns)
	reg.Register(kindAlign, r.renderAlign)
	// Images are re-registered rather than left to goldmark's default, which
	// has no notion of the width the editor stores in the alt slot.
	reg.Register(gast.KindImage, r.renderImage)
}

// renderColumns emits a real CSS grid.
//
// grid-template-columns uses minmax(0, 1fr) and NEVER a bare 1fr. A grid item's
// automatic minimum size is content-based (CSS Grid §6.6), so a bare 1fr lets
// one wide cell — an unwrapped image, a long code span — stretch its track and
// push the others out of the page. This repo has recorded the same trap twice
// already, in DialogContent and PageContainer.
func (r *layoutHTMLRenderer) renderColumns(w util.BufWriter, _ []byte, node gast.Node, entering bool) (gast.WalkStatus, error) {
	n := node.(*columnsNode)
	if entering {
		fmt.Fprintf(w, `<div class="kb-columns" style="display:grid;grid-template-columns:repeat(%d,minmax(0,1fr));gap:1rem;align-items:start">`, n.Cols)
	} else {
		_, _ = w.WriteString("</div>\n")
	}
	return gast.WalkContinue, nil
}

func (r *layoutHTMLRenderer) renderAlign(w util.BufWriter, _ []byte, node gast.Node, entering bool) (gast.WalkStatus, error) {
	n := node.(*alignNode)
	if entering {
		// The alignment value is one of three literals matched by alignOpenRE,
		// never free text from the note, so it cannot carry markup here.
		fmt.Fprintf(w, `<div class="kb-align" style="text-align:%s">`, n.Align)
	} else {
		_, _ = w.WriteString("</div>\n")
	}
	return gast.WalkContinue, nil
}

// renderImage honours the width the editor stores in the alt slot.
//
// The editor serialises a resized image as `![alt|420](src)` (Obsidian's form),
// and nothing on this side had ever split it — so every exported image came out
// at natural size with the literal "|420" sitting in its alt text as visible
// noise. That is half the reported bug.
//
// The width ATTRIBUTE plus max-width:100% is deliberate as a pair: the
// attribute honours the author's chosen size, and the CSS lets an image wider
// than the page scale DOWN without ever scaling up past what they asked for.
func (r *layoutHTMLRenderer) renderImage(w util.BufWriter, source []byte, node gast.Node, entering bool) (gast.WalkStatus, error) {
	if !entering {
		return gast.WalkContinue, nil
	}
	n := node.(*gast.Image)

	altText, width := SplitAltWidth(string(n.Text(source)))

	_, _ = w.WriteString(`<img src="`)
	// Destination is escaped the same way goldmark's own renderer does; by this
	// point it is either a vault-relative path or a data: URI written by
	// inlineVaultAssets.
	_, _ = w.Write(util.EscapeHTML(util.URLEscape(n.Destination, true)))
	_, _ = w.WriteString(`" alt="` + html.EscapeString(altText) + `"`)
	if width > 0 {
		fmt.Fprintf(w, ` width="%d"`, width)
	}
	if len(n.Title) > 0 {
		_, _ = w.WriteString(` title="` + html.EscapeString(string(n.Title)) + `"`)
	}
	_, _ = w.WriteString(` style="max-width:100%">`)

	// The alt text's children are already consumed above.
	return gast.WalkSkipChildren, nil
}
