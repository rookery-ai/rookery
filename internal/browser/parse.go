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
		e, ok := parseElementLine(rest)
		if !ok {
			continue
		}
		out = append(out, e)
	}
	return out
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
