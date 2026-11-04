package browser

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/mxschmitt/playwright-go"
)

// keyDelayMS is the pause between typed keys. Long enough for a page's own
// key handlers (formatters, masks, validators) to run between characters,
// short enough that a 16-digit card number takes well under a second.
const keyDelayMS = 25

// maxTypedRunes is the longest value typed key by key. Longer values are free
// text — a message, an address — which is never a key-driven widget, and typing
// it would take seconds per call.
const maxTypedRunes = 256

// secretAttr marks a DOM element the helper filled from a secret. It survives a
// re-render that keeps the node, which is what lets a later listing still say
// "filled from ${NAME}" after the ref has changed. Its value is the
// placeholder, never the secret.
const secretAttr = "data-rk-secret"

// fillText enters a value into one field and reports, as a note for the model,
// whether the page actually took it.
//
// Typing key by key is the DEFAULT, not a fallback. A value written in one step
// (Locator.Fill) fires no key events, and a card-expiry box that writes its
// hidden month/year inputs from an onkeyup handler then shows "12/30" while the
// form submits nothing — the visible field reads back correctly, so a read-back
// alone cannot catch it. The one-step write is kept as the fallback for the
// opposite case: a key filter that rejects a character the value legitimately
// contains.
//
// Nothing here returns the field's contents. The read-back is compared inside
// the helper and reported as a verdict.
func (h *browserHost) fillText(s *hostSession, loc playwright.Locator, req actReq, timeout float64) (string, error) {
	opt := playwright.Float(timeout)
	if refusal, err := checkReachable(loc, req.Ref, timeout); err != nil || refusal != "" {
		return refusal, err
	}
	if isSelect(loc, timeout) {
		if err := selectByText(loc, req.Value, timeout); err != nil {
			return "", err
		}
		return "fill: chose " + choiceLabel(req), nil
	}
	if !isTextEditable(loc, timeout) {
		if isDropdownTrigger(loc, timeout) {
			return h.chooseFromDropdown(s, loc, req, timeout)
		}
		return fmt.Sprintf("ref %s is %s, not a field — use browser_click for it", req.Ref, describeControl(loc, timeout)), nil
	}

	if err := loc.Clear(playwright.LocatorClearOptions{Timeout: opt}); err != nil {
		return "", err
	}
	n := len([]rune(req.Value))
	var err error
	if n > maxTypedRunes {
		err = loc.Fill(req.Value, playwright.LocatorFillOptions{Timeout: opt})
	} else {
		err = loc.PressSequentially(req.Value, playwright.LocatorPressSequentiallyOptions{
			Delay:   playwright.Float(keyDelayMS),
			Timeout: playwright.Float(max(timeout, float64(n*keyDelayMS*4))),
		})
	}
	if err != nil {
		return "", err
	}
	_ = loc.Blur(playwright.LocatorBlurOptions{Timeout: opt})

	got, maxLen := readField(loc, timeout)
	verdict := fillVerdict(req.Value, got)
	if verdict == verdictNone {
		if err := loc.Fill(req.Value, playwright.LocatorFillOptions{Timeout: opt}); err == nil {
			_ = loc.Blur(playwright.LocatorBlurOptions{Timeout: opt})
			got, maxLen = readField(loc, timeout)
			verdict = fillVerdict(req.Value, got)
		}
	}
	if req.Secret {
		s.markSecretField(loc, req.Ref, req.SecretLabel, timeout)
	}
	return fillNote(verdict, maxLen, n), nil
}

// isTextEditable reports whether typing into an element makes sense. It is
// computed here rather than with Locator.IsEditable, which THROWS on a button
// instead of answering false — and a button is exactly the target this check
// exists to catch.
func isTextEditable(loc playwright.Locator, timeout float64) bool {
	v, err := loc.Evaluate(`el => {
  if (el.isContentEditable) return true;
  if (el.disabled || el.readOnly) return false;
  if (el.tagName === "TEXTAREA") return true;
  if (el.tagName !== "INPUT") return false;
  return !["button","submit","reset","checkbox","radio","file","image","hidden","range","color"].includes((el.type || "text").toLowerCase());
}`, nil, playwright.LocatorEvaluateOptions{Timeout: playwright.Float(timeout)})
	b, _ := v.(bool)
	return err == nil && b
}

// choiceLabel names what was chosen without ever naming a secret: a secret
// choice is described by its placeholder.
func choiceLabel(req actReq) string {
	if req.Secret {
		return req.SecretLabel
	}
	return fmt.Sprintf("%q", req.Value)
}

// readField returns a field's current text and its maxlength (-1 when none).
// The text stays inside the helper: callers turn it into a verdict.
func readField(loc playwright.Locator, timeout float64) (string, int) {
	v, err := loc.Evaluate(`el => ({v: el.isContentEditable ? el.innerText : String(el.value ?? ""), m: typeof el.maxLength === "number" ? el.maxLength : -1})`,
		nil, playwright.LocatorEvaluateOptions{Timeout: playwright.Float(timeout)})
	if err != nil {
		return "", -1
	}
	m, _ := v.(map[string]any)
	text, _ := m["v"].(string)
	maxLen := -1
	switch x := m["m"].(type) {
	case float64:
		maxLen = int(x)
	case int:
		maxLen = x
	}
	return text, maxLen
}

