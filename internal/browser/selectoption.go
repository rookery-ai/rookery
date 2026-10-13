package browser

import (
	"fmt"
	"strings"

	"github.com/mxschmitt/playwright-go"
)

// selectOption is one <option> of a native <select>, as the page reports it.
type selectOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// matchOption decides which option a browser_fill value means.
//
// browser_fill is how a model enters text, and a <select> is where the toolset
// used to stop working: Playwright's Fill rejects it, and pressing keys one tool
// call at a time cannot drive the browser's type-ahead (the calls are seconds
// apart). A real run pressed ArrowDown 105 times on a country field and gave up.
// So a fill aimed at a <select> chooses an option instead — the model writes
// what it would type, "Portugal", and this maps it to an option value.
//
// Order is most to least specific: exact label, exact value, case-insensitive
// label, then a case-insensitive substring of a label. The substring step must
// be UNIQUE: on a checkout form, silently choosing "United States Minor Outlying
// Islands" for "United States" and carrying on is worse than an error the model
// can correct from, so an ambiguous or missing match lists the candidates.
func matchOption(opts []selectOption, want string) (string, error) {
	w := strings.TrimSpace(want)
	for _, o := range opts {
		if o.Label == w {
			return o.Value, nil
		}
	}
	for _, o := range opts {
		if o.Value == w && w != "" {
			return o.Value, nil
		}
	}
	lw := strings.ToLower(w)
	for _, o := range opts {
		if strings.ToLower(strings.TrimSpace(o.Label)) == lw {
			return o.Value, nil
		}
	}
	var hits []selectOption
	if lw != "" {
		for _, o := range opts {
			if strings.Contains(strings.ToLower(o.Label), lw) {
				hits = append(hits, o)
			}
		}
	}
	switch len(hits) {
	case 1:
		return hits[0].Value, nil
	case 0:
		return "", fmt.Errorf("this is a dropdown and no option matches %q — pass one option's visible text exactly. Options: %s", w, listOptions(opts))
	default:
		return "", fmt.Errorf("this is a dropdown and %q matches more than one option — pass one of these exactly: %s", w, listOptions(hits))
	}
}

// listOptions renders option labels for an error message, bounded so a
// 250-country dropdown cannot spend the whole tool-result budget.
func listOptions(opts []selectOption) string {
	const maxBytes = 1000
	var b strings.Builder
	for i, o := range opts {
		label := strings.TrimSpace(o.Label)
		if label == "" {
			continue
		}
		piece := fmt.Sprintf("%q", label)
		if i > 0 && b.Len() > 0 {
			piece = ", " + piece
		}
		if b.Len()+len(piece) > maxBytes {
			fmt.Fprintf(&b, ", … (%d options in total)", len(opts))
			break
		}
		b.WriteString(piece)
	}
	return b.String()
}

// isSelect reports whether a located element is a native <select>. Any error
// (stale ref, detached node) answers false, so the ordinary Fill path runs and
// produces the error the model already knows how to act on.
func isSelect(loc playwright.Locator, timeout float64) bool {
	v, err := loc.Evaluate(`el => el.tagName`, nil, playwright.LocatorEvaluateOptions{Timeout: playwright.Float(timeout)})
	if err != nil {
		return false
	}
	tag, _ := v.(string)
	return strings.EqualFold(tag, "select")
}

// selectByText reads the dropdown's options, resolves the model's text with
// matchOption, and selects by VALUE — the one identifier matchOption has proved
// is the intended option.
func selectByText(loc playwright.Locator, want string, timeout float64) error {
	raw, err := loc.Evaluate(`el => Array.from(el.options).map(o => ({value: o.value, label: (o.label || o.text || "").trim()}))`,
		nil, playwright.LocatorEvaluateOptions{Timeout: playwright.Float(timeout)})
	if err != nil {
		return err
	}
	var opts []selectOption
	if list, ok := raw.([]any); ok {
		for _, item := range list {
			m, _ := item.(map[string]any)
			value, _ := m["value"].(string)
			label, _ := m["label"].(string)
			opts = append(opts, selectOption{Value: value, Label: label})
		}
	}
	value, err := matchOption(opts, want)
	if err != nil {
		return err
	}
	_, err = loc.SelectOption(playwright.SelectOptionValues{Values: &[]string{value}},
		playwright.LocatorSelectOptionOptions{Timeout: playwright.Float(timeout)})
	return err
}
