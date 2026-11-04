package browser

import (
	"regexp"
	"strings"
	"unicode"
)

// redactor removes every secret this session typed from text on its way back to
// the model.
//
// It used to be an exact-string ReplaceAll, and that leaked. A payment field
// that reformats what it is given — 4111111111111111 rendered as
// "4111 1111 1111 1111", or masked to "•••• 1111" — echoes a string that is not
// the secret byte-for-byte, and the element list carries every field's rendered
// value, iframes included, which is exactly where hosted card fields live. So a
// formatted card number could reach the model while redaction reported nothing
// to do.
//
// Three tiers, from strongest to weakest evidence:
//
//   - the whole secret, case-insensitive, with any run of separators (space,
//     dash, slash, dot, bullet, asterisk, middle dot) between its characters;
//   - a 3-character secret (a CVV) matched exactly — a separator-tolerant
//     pattern over 3 characters would fire on unrelated text;
//   - for a card-shaped secret (12+ digits), its first 6 and last 4 digits, but
//     ONLY beside a mask character. Redacting every occurrence of the last four
//     digits would wipe ordinary amounts and years off a page, and a page that reads as mangled is a page the model distrusts.
//
// Secrets shorter than 3 characters are not matched at all: they would mangle
// unrelated text. The field-based rule (a field filled from a secret is always
// listed as "filled from ${NAME}") covers them where it matters.
//
// This is the BACKSTOP. The primary protection is that the model only ever
// writes ${NAME} and the value is typed by the helper.
type redactor struct {
	values   []string
	patterns []*regexp.Regexp
}

const (
	redactSep  = `[\s\-/.•*·]*`
	maskChars  = `*•xX…`
	redactMark = "[redacted]"
)

func (r *redactor) remember(v string) {
	if r == nil || v == "" {
		return
	}
	for _, existing := range r.values {
		if existing == v {
			return
		}
	}
	r.values = append(r.values, v)

	runes := []rune(v)
	if len(runes) < 3 {
		return
	}
	var alnum []string
	// own collects the secret's punctuation. It is always allowed between
	// characters: an email typed from a secret came back as a page's pre-filled
	// value and slipped through, because the generic separators allowed "."
	// but not "@".
	own := map[rune]bool{}
	digitsOnly := true
	for _, c := range runes {
		if unicode.IsLetter(c) || unicode.IsDigit(c) {
			alnum = append(alnum, regexp.QuoteMeta(string(c)))
		} else if !unicode.IsSpace(c) {
			own[c] = true
		}
		if !unicode.IsDigit(c) {
			digitsOnly = false
		}
	}
	if len(runes) == 3 || len(alnum) < 4 {
		r.patterns = append(r.patterns, regexp.MustCompile(regexp.QuoteMeta(v)))
		return
	}
	sep := redactSep
	if len(own) > 0 {
		var extra strings.Builder
		for c := range own {
			// Escape every class-significant character explicitly: an
			// unescaped "-" between two others forms a RANGE, and an invalid
			// range would make regexp.MustCompile panic on a secret.
			if strings.ContainsRune(`\-]^[`, c) {
				extra.WriteRune('\\')
			}
			extra.WriteRune(c)
		}
		sep = `[\s\-/.•*·` + extra.String() + `]*`
	}
	r.patterns = append(r.patterns, regexp.MustCompile(`(?i)`+strings.Join(alnum, sep)))

	if digitsOnly && len(runes) >= 12 {
		mask := `[` + regexp.QuoteMeta(maskChars) + `]`
		gap := `[\s\-` + regexp.QuoteMeta(maskChars) + `]*`
		first6, last4 := string(runes[:6]), string(runes[len(runes)-4:])
		r.patterns = append(r.patterns,
			regexp.MustCompile(first6+`[\s\-]*`+mask),
			regexp.MustCompile(mask+gap+last4),
		)
	}
}

// Redactor is the same matcher, exported for the host-process side of the tool
// (internal/coder), which applies it once more to the rendered tool result. Two
// layers is deliberate: the helper redacts what it collects, and the host
// redacts what it is about to hand the model, so a channel the helper missed
// is still caught before the model sees it.
type Redactor struct{ r redactor }

// Remember adds a resolved secret value.
func (x *Redactor) Remember(v string) { x.r.remember(v) }

// Apply redacts every remembered secret from s.
func (x *Redactor) Apply(s string) string { return x.r.apply(s) }

func (r *redactor) apply(s string) string {
	if r == nil || s == "" {
		return s
	}
	for _, p := range r.patterns {
		s = p.ReplaceAllLiteralString(s, redactMark)
	}
	return s
}
