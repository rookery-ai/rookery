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
// Nesting is discarded on purpose. The model acts on one control at a time, and
// a flat list is both smaller and easier for a weak model than a tree it has to
// navigate.
func ParseAriaSnapshot(snap string) []Element {
	var out []Element
	// label is the last visible text seen since the previous interactive control
	// — the candidate name for an unlabeled form field. See inferFieldName.
	label := ""
	for _, raw := range strings.Split(snap, "\n") {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		rest := strings.TrimSpace(line[2:])
		// "- /url: ..." is a PROPERTY of the element above, not an element.
		// Treating it as one would add an unclickable "/url" row to every link.
		if strings.HasPrefix(rest, "/") {
			continue
		}
		if t, ok := strings.CutPrefix(rest, "text:"); ok {
			label = cleanLabel(t)
			continue
		}
		e, ok := parseElementLine(rest)
		if !ok {
			continue
		}
		if !interactiveRoles[strings.ToLower(e.Role)] {
			// A wrapper (generic, group, document) between a label and its field
			// is normal markup and must not break the association.
			out = append(out, e)
			continue
		}
		inferFieldName(&e, label)
		label = "" // text is never carried past a control to a later field
		out = append(out, e)
	}
	return out
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
	if e.Note == "" {
		e.Note = "label inferred from nearby text"
	} else {
		e.Note = "label inferred from nearby text, " + e.Note
	}
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
