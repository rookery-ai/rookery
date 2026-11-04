package browser

import (
	"fmt"
	"strings"

	"github.com/mxschmitt/playwright-go"
)

// coverJS reports whether a pointer at the element's centre would land on the
// element. When it would not, it tries once more with the element scrolled to
// the middle of the viewport (sticky headers and footers often cover only the
// edges), and if it is still covered, describes what is in the way.
//
// The element's own <label> counts as the element: custom checkboxes paint a
// styled label over a transparent input, and calling that "covered" would make
// every such checkbox unclickable.
const coverJS = `el => {
` + overlayHelpersJS + `
  const vis = r => r.width > 0 && r.height > 0;
  const probe = () => {
    const r = el.getBoundingClientRect();
    if (!vis(r)) return null;
    const x = r.left + r.width / 2, y = r.top + r.height / 2;
    if (x < 0 || y < 0 || x > innerWidth || y > innerHeight) return null;
    const top = document.elementFromPoint(x, y);
    if (!top || top === el || el.contains(top) || top.contains(el)) return null;
    if (el.labels && Array.from(el.labels).some(l => l === top || l.contains(top))) { viaLabel = true; return null; }
    return top;
  };
  let viaLabel = false;
  let top = probe();
  if (top) { el.scrollIntoView({block: "center", inline: "center"}); top = probe(); }
  if (!top) return {ok: true, viaLabel};
  let cover = top;
  for (let n = top; n && n !== document.body; n = n.parentElement) {
    const s = getComputedStyle(n);
    const role = (n.getAttribute("role") || "").toLowerCase();
    if (s.position === "fixed" || s.position === "sticky" || role === "dialog" || role === "alertdialog" || n.getAttribute("aria-modal") === "true" || n.tagName === "DIALOG") { cover = n; break; }
  }
  let d = describe(cover);
  // The hit element is often a modal's empty backdrop; the words and buttons
  // are on the panel above it.
  if (!d.text) { const o = bestOverlay(); if (o) d = describe(o); }
  return {ok: false, text: d.text, buttons: d.buttons};
}`

// overlayHelpersJS is shared by coverJS and dialogJS: describe() summarises an
// element by its text and button labels; bestOverlay() finds the frontmost
// fixed-position panel that carries words and at least one button — a custom
// modal or banner that declares no role at all, which can hide every control
// behind it without any dialog role to detect.
const overlayHelpersJS = `
  const clean = s => (s || "").trim().replace(/\s+/g, " ");
  const shown = n => { const r = n.getBoundingClientRect(); const s = getComputedStyle(n);
    return r.width > 0 && r.height > 0 && s.visibility !== "hidden" && s.display !== "none" && s.opacity !== "0"; };
  const btns = n => Array.from(n.querySelectorAll("button,[role=button],input[type=button],input[type=submit],a"))
    .filter(shown).map(b => clean(b.innerText || b.value || b.getAttribute("aria-label"))).filter(Boolean);
  const describe = n => ({text: clean(n.innerText).slice(0, 80), buttons: btns(n).slice(0, 4)});
  // A full-width strip (a sticky header, a bottom action bar, a cookie bar) is
  // page furniture, not the panel a backdrop belongs to: ranking by z-index
  // alone would pick a fixed action footer over the slide-in panel actually
  // on screen. So strips are skipped and the LARGEST remaining
  // panel wins, with z-index breaking ties.
  const bestOverlay = () => {
    let best = null, bestArea = -1, bestZ = -Infinity;
    for (const n of document.body.querySelectorAll("*")) {
      const s = getComputedStyle(n);
      if (s.position !== "fixed" || !shown(n) || !clean(n.innerText) || !btns(n).length) continue;
      const r = n.getBoundingClientRect();
      if (r.width >= innerWidth * 0.9 && r.height < innerHeight * 0.25) continue;
      const area = r.width * r.height;
      let z = parseInt(s.zIndex, 10); if (isNaN(z)) z = 0;
      if (area > bestArea || (area === bestArea && z >= bestZ)) { best = n; bestArea = area; bestZ = z; }
    }
    return best;
  };
`

// checkReachable decides whether a click or fill can land on its target, and
// when it cannot, returns a refusal the model can act on.
//
// Without it the model met Playwright's own behaviour: wait the full timeout,
// then fail with "element intercepts pointer events" and no name for what was
// in the way, so a control under a cookie banner looks merely broken.
//
// A refusal is returned as a NOTE (empty error): the action was deliberately
// not taken, and the model should deal with the obstruction, not retry.
func checkReachable(loc playwright.Locator, ref string, timeout float64) (string, error) {
	r, err := reach(loc, ref, timeout)
	return r.refusal, err
}

// reachability is checkReachable's full answer. viaLabel means the element is
// painted over by its OWN <label> — the custom-checkbox pattern — where a real
// user's click lands on the label and the label toggles the input. Playwright
// refuses that click ("label intercepts pointer events"), so the caller clicks
// the same point with Force instead.
type reachability struct {
	refusal  string
	viaLabel bool
}

