package browser

import (
	"fmt"
	"strconv"

	"github.com/mxschmitt/playwright-go"
)

// Scripted dropdowns — a button plus a list that a script shows and hides —
// are a common replacement for <select>. Their items are list items and links,
// not interactive roles, and their choice reaches the form through a click
// handler, so neither typing, arrow keys nor SelectOption can drive them.
//
// browser_fill on such a trigger therefore opens it and clicks the option whose
// text matches, reusing matchOption's rules for <select>.

const triggerJS = `el => {
  const a = n => el.getAttribute(n);
  const toggle = (a("data-toggle") || a("data-bs-toggle") || "").toLowerCase();
  const popup = a("aria-haspopup");
  return toggle === "dropdown" || (popup !== null && popup !== "false") ||
    a("aria-expanded") !== null || !!a("aria-controls") || (a("role") || "").toLowerCase() === "combobox";
}`

// isDropdownTrigger reports whether a non-editable target opens a popup list.
func isDropdownTrigger(loc playwright.Locator, timeout float64) bool {
	v, err := loc.Evaluate(triggerJS, nil, playwright.LocatorEvaluateOptions{Timeout: playwright.Float(timeout)})
	b, _ := v.(bool)
	return err == nil && b
}

// beforeOpenJS marks every option-like element already visible, so the options
// collected after the click are only the ones the click revealed, and returns
// the trigger's state for the did-it-register check.
const beforeOpenJS = `el => {
  const vis = e => { const r = e.getBoundingClientRect(); const s = getComputedStyle(e); return r.width > 0 && r.height > 0 && s.visibility !== "hidden" && s.display !== "none"; };
  el.ownerDocument.querySelectorAll('[role=option],[role^=menuitem],li,a').forEach(e => { if (vis(e)) e.setAttribute("data-rk-pre", "1"); });
  return (el.innerText || el.value || "").trim() + "\u0000" + (el.getAttribute("aria-activedescendant") || "");
}`

// collectOptionsJS finds the options the click revealed, ONLY inside the popup
// the trigger controls or sits beside. Restricting the search to newly visible
// elements in that container is what guarantees an option match can never land
// on an unrelated control — a "Confirm" button directly under the picker.
const collectOptionsJS = `async el => {
  await new Promise(r => setTimeout(r, 250));
  const doc = el.ownerDocument;
  const vis = e => { const r = e.getBoundingClientRect(); const s = getComputedStyle(e); return r.width > 0 && r.height > 0 && s.visibility !== "hidden" && s.display !== "none"; };
  const roots = [];
  const ids = (el.getAttribute("aria-controls") || el.getAttribute("aria-owns") || "").split(/\s+/).filter(Boolean);
  ids.forEach(id => { const n = doc.getElementById(id); if (n) roots.push(n); });
  if (!roots.length) {
    for (let s = el.nextElementSibling; s; s = s.nextElementSibling) roots.push(s);
    if (el.parentElement) roots.push(el.parentElement);
  }
  doc.querySelectorAll('[role=listbox],[role=menu]').forEach(n => { if (vis(n)) roots.push(n); });
  const sel = '[role=option],[role^=menuitem],li,a';
  const seen = new Set(); const out = [];
  for (const r of roots) for (const e of r.querySelectorAll(sel)) {
    if (seen.has(e) || e === el || e.hasAttribute("data-rk-pre") || !vis(e)) continue;
    seen.add(e);
    if (e.querySelector('[role=option],[role^=menuitem],a')) continue; // a wrapper; its child is the option
    e.setAttribute("data-rk-opt", String(out.length));
    out.push((e.innerText || e.getAttribute("aria-label") || "").trim().replace(/\s+/g, " "));
  }
  doc.querySelectorAll('[data-rk-pre]').forEach(e => e.removeAttribute("data-rk-pre"));
  return out;
}`

const cleanupOptionsJS = `el => { el.ownerDocument.querySelectorAll('[data-rk-opt],[data-rk-pre]').forEach(e => { e.removeAttribute("data-rk-opt"); e.removeAttribute("data-rk-pre"); }); }`

func (h *browserHost) chooseFromDropdown(s *hostSession, loc playwright.Locator, req actReq, timeout float64) (string, error) {
	opt := playwright.Float(timeout)
	evalOpt := playwright.LocatorEvaluateOptions{Timeout: opt}
	defer func() { _, _ = loc.Evaluate(cleanupOptionsJS, nil, evalOpt) }()

	beforeV, err := loc.Evaluate(beforeOpenJS, nil, evalOpt)
	if err != nil {
		return "", err
	}
	before, _ := beforeV.(string)
	if err := loc.Click(playwright.LocatorClickOptions{Timeout: opt}); err != nil {
		return "", err
	}
	raw, err := loc.Evaluate(collectOptionsJS, nil, evalOpt)
	if err != nil {
		return "", err
	}
	var opts []selectOption
	if list, ok := raw.([]any); ok {
		for i, item := range list {
			label, _ := item.(string)
			opts = append(opts, selectOption{Value: strconv.Itoa(i), Label: label})
		}
	}
	if len(opts) == 0 {
		return fmt.Sprintf("ref %s looks like a dropdown, but clicking it revealed no options — re-read the page; the list may have opened somewhere else", req.Ref), nil
	}
	idx, err := matchOption(opts, req.Value)
	if err != nil {
		if req.Secret {
			// matchOption quotes what it was asked to match, which here is a
			// secret. Say the placeholder instead.
			return fmt.Sprintf("no single option of this dropdown matches %s — options: %s", req.SecretLabel, listOptions(opts)), nil
		}
		return "", err
	}
	// The irreversibility check judged the TRIGGER, not this option. A trigger
	// can be anything that toggles — an account menu, an accordion — and an
	// option like "Delete account" must go through browser_click, where the
	// check applies, rather than be clicked here unjudged.
	if i, _ := strconv.Atoi(idx); LooksIrreversible(opts[i].Label) {
		return fmt.Sprintf("the option %q cannot be undone, so it was not chosen here — it is open on the page now; "+
			"use browser_click on it, which applies the permission check for irreversible actions", opts[i].Label), nil
	}
	handle, err := loc.EvaluateHandle(`(el, i) => el.ownerDocument.querySelector('[data-rk-opt="' + i + '"]')`, idx,
		playwright.LocatorEvaluateHandleOptions{Timeout: opt})
	if err != nil {
		return "", err
	}
	el := handle.AsElement()
	if el == nil {
		return "", fmt.Errorf("the chosen option disappeared before it could be clicked")
	}
	if err := el.Click(playwright.ElementHandleClickOptions{Timeout: opt}); err != nil {
		return "", err
	}

	afterV, _ := loc.Evaluate(`el => (el.innerText || el.value || "").trim() + "\u0000" + (el.getAttribute("aria-activedescendant") || "")`, nil, evalOpt)
	after, _ := afterV.(string)
	if req.Secret {
		s.markSecretField(loc, req.Ref, req.SecretLabel, timeout)
	}
	chosen := choiceLabel(req)
	if !req.Secret {
		i, _ := strconv.Atoi(idx)
		chosen = fmt.Sprintf("%q", opts[i].Label)
	}
	if after == before {
		return fmt.Sprintf("fill: chose %s, but the dropdown did not register the choice visibly — re-read the page to confirm", chosen), nil
	}
	return "fill: chose " + chosen, nil
}
