package browser

import "strings"

// ParseAriaSnapshot turns Playwright's "ai"-mode aria snapshot into a flat
// element list.
//
// The snapshot is an indented YAML-ish tree of "- role "name" [attrs]" lines,
// where a nested "- /url: ..." line is a PROPERTY of the row above it rather
// than a row of its own. testdata/github-login.snapshot is a real capture from
// a live page and is what the tests parse — an invented fixture would only
// prove the parser agrees with itself.
//
// Parsing this rather than querying the DOM ourselves is deliberate: the [ref=]
// handles are what Playwright's `aria-ref=` selector engine resolves, so they are
// the only addressing scheme that lets the model say click(ref=e31) without
// writing a CSS selector. A DOM walk would give us elements we could not then
// address.
//
// The OUTPUT is flat on purpose: the model acts on one control at a time, and a
// flat list is both smaller and easier for a weak model than a tree it has to
// navigate. The parse is not flat, though — it tracks indentation, because
// several of the names a form needs are only recoverable from a control's
// surroundings (see nameFromSurroundings).
func ParseAriaSnapshot(snap string) []Element {
	var (
		out   []Element
		nodes []snapNode
		stack []int // indices into nodes, innermost last
	)
	// label is the last visible text seen since the previous interactive control
	// — the candidate name for an unlabeled form field. See inferFieldName.
	label := ""
	for _, raw := range strings.Split(snap, "\n") {
		trimmed := strings.TrimLeft(raw, " ")
		indent := len(raw) - len(trimmed)
		line := strings.TrimSpace(trimmed)
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		rest := strings.TrimSpace(line[2:])
		for len(stack) > 0 && nodes[stack[len(stack)-1]].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		parent := -1
		if len(stack) > 0 {
			parent = stack[len(stack)-1]
		}

		// "- /url: ..." is a PROPERTY of the element above, not an element.
		// Treating it as one would add an unclickable "/url" row to every link.
		if prop, ok := strings.CutPrefix(rest, "/"); ok {
			key, val, _ := strings.Cut(prop, ":")
			if key == "placeholder" && parent >= 0 && nodes[parent].el >= 0 {
				applyPlaceholder(&out[nodes[parent].el], cleanLabel(val))
			}
			continue
		}
		n := snapNode{indent: indent, parent: parent, el: -1}
		if t, ok := strings.CutPrefix(rest, "text:"); ok {
			// Directly under a text field, a text line is that field's VALUE:
			// Playwright writes a field that has a placeholder as
			// "textbox …:" with "/placeholder:" and "text: <value>" children,
			// instead of the inline "textbox …: <value>". Read as a label it
			// made every such field look empty, and named the next field after
			// what had been typed into this one.
			if parent >= 0 && nodes[parent].el >= 0 && isTextInput(out[nodes[parent].el].Role) {
				setFieldValue(&out[nodes[parent].el], t)
				continue
			}
			n.role, n.content = "text", cleanLabel(t)
			label = n.content
			nodes = append(nodes, n)
			continue
		}
		n.role, n.name, n.content = readLineText(rest)
		if e, ok := parseElementLine(rest); ok {
			if interactiveRoles[strings.ToLower(e.Role)] {
				inferFieldName(&e, label)
				label = "" // text is never carried past a control to a later field
			} else if staticTextRoles[strings.ToLower(e.Role)] {
				if t := firstNonEmpty(n.content, n.name); t != "" {
					label = cleanLabel(t)
				}
			}
			// A wrapper (generic, group, document) between a label and its field
			// is normal markup and must not break the association.
			n.el = len(out)
			out = append(out, e)
		} else if staticTextRoles[strings.ToLower(n.role)] {
			if t := firstNonEmpty(n.content, n.name); t != "" {
				label = cleanLabel(t)
			}
		}
		nodes = append(nodes, n)
		stack = append(stack, len(nodes)-1)
	}
	nameFromSurroundings(nodes, out)
	return out
}

// snapNode is one snapshot line with enough structure to look around it.
type snapNode struct {
	indent  int
	parent  int // index into the node list, -1 at the root
	role    string
	name    string // the quoted accessible name, if any
	content string // text after the trailing colon, if any
	el      int    // index into the element list, -1 when the line has no ref
}

// staticTextRoles carry visible words a human reads as a label. A form whose
// labels are <p> elements is common and has no "text:" lines at all, so reading
// only those names nothing.
var staticTextRoles = map[string]bool{
	"paragraph": true, "heading": true, "generic": true, "cell": true,
	"gridcell": true, "label": true, "listitem": true, "strong": true,
	"emphasis": true, "text": true, "columnheader": true,
}