func reach(loc playwright.Locator, ref string, timeout float64) (reachability, error) {
	opt := playwright.Float(timeout)
	if dis, err := loc.IsDisabled(playwright.LocatorIsDisabledOptions{Timeout: opt}); err != nil {
		return reachability{}, err
	} else if dis {
		return reachability{refusal: fmt.Sprintf("ref %s is disabled right now — the page may be waiting for something; re-read it before trying again", ref)}, nil
	}
	_ = loc.ScrollIntoViewIfNeeded(playwright.LocatorScrollIntoViewIfNeededOptions{Timeout: opt})
	v, err := loc.Evaluate(coverJS, nil, playwright.LocatorEvaluateOptions{Timeout: opt})
	if err != nil {
		// Could not judge it: let the action itself report whatever is wrong.
		return reachability{}, nil
	}
	m, _ := v.(map[string]any)
	if m == nil {
		return reachability{}, nil
	}
	if ok, _ := m["ok"].(bool); ok {
		via, _ := m["viaLabel"].(bool)
		return reachability{viaLabel: via}, nil
	}
	return reachability{refusal: describeCover(ref, m)}, nil
}

func describeCover(ref string, m map[string]any) string {
	text, _ := m["text"].(string)
	what := "another element"
	if text != "" {
		what = fmt.Sprintf("an overlay (%q)", truncateRunes(text, 80))
	}
	msg := fmt.Sprintf("ref %s is covered by %s", ref, what)
	if bs := buttonList(m); bs != "" {
		msg += " with buttons " + bs
	}
	return msg + " — deal with it first (it was not clicked)"
}

func buttonList(m map[string]any) string {
	var buttons []string
	if list, ok := m["buttons"].([]any); ok {
		for _, b := range list {
			if s, ok := b.(string); ok && s != "" {
				buttons = append(buttons, fmt.Sprintf("%q", truncateRunes(s, 40)))
			}
		}
	}
	return strings.Join(buttons, ", ")
}

// dialogJS finds a visible modal dialog and returns its heading or first words.
// While one is open, Chromium removes the accessible names of everything behind
// it, so a listing full of nameless controls has a cause the model should be
// told.
const dialogJS = `() => {
` + overlayHelpersJS + `
  const title = d => { const h = d.querySelector("h1,h2,h3,h4,h5,h6,[role=heading]"); return clean((h && h.innerText) || d.innerText).slice(0, 80) || "(untitled)"; };
  const sel = '[role=dialog],[role=alertdialog],dialog[open],[aria-modal="true"]';
  for (const d of document.querySelectorAll(sel)) {
    if (shown(d)) return {title: title(d), buttons: btns(d).slice(0, 4)};
  }
  // A modal that declares no role: something fixed sits over the middle of
  // the viewport. A sticky header or a cookie banner at the bottom does not,
  // so neither is reported on every listing.
  const top = document.elementFromPoint(innerWidth / 2, innerHeight / 2);
  for (let n = top; n && n !== document.body; n = n.parentElement) {
    if (getComputedStyle(n).position !== "fixed") continue;
    const panel = (clean(n.innerText) && btns(n).length) ? n : bestOverlay();
    if (panel) return {title: title(panel), buttons: btns(panel).slice(0, 4)};
    break;
  }
  return null;
}`

// focusJS describes the focused element by role and label — never its value.
// Focus inside a cross-origin frame cannot be inspected and is reported as such.
const focusJS = `() => {
  let el = document.activeElement, doc = document;
  while (el && el.tagName === "IFRAME") {
    try { doc = el.contentDocument; if (!doc) return "inside a frame"; el = doc.activeElement; } catch (e) { return "inside a frame"; }
  }
  if (!el || el === doc.body) return "nothing";
  const role = el.getAttribute("role") || (el.tagName === "INPUT" ? (el.type === "checkbox" || el.type === "radio" ? el.type : "textbox") : el.tagName.toLowerCase());
  const lab = (el.getAttribute("aria-label") || (el.labels && el.labels[0] && el.labels[0].innerText) || el.getAttribute("placeholder") ||
    (el.tagName === "INPUT" || el.tagName === "TEXTAREA" ? "" : el.innerText) || "").trim().replace(/\s+/g, " ").slice(0, 60);
  return role + (lab ? " \"" + lab + "\"" : "");
}`

// focusNote reports where focus landed after a key press, so tabbing through a
// page is not done blind.
func focusNote(page playwright.Page) string {
	v, err := page.Evaluate(focusJS)
	if s, _ := v.(string); err == nil && s != "" {
		return "focus: " + s
	}
	return ""
}

func joinNotes(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "; " + b
}

// openDialogNote returns the note for a visible modal, or "".
func openDialogNote(page playwright.Page) string {
	v, err := page.Evaluate(dialogJS)
	m, _ := v.(map[string]any)
	if err != nil || m == nil {
		return ""
	}
	title, _ := m["title"].(string)
	note := fmt.Sprintf("a dialog is open: %q", title)
	if bs := buttonList(m); bs != "" {
		note += " with buttons " + bs
	}
	return note + " — controls behind it are hidden until it is closed"
}
