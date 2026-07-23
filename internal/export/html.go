package export

import (
	"bytes"
	"fmt"
	"html"
	"strings"
)

// htmlCSS is the inlined stylesheet wrapped around the rendered note. It is kept
// deliberately small and self-contained (no web fonts, no external assets) so the
// exported file is a single portable document and so it doubles as clean input to
// a headless PDF engine. It styles the handful of block types a note produces:
// headings, code, tables, blockquotes, lists.
const htmlCSS = `
:root { color-scheme: light; }
body {
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
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

	doc := fmt.Sprintf(htmlDocTemplate,
		html.EscapeString(opts.docTitle()),
		htmlCSS,
		strings.TrimRight(body.String(), "\n")+"\n",
	)
	return []byte(doc), nil
}
