// Package logsafe makes a caller-supplied string safe to put in a log entry.
//
// The risk is log forgery: a value carrying a newline can fabricate an entry
// that looks like the server's own. That matters here more than it might
// elsewhere, because this project's logs are its diagnosis surface — several
// bugs in this codebase were found by reading them, and one was found by
// noticing a log line that should have existed and did not.
//
// Go's DEFAULT slog handler already quotes and escapes such values, so nothing
// is currently forgeable. This package exists anyway for a specific reason:
// that escaping is a property of a handler nobody chose. A future
// slog.SetDefault with a hand-rolled handler — an easy thing to write and an
// easy thing to get wrong — would remove it silently, turning a dormant finding
// into a live one with no test anywhere that would notice.
//
// So the sanitising is done at the call site, where it does not depend on a
// decision made in another file.
package logsafe

import "strings"

// maxLoggedValue bounds a logged value. A model-supplied path has no length
// limit of its own, and a megabyte in a log line is its own kind of denial of
// readability.
const maxLoggedValue = 256

// Value returns s with anything that could break a log line removed, truncated
// to a readable length.
//
// Control characters become spaces rather than being deleted, so
// "notes/a.md\nINFO forged" reads as "notes/a.md INFO forged" — visibly one
// value, rather than silently losing the evidence that something odd was
// passed.
func Value(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteByte(' ')
		case r < 0x20 || r == 0x7f:
			// Other C0 controls and DEL: a terminal reading the log would
			// interpret some of these, so they do not survive either.
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) > maxLoggedValue {
		// Cut on a rune boundary; this project's data is routinely Cyrillic.
		cut := maxLoggedValue
		for cut > 0 && !utf8RuneStart(out[cut]) {
			cut--
		}
		out = out[:cut] + "…"
	}
	return out
}

// utf8RuneStart reports whether b is the first byte of a UTF-8 rune. Inlined
// rather than importing unicode/utf8 for one predicate.
func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }
