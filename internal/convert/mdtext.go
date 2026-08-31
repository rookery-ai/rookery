package convert

import "strings"

// Escaping extracted document text so it survives the knowledge base editor.
//
// The KB editor decides whether a note is editable by round-tripping its body
// through a real parse/serialize cycle and comparing the result to the bytes on
// disk (checkFidelity, web/ui/src/pages/kb/editor.ts). A mismatch opens the note
// READ-ONLY — the editor is not made editable, no keystroke marks it dirty, and
// no save path can run. So a converter that writes a document's text verbatim
// does not merely produce untidy markdown: it produces a note the user cannot
// edit, which is the whole point of importing it into a knowledge base.
//
// Every rule below was MEASURED by driving that editor, not reasoned about.
// Two properties of the result are worth stating because both are easy to get
// backwards:
//
// The escaped forms are FIXED POINTS. It is not enough for an escape to change
// the bytes — the escaped form must itself round-trip unchanged, or escaping
// merely trades one unopenable note for another. All of them were verified.
//
// The characters left ALONE are as load-bearing as the ones escaped, and the
// list is not the intuitive one. "_" survives untouched (markdown-it does not
// treat intra-word underscores as emphasis), so escaping it would put a
// backslash inside every snake_case identifier in an imported document. "|",
// "#", "+" and a leading digit run are all safe in prose. And "&" must NOT be
// escaped: "&amp;" round-trips back to a bare "&", so escaping it is the one
// change here that would actively CREATE the failure it is meant to prevent.
//
// One known limitation, recorded rather than worked around: text that literally
// contains an HTML entity ("Caf&eacute;" as characters, not as an encoding)
// cannot be represented. The parser decodes it to "Café" and the serializer
// writes the decoded character back, and no escaping of "&" avoids that — the
// escaped spellings all decode too. Converters should emit decoded text (the
// HTML path already does, via x/net/html), and a literal entity in a Word or
// PDF document will be normalised to the character it names.

// EscapeInline escapes a run of text extracted from a source document so that
// it round-trips through the KB editor unchanged.
//
// It is for document CONTENT only. Markup a converter authors itself — a "# "
// heading prefix, a "- " bullet, a table's pipes, the brackets of a link it is
// building — must not be passed through here, or the construct would be escaped
// into the literal text of itself.
func EscapeInline(s string) string {
	if s == "" {
		return ""
	}
	// Sized for the common case of few or no escapes; growth is amortised.
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		switch r {
		// A backslash must be doubled, and doing it in the same pass as the
		// others is what keeps it correct: a second pass would also double the
		// backslashes this function had just introduced.
		case '\\':
			b.WriteString(`\\`)
		case '`':
			b.WriteString("\\`")
		case '*':
			b.WriteString(`\*`)
		case '[':
			b.WriteString(`\[`)
		case ']':
			b.WriteString(`\]`)
		case '~':
			b.WriteString(`\~`)
		// "<" and ">" are HTML-escaped rather than backslash-escaped because the
		// editor runs its parser with html:true, so the serializer writes them
		// as entities. "&lt;" is the fixed point; "\<" is not.
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// escapeLeadingMarker neutralises a block-leading character that would other-
// wise be read as list syntax. It is applied ONLY at the start of a block: a
// hyphen mid-sentence is ordinary punctuation, and escaping it everywhere
// would put a backslash inside every hyphenated word.
//
// Only the hyphen needs this. A leading "#" without a following space is not a
// heading, "+" and "1990." do not open a list in this parser, and "<", ">", "["
// and "*" are already handled by EscapeInline — all measured.
func escapeLeadingMarker(s string) string {
	// A hyphen followed by a space IS a real bullet the converter authored, and
	// must be left intact. Only a hyphen glued to the following word ("-40") is
	// prose that would be misread.
	if strings.HasPrefix(s, "-") && !strings.HasPrefix(s, "- ") && s != "-" {
		return `\` + s
	}
	return s
}

// escapeDestination escapes the URL half of a link or image.
//
// A destination is a path, not prose, so the inline rules are wrong for it —
// "&lt;" inside a path is simply a broken path. It needs exactly what the
// editor's own image serializer applies (kbImage.ts escapes a backslash and
// both parens, since an unescaped ")" ends the destination early) plus a space
// encoding: a raw space terminates the destination and collapses the whole
// construct back into literal text, which is how "![pic](uploads/my file.png)"
// stops being an image at all.
func escapeDestination(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`(`, `\(`,
		`)`, `\)`,
		` `, "%20",
	)
	return r.Replace(s)
}