// readLineText extracts the role, quoted name and after-colon content of a line
// without requiring a ref, so text lines that cannot be acted on still count as
// context.
func readLineText(s string) (role, name, content string) {
	role, rest := readRole(s)
	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(rest, `"`) {
		if q, after, ok := readQuoted(rest); ok {
			name, rest = q, after
		}
	}
	_, after := readAttrs(rest)
	content = cleanLabel(strings.TrimPrefix(strings.TrimSpace(after), ":"))
	return role, name, content
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// applyPlaceholder records a field's placeholder. A placeholder is often the
// only statement of the FORMAT a field wants ("MM/YY"), so it goes in the note
// even when the field has a name; it becomes the name only when there is none.
func applyPlaceholder(e *Element, ph string) {
	if ph == "" || !fieldRoles[strings.ToLower(e.Role)] {
		return
	}
	if strings.TrimSpace(e.Name) == "" {
		e.Name = ph
	}
	note := "placeholder " + ph
	if e.Note == "" || e.Note == "empty" {
		if e.Note == "empty" {
			note += ", empty"
		}
		e.Note = note
		return
	}
	e.Note = note + ", " + e.Note
}

// setFieldValue records a field's current value from a child text line,
// replacing the "empty" the field line alone implied.
func setFieldValue(e *Element, raw string) {
	v := strings.TrimSpace(raw)
	if len(v) >= 2 && strings.HasPrefix(v, `"`) && strings.HasSuffix(v, `"`) {
		v = v[1 : len(v)-1]
	}
	if v == "" {
		return
	}
	v = "value: " + truncateRunes(v, 80)
	switch {
	case e.Note == "" || e.Note == "empty":
		e.Note = v
	case strings.HasSuffix(e.Note, ", empty"):
		e.Note = strings.TrimSuffix(e.Note, ", empty") + ", " + v
	default:
		e.Note += ", " + v
	}
}

// rowNameMaxRunes bounds a name built from a table row: enough to tell rows
// apart (a description, a status, an amount), short enough not to crowd out the
// rest of the list.
const rowNameMaxRunes = 60

// nameFromSurroundings fills in names that only the structure around a control
// can supply:
//
//   - a checkbox or radio inside a table row takes the row's other cells. A
//     "select this row" checkbox has no label of its own; its meaning is the
//     cells AFTER it, which inferFieldName (text BEFORE a field) cannot see.
//   - a button or link with no name takes its first descendant's text. That is
//     what a page behind an open modal looks like: Chromium drops the names,
//     the words are still there.
//   - text items inside an open menu or listbox become options. A scripted
//     dropdown's items are list items and generics, neither of which is an
//     interactive role, so all twelve months of a card-expiry picker were
//     filtered out and a run pressed ArrowDown 180 times instead.
func nameFromSurroundings(nodes []snapNode, out []Element) {
	children := make([][]int, len(nodes))
	for i, n := range nodes {
		if n.parent >= 0 {
			children[n.parent] = append(children[n.parent], i)
		}
	}
	var texts func(i, skip int, acc *[]string)
	texts = func(i, skip int, acc *[]string) {
		if i == skip {
			return
		}
		if t := firstNonEmpty(nodes[i].content, nodes[i].name); t != "" && !interactiveRoles[strings.ToLower(nodes[i].role)] {
			*acc = append(*acc, t)
		}
		for _, c := range children[i] {
			texts(c, skip, acc)
		}
	}
	ancestor := func(i int, roles ...string) int {
		for p := nodes[i].parent; p >= 0; p = nodes[p].parent {
			for _, r := range roles {
				if strings.EqualFold(nodes[p].role, r) {
					return p
				}
			}
		}
		return -1
	}

	for i, n := range nodes {
		if n.el < 0 {
			continue
		}
		e := &out[n.el]
		role := strings.ToLower(e.Role)
		switch {
		case (role == "checkbox" || role == "radio") && strings.TrimSpace(e.Name) == "":
			row := ancestor(i, "row")
			if row < 0 {
				continue
			}
			var parts []string
			texts(row, i, &parts)
			if len(parts) == 0 {
				continue
			}
			e.Name = truncateRunes(strings.Join(parts, " · "), rowNameMaxRunes)
			e.Note = joinNote("named from its table row", e.Note)

		case (role == "button" || role == "link") && strings.TrimSpace(e.Name) == "":
			var parts []string
			for _, c := range children[i] {
				texts(c, -1, &parts)
			}
			if len(parts) > 0 {
				e.Name = truncateRunes(parts[0], rowNameMaxRunes)
			}

		case !interactiveRoles[role] && ancestor(i, "menu", "listbox", "menubar") >= 0:
			t := firstNonEmpty(n.content, n.name)
			if t == "" || hasRefDescendantWithText(nodes, children, i) {
				continue
			}
			e.Role, e.Name, e.Note = "option", t, "menu option"
		}
	}
}

// hasRefDescendantWithText reports whether a node has a deeper addressable
// node carrying text — in which case the deeper one is the option and this one
// is only its wrapper, and listing both would show every choice twice.
func hasRefDescendantWithText(nodes []snapNode, children [][]int, i int) bool {
	for _, c := range children[i] {
		if nodes[c].el >= 0 && firstNonEmpty(nodes[c].content, nodes[c].name) != "" {
			return true
		}
		if hasRefDescendantWithText(nodes, children, c) {
			return true
		}
	}
	return false
}

func joinNote(add, existing string) string {
	if existing == "" {
		return add
	}
	return add + ", " + existing
}

func truncateRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n-1]) + "…"
}

