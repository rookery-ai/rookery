package convert

import "strings"

// Scheme handling for URLs lifted out of untrusted HTML.
//
// Conversion is one-directional into markdown, but the markdown is not the end
// of the road: a note is rendered back to HTML by internal/export and by the KB
// editor, and a markdown link becomes a real <a href>. So a destination that
// survives conversion is a destination that eventually gets clicked.
//
// The check this replaces was a literal, case-sensitive HasPrefix on
// "javascript:", which three separate things walk straight past:
//
//   - case — "JaVaScRiPt:" is the same scheme to every browser;
//   - leading whitespace and C0 controls, which browsers strip before parsing;
//   - whitespace and controls INSIDE the scheme — browsers ignore tab, newline
//     and carriage return anywhere in a URL, so "java\tscript:" runs. These
//     reach here already entity-decoded, because x/net/html decodes
//     "java&#09;script:" during parsing.
//
// and which never considered "data:" or "vbscript:" at all.

// urlScheme returns the lowercased scheme of raw with the characters browsers
// ignore removed, or "" when raw carries no scheme. A colon that appears after
// a path, query or fragment delimiter is part of the path, not a scheme
// separator ("/a:b/c" is a relative path).
func urlScheme(raw string) string {
	s := strings.TrimLeft(raw, " \t\n\r\f\v\x00")
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case ':':
			return strings.ToLower(stripIgnored(s[:i]))
		case '/', '?', '#':
			return ""
		}
	}
	return ""
}

// stripIgnored removes the ASCII whitespace and control characters a browser
// discards while parsing a URL. Anything at or below U+0020, plus DEL.
func stripIgnored(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if c := s[i]; c > 0x20 && c != 0x7f {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// blockedHref reports whether an anchor destination must be dropped.
//
// "data:" is blocked outright here rather than narrowed the way it is for
// images: there is no anchor equivalent of an inline raster that is worth
// keeping, and data:text/html is a same-origin script vector.
func blockedHref(raw string) bool {
	switch urlScheme(raw) {
	case "javascript", "vbscript", "data":
		return true
	}
	return false
}

// blockedImageSrc reports whether an image source must be dropped.
//
// Inline raster data URIs are legitimate and common in scraped HTML, so those
// stay. SVG does not: an SVG payload can carry script, and unlike the <img>
// element itself, the note it lands in may be rendered somewhere that honours
// it.
func blockedImageSrc(raw string) bool {
	switch urlScheme(raw) {
	case "javascript", "vbscript":
		return true
	case "data":
		payload := strings.ToLower(stripIgnored(strings.TrimLeft(raw, " \t\n\r\f\v\x00")))
		payload = strings.TrimPrefix(payload, "data:")
		return !strings.HasPrefix(payload, "image/") ||
			strings.HasPrefix(payload, "image/svg")
	}
	return false
}
