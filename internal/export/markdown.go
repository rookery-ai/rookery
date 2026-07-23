package export

import (
	"regexp"

	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// mdRenderer is the shared markdown→HTML renderer. It mirrors the config in
// web/handlers_kb.go (GFM: tables, strikethrough, task lists, autolinks) and,
// deliberately like it, does NOT enable goldmark's "unsafe" HTML — raw HTML in a
// note is dropped rather than rendered, so an exported note can never carry a
// <script> into the reader's browser. The vault is the user's own content, but
// defence-in-depth is cheap and export produces a file that may travel.
var mdRenderer = goldmark.New(goldmark.WithExtensions(extension.GFM))

// mdParser produces the AST the DOCX builder walks. It is the SAME parser the
// renderer uses (GFM tables etc.) so HTML and DOCX see identical structure — a
// table in one is a table in the other.
var mdParser = goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser()

// wikilinkRE matches an Obsidian-style [[Target]] or [[Target|Display]] link.
// Target and Display are captured separately; the pipe form is optional.
var wikilinkRE = regexp.MustCompile(`\[\[([^\]|]+)(?:\|([^\]]+))?\]\]`)

// stripWikilinks replaces [[wikilinks]] with plain display text BEFORE the
// markdown reaches goldmark. goldmark has no notion of wikilinks, so left alone
// they would render as the literal "[[Target]]" brackets. The KB viewer rewrites
// them to an in-app route, but an exported document leaves the app — there is
// nothing to link to — so a wikilink degrades to the text a reader would see:
// the display label if the "|Display" form is used, otherwise the target name.
func stripWikilinks(src []byte) []byte {
	return wikilinkRE.ReplaceAllFunc(src, func(m []byte) []byte {
		sub := wikilinkRE.FindSubmatch(m)
		if len(sub[2]) > 0 { // [[Target|Display]] → Display
			return sub[2]
		}
		return sub[1] // [[Target]] → Target
	})
}

// parseMarkdown preprocesses wikilinks and parses the result into a goldmark
// AST, returning the (preprocessed) source alongside the root node — the source
// is needed to resolve every node's text via its segments.
func parseMarkdown(src []byte) (root gast.Node, source []byte) {
	source = stripWikilinks(src)
	return mdParser.Parse(text.NewReader(source)), source
}