// inferFieldName gives an unnamed FORM FIELD the visible text directly before it.
//
// FilterInteractive drops a control with no accessible name, because the model
// chooses by name. That is right for a button and wrong for a field whose
// <label> is simply not linked to it — a common markup mistake (demoblaze.com's
// login: label for="log-name" on input id="loginusername"). The field then had
// no name, was filtered out, and vanished from the agent's view while a human
// sees "Username:" right beside it; a real run clicked "Log in" 24 times and
// pressed Tab ~100 times looking for fields it was never shown.
//
// Deliberately narrow: only field roles borrow a name (an unnamed button still
// offers nothing to choose by), only from text with no other control between,
// and the note says the name was inferred so the model does not mistake it for
// a real label.
func inferFieldName(e *Element, label string) {
	if strings.TrimSpace(e.Name) != "" || label == "" || !fieldRoles[strings.ToLower(e.Role)] {
		return
	}
	e.Name = label
	e.Note = joinNote("label inferred from nearby text", e.Note)
}

var fieldRoles = map[string]bool{
	"textbox": true, "searchbox": true, "textarea": true, "combobox": true, "spinbutton": true,
}

// cleanLabel turns a snapshot text line into a field name: unquoted, without the
// trailing colon or required-marker asterisk a form label usually carries.
func cleanLabel(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) {
		s = s[1 : len(s)-1]
	}
	s = strings.TrimSpace(strings.TrimRight(strings.TrimSpace(s), ":*"))
	if len(s) > 80 {
		return "" // a paragraph, not a label
	}
	return s
}

// parseElementLine parses one "- " line's body.
func parseElementLine(s string) (Element, bool) {
	role, rest := readRole(s)
	if role == "" {
		return Element{}, false
	}
	var e Element
	e.Role = role

	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(rest, `"`) {
		name, after, ok := readQuoted(rest)
		if ok {
			e.Name = name
			rest = strings.TrimSpace(after)
		}
	}

	attrs, after := readAttrs(rest)
	e.Ref = attrs["ref"]
	if e.Ref == "" {
		// No ref means nothing can act on it; such a row would be noise the
		// model might still try to click.
		return Element{}, false
	}
	e.Note = noteFromAttrs(attrs)

	// Text after a trailing colon is the element's current value/content. For a
	// textbox that is what is already typed in it, which the model needs in
	// order to decide whether to clear the field first.
	if v := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(after), ":")); v != "" {
		if e.Note == "" {
			e.Note = "value: " + v
		} else {
			e.Note += ", value: " + v
		}
	} else if isTextInput(e.Role) && e.Note == "" {
		e.Note = "empty"
	}
	return e, true
}

func isTextInput(role string) bool {
	switch strings.ToLower(role) {
	case "textbox", "searchbox", "textarea", "combobox", "spinbutton":
		return true
	}
	return false
}

// readRole reads the leading role token.
func readRole(s string) (string, string) {
	for i, r := range s {
		if r == ' ' || r == '[' || r == ':' || r == '"' {
			return s[:i], s[i:]
		}
	}
	return s, ""
}

// readQuoted reads a double-quoted string, honouring backslash escapes so a
// control literally named `Say "hi"` does not truncate the parse.
func readQuoted(s string) (string, string, bool) {
	if len(s) == 0 || s[0] != '"' {
		return "", s, false
	}
	var b strings.Builder
	escaped := false
	for i := 1; i < len(s); i++ {
		c := s[i]
		if escaped {
			b.WriteByte(c)
			escaped = false
			continue
		}
		switch c {
		case '\\':
			escaped = true
		case '"':
			return b.String(), s[i+1:], true
		default:
			b.WriteByte(c)
		}
	}
	return "", s, false
}

// readAttrs collects the bracketed [key] / [key=value] groups.
func readAttrs(s string) (map[string]string, string) {
	attrs := map[string]string{}
	for {
		s = strings.TrimSpace(s)
		if !strings.HasPrefix(s, "[") {
			return attrs, s
		}
		end := strings.IndexByte(s, ']')
		if end < 0 {
			return attrs, s
		}
		body := s[1:end]
		if eq := strings.IndexByte(body, '='); eq >= 0 {
			attrs[body[:eq]] = body[eq+1:]
		} else {
			attrs[body] = ""
		}
		s = s[end+1:]
	}
}

// noteFromAttrs renders the handful of states worth showing the model. It
// deliberately drops [cursor=pointer] and [active], which appear on most rows
// and carry no decision value — spending the result budget on them would push
// real controls off the end of the list.
func noteFromAttrs(attrs map[string]string) string {
	var parts []string
	for _, k := range []string{"disabled", "checked", "expanded", "selected", "pressed"} {
		if v, ok := attrs[k]; ok {
			if v == "" || v == "true" {
				parts = append(parts, k)
			} else {
				parts = append(parts, k+"="+v)
			}
		}
	}
	return strings.Join(parts, ", ")
}
