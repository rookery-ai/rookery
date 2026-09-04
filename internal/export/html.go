package export

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"html"
	"strings"

	"github.com/rookery-ai/rookery/internal/fonts"
)

// fontFaceCSS inlines the UI font as a data: URI.
//
// Naming the font instead would not work for two reasons. ToPDF shells out to a
// headless renderer running on the SERVER, which will not have Inter installed
// — a named font silently falls back while the export still reports success.
// And an exported HTML file is meant to be a single portable document, which a
// font fetched from anywhere is not.
//
// Built once at package init: the encoding is deterministic, so doing it per
// request would re-base64 48 KB for nothing. It adds ~65 KB to each exported
// file, consistent with the existing precedent that base64's ~33% inflation is
// an accepted cost for self-contained exports (see the vault-asset inlining in
// web/api_kb.go).
var fontFaceCSS = `@font-face{font-family:"InterVariable";font-style:normal;` +
	`font-weight:100 900;src:url(data:font/woff2;base64,` +
	base64.StdEncoding.EncodeToString(fonts.InterVariableWOFF2) +
	`) format("woff2-variations");}`

// htmlCSS is the inlined stylesheet wrapped around the rendered note. It is kept
// deliberately small and self-contained (the font is EMBEDDED, not fetched — see
// fontFaceCSS) so the exported file is a single portable document and so it
// doubles as clean input to a headless PDF engine. It styles the handful of
// block types a note produces: headings, code, tables, blockquotes, lists.
var htmlCSS = fontFaceCSS + `
:root { color-scheme: light; }
body {
  font-family: "InterVariable", ui-sans-serif, system-ui, Helvetica, Arial, sans-serif;
  line-height: 1.6; color: #1a1a1a; background: #fff;
  max-width: 820px; margin: 2rem auto; padding: 0 1.25rem;
}
h1, h2, h3, h4, h5, h6 { line-height: 1.25; margin: 1.6em 0 0.6em; font-weight: 600; }
h1 { font-size: 1.9rem; border-bottom: 1px solid #e5e5e5; padding-bottom: 0.3em; }
h2 { font-size: 1.5rem; border-bottom: 1px solid #eee; padding-bottom: 0.25em; }
h3 { font-size: 1.25rem; }
p { margin: 0.75em 0; }
a { color: #0b5fff; text-decoration: none; }
a:hover { text-decoration: underline; }
code {
  font-family: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace;
  font-size: 0.9em; background: #f3f3f3; padding: 0.15em 0.35em; border-radius: 4px;
}
pre {
  background: #f6f8fa; padding: 0.9rem 1rem; border-radius: 6px; overflow-x: auto;
  border: 1px solid #eaecef;
}
pre code { background: none; padding: 0; font-size: 0.875em; }
blockquote {
  margin: 0.9em 0; padding: 0.2em 1rem; color: #555;
  border-left: 4px solid #d0d7de; background: #fafbfc;
}
table { border-collapse: collapse; margin: 1em 0; width: 100%; }
th, td { border: 1px solid #d0d7de; padding: 0.45em 0.7em; text-align: left; }
th { background: #f3f4f6; font-weight: 600; }
ul, ol { padding-left: 1.6rem; }
li { margin: 0.25em 0; }
hr { border: none; border-top: 1px solid #e5e5e5; margin: 1.8em 0; }
img { max-width: 100%; }
.kb-attachments { list-style: none; padding-left: 0; }
.kb-attachments li { margin: 0.35em 0; }
.kb-attachment-path {
  font-family: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace;
  font-size: 0.85em; color: #666; margin-left: 0.5em;
}
/* A grid split across a page boundary reads as two broken half-layouts, so it
   is kept whole. Only relevant to the PDF path, where a headless renderer
   paginates; harmless in a browser. */
@media print {
  .kb-columns { break-inside: avoid; page-break-inside: avoid; }
  img { break-inside: avoid; page-break-inside: avoid; }
}
`

// htmlDocTemplate wraps rendered body HTML in a minimal, valid, standalone HTML5
// document. %s is the escaped title, %s the inlined CSS, %s the rendered body.
const htmlDocTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<style>%s</style>
</head>
<body>
%s</body>
</html>
`

// ToHTML renders a markdown note into a self-contained HTML document with
// readable inlined CSS. Wikilinks are flattened to their display text first;
// raw HTML in the note is dropped (goldmark's safe default), so the output can
// never carry an injected <script>. External links are preserved. This HTML is
// also the source ToPDF hands to a headless renderer.
func ToHTML(md []byte, opts Options) ([]byte, error) {
	source := stripWikilinks(md)

	var body bytes.Buffer
	if err := mdRenderer.Convert(source, &body); err != nil {
		// A conversion failure here is unexpected (goldmark rarely errors on
		// valid UTF-8), but degrade to escaped preformatted text rather than
		// dropping the note entirely — the reader still gets the content.
		body.Reset()
		fmt.Fprintf(&body, "<pre>%s</pre>\n", html.EscapeString(string(md)))
	}

	rendered := strings.TrimRight(body.String(), "\n") + "\n"
	rendered += attachmentsHTML(opts.Attachments)

	doc := fmt.Sprintf(htmlDocTemplate,
		html.EscapeString(opts.docTitle()),
		htmlCSS,
		rendered,
	)
	return []byte(doc), nil
}

// attachmentsHTML renders the closing list of files the note links to.
//
// The links are kept relative, so they still resolve when the document travels
// alongside its uploads folder, and the PATH is shown as well as the name — a
// reader who received only the exported file cannot follow the link, and the
// path is what lets them ask for the right thing.
func attachmentsHTML(list []Attachment) string {
	if len(list) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("<hr>\n<h2>Attachments</h2>\n<ul class=\"kb-attachments\">\n")
	for _, a := range list {
		fmt.Fprintf(&sb, "<li><a href=\"%s\">%s</a> <span class=\"kb-attachment-path\">%s</span></li>\n",
			html.EscapeString(a.Path), html.EscapeString(a.Name), html.EscapeString(a.Path))
	}
	sb.WriteString("</ul>\n")
	return sb.String()
}