const (
	verdictMatched = "matched"
	verdictNone    = "did not take the value"
)

// fillVerdict compares what was typed with what the field holds, ignoring case
// and everything but letters and digits — a card field that renders
// "4111 1111 1111 1111" took the value, and a name field that upper-cases it
// did too.
func fillVerdict(typed, got string) string {
	w, g := []rune(compareForm(typed)), []rune(compareForm(got))
	if string(w) == string(g) {
		return verdictMatched
	}
	common := 0
	for common < len(w) && common < len(g) && w[common] == g[common] {
		common++
	}
	if common == 0 {
		return verdictNone
	}
	return fmt.Sprintf("partial: %d of %d characters", common, len(w))
}

func compareForm(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// fillNote renders the verdict for the model, with the one hint that turns a
// partial fill into something it can fix: the box is shorter than the value.
func fillNote(verdict string, maxLen, typedRunes int) string {
	note := "fill: " + verdict
	if strings.HasPrefix(verdict, "partial") && maxLen > 0 && typedRunes > maxLen {
		note += fmt.Sprintf(" — the field holds at most %d characters; if the value is longer than the box "+
			"(e.g. a 4-digit year in an MM/YY box), pass a slice such as ${NAME[-2:]}", maxLen)
	}
	if verdict == verdictNone {
		note += " — re-read the page before trying again; the field may need a different format"
	}
	return note
}

// secretFillError is the ONLY error a failed secret fill returns. Playwright's
// errors can quote the text being typed, so a redacted copy is not good enough.
func secretFillError(label, ref string) error {
	return fmt.Errorf("typing %s into ref %s failed — re-read the page before trying again", label, ref)
}

// describeControl names a non-field for the refusal message: its tag and up to
// 40 characters of its visible text.
func describeControl(loc playwright.Locator, timeout float64) string {
	v, err := loc.Evaluate(`el => { const r = el.getAttribute("role"); const t = (el.innerText || el.getAttribute("aria-label") || "").trim().slice(0, 40); return (r || el.tagName.toLowerCase()) + (t ? " \"" + t + "\"" : ""); }`,
		nil, playwright.LocatorEvaluateOptions{Timeout: playwright.Float(timeout)})
	if s, ok := v.(string); ok && err == nil && s != "" {
		return "a " + s
	}
	return "not an input"
}

// markSecretField remembers that a field now holds a secret, by ref and by a DOM
// marker, so every later listing shows it as "filled from ${NAME}" whatever the
// page renders into it.
func (s *hostSession) markSecretField(loc playwright.Locator, ref, label string, timeout float64) {
	if label == "" {
		label = "a stored secret"
	}
	if s.secretRefs == nil {
		s.secretRefs = map[string]string{}
	}
	s.secretRefs[ref] = label
	_, _ = loc.Evaluate(`(el, l) => el.setAttribute("`+secretAttr+`", l)`, label,
		playwright.LocatorEvaluateOptions{Timeout: playwright.Float(timeout)})
}

// labelSecretFields rewrites the note of every listed field known to hold a
// secret. Known by ref first (cheap), then by the DOM marker for fields that
// carry a value — the only ones that can echo anything — so a re-render that
// changed the ref does not drop the label.
func (s *hostSession) labelSecretFields(els []Element) {
	if len(s.secretRefs) == 0 {
		return
	}
	for i := range els {
		e := &els[i]
		role := strings.ToLower(e.Role)
		// A dropdown trigger shows the chosen option as its NAME, so a secret
		// choice (an expiry month) would otherwise be listed in plain sight.
		trigger := role == "button" || role == "combobox"
		if !keptWhenUnnamed[role] && !trigger {
			continue
		}
		_, known := s.secretRefs[e.Ref]
		if !known && !trigger && !strings.Contains(e.Note, "value:") {
			continue
		}
		// The DOM marker is the only proof. A ref seen before is a HINT: a
		// different element on a later page may reuse it, and labelling that one
		// would report an empty field as filled — or rename a Pay button, whose
		// name is what the irreversibility check judges.
		label, err := s.page.Locator("aria-ref="+e.Ref).GetAttribute(secretAttr,
			playwright.LocatorGetAttributeOptions{Timeout: playwright.Float(500)})
		if err != nil || label == "" {
			delete(s.secretRefs, e.Ref)
			continue
		}
		s.secretRefs[e.Ref] = label
		if trigger && !keptWhenUnnamed[role] {
			e.Name = "chosen from " + label
			continue
		}
		if role == "combobox" {
			e.Name = "chosen from " + label
		}
		e.Note = "filled from " + label
	}
}
