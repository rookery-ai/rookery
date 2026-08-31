package convert

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Mapping HTML onto the constructs the knowledge base editor can actually edit.
//
// The editor grew a real vocabulary — alignment, collapsible sections,
// underline, colour marks, callouts, tables — and the converters were never
// taught any of it, so every one of these arrived as undifferentiated prose. An
// imported page lost its structure on the way in, and the user could not get it
// back by hand because the information was gone.
//
// Each form below is the editor's OWN serialized spelling, taken from its
// serializers rather than invented here: get it wrong and the note round-trips
// to something different, which opens it read-only — a worse outcome than the
// flattening this replaces. web/ui/src/pages/kb/nodes/*.ts are the source of
// truth, and internal/convert/testdata/fidelity is the check.

// quotePrefix turns a rendered block into a markdown blockquote.
//
// Each PARAGRAPH becomes one "> " line rather than each source line, because a
// multi-line quote round-trips through the editor as a single joined line: the
// newlines inside a quote are soft breaks, and the serializer collapses them.
// Emitting them separately would open the note read-only.
func quotePrefix(s string) string {
	if s == "" {
		return ""
	}
	paras := strings.Split(s, "\n\n")
	var out []string
	for _, p := range paras {
		p = strings.TrimSpace(strings.ReplaceAll(p, "\n", " "))
		if p == "" {
			continue
		}
		out = append(out, "> "+p)
	}
	// A blank quoted line is what separates two quoted paragraphs.
	return strings.Join(out, "\n>\n")
}

// alignRE reads a text alignment out of a style attribute. Only the three
// values the editor's kbAlign node accepts are recognised; anything else is not
// an alignment it can represent, so the div is treated as an ordinary one.
var alignRE = regexp.MustCompile(`text-align\s*:\s*(left|center|right)`)

// alignmentOf reports the alignment a <div> carries, or "".
//
// Both spellings are read — the align attribute and the CSS property — because
// real documents use both, and the editor itself parses both while serializing
// only the attribute form. Emitting the attribute form is therefore what makes
// an aligned block a fixed point rather than something the editor rewrites on
// first save.
func alignmentOf(n *html.Node) string {
	if v := strings.ToLower(strings.TrimSpace(attr(n, "align"))); v != "" {
		switch v {
		case "left", "center", "right":
			// A div carrying data-cols belongs to the columns node, which owns
			// that attribute; claiming it here would produce a wrapper with both
			// and the editor drops one of them.
			if attr(n, "data-cols") != "" {
				return ""
			}
			return v
		}
	}
	if m := alignRE.FindStringSubmatch(strings.ToLower(attr(n, "style"))); m != nil {
		if attr(n, "data-cols") != "" {
			return ""
		}
		return m[1]
	}
	return ""
}

// inlineHTML wraps a node's text in a literal HTML tag pair. Used for the marks
// whose canonical serialized form IS raw HTML (underline, the colour marks),
// which is how the editor writes them and therefore what round-trips.
func (w *mdWriter) inlineHTML(tag string, n *html.Node) {
	raw := textOf(n)
	leading, trailing := hasEdgeSpace(raw)
	text := squeeze(raw)
	if text == "" {
		return
	}
	w.emit("<"+tag+">"+EscapeInline(text)+"</"+tag+">", leading)
	w.pendingSpace = trailing
}

// colourRE reads a foreground colour out of a style attribute, accepting only a
// hex value. A named or rgb() colour is deliberately NOT carried: the editor
// normalises what it stores to lowercase hex, so anything else would be
// rewritten on the first save and the note would open read-only until then.
var colourRE = regexp.MustCompile(`(?:^|;)\s*color\s*:\s*(#[0-9a-fA-F]{3,8})\s*(?:;|$)`)

// bgColourRE is the background half, read the same way and for the same reason.
var bgColourRE = regexp.MustCompile(`background-color\s*:\s*(#[0-9a-fA-F]{3,8})`)

// span renders a <span> carrying a colour as the editor's own colour mark, and
// anything else as its plain contents.
//
// The exact spelling matters and is not obvious: no space after the colon,
// lowercase hex, and a highlight pins its own foreground so the text stays
// legible on it. Those come from marks/colors.ts, where the same literal form is
// pinned by test — a span written any other way is rewritten on first save.
func (w *mdWriter) span(n *html.Node) bool {
	style := attr(n, "style")
	if style == "" {
		return false
	}
	fg := firstSubmatch(colourRE, style)
	bg := firstSubmatch(bgColourRE, style)
	if fg == "" && bg == "" {
		return false
	}
	raw := textOf(n)
	leading, trailing := hasEdgeSpace(raw)
	text := squeeze(raw)
	if text == "" {
		return false
	}
	body := EscapeInline(text)
	// Registration order in the editor puts the background OUTSIDE the
	// foreground, so a text colour applied inside a highlight wins. Nesting them
	// the other way is pinned as lossy there, so it must not be produced here.
	if fg != "" {
		body = `<span style="color:` + strings.ToLower(fg) + `">` + body + `</span>`
	}
	if bg != "" {
		body = `<span style="background-color:` + strings.ToLower(bg) + `;color:#18181b">` + body + `</span>`
	}
	w.emit(body, leading)
	w.pendingSpace = trailing
	return true
}

func firstSubmatch(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

// details renders <details>/<summary> as the editor's collapsible node.
//
// The canonical form has the two tags on SEPARATE lines with a blank line before
// the body. The glued spelling parses to the same document but is explicitly not
// a fixed point — nodes/toggle.ts records a reverted attempt to make it one — so
// emitting it would open every imported page containing a collapsible in
// read-only mode.
func (w *mdWriter) details(n *html.Node) {
	var summary string
	var body mdWriter
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.DataAtom == atom.Summary {
			summary = squeeze(textOf(c))
			continue
		}
		body.walk(c)
	}
	if summary == "" {
		// <details> with no summary has no label to collapse under; the browser
		// shows a default one, which is not text we can honestly invent.
		summary = "Details"
	}
	w.block()
	w.sb.WriteString("<details>\n<summary>" + EscapeInline(summary) + "</summary>\n\n")
	if b := strings.TrimSpace(body.sb.String()); b != "" {
		w.sb.WriteString(b + "\n\n")
	}
	w.sb.WriteString("</details>")
	w.block()
}

// codeLanguage reads the language off a <pre>'s inner <code class="language-go">,
// the convention every syntax highlighter and every markdown renderer uses. The
// info string was previously dropped, so an imported code block lost its
// highlighting and its language for good.
func codeLanguage(n *html.Node) string {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || c.DataAtom != atom.Code {
			continue
		}
		for _, class := range strings.Fields(attr(c, "class")) {
			for _, prefix := range []string{"language-", "lang-"} {
				if strings.HasPrefix(class, prefix) {
					lang := strings.TrimPrefix(class, prefix)
					// A fence's info string cannot contain a backtick or a
					// space without changing what the fence means.
					if lang != "" && !strings.ContainsAny(lang, "` \t") {
						return lang
					}
				}
			}
		}
	}
	return ""
}
