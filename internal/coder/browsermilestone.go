package coder

import (
	"encoding/json"
	"fmt"

	"github.com/rookery-ai/rookery/internal/browser"
	"github.com/rookery-ai/rookery/internal/llm"
)

// rememberRefs records the names of the elements in the latest page listing.
func (h *hostToolSet) rememberRefs(els []browser.Element) {
	m := make(map[string]string, len(els))
	for _, e := range els {
		if e.Ref != "" && e.Name != "" {
			m[e.Ref] = e.Name
		}
	}
	h.refNames = m
}

// browserMilestone is the step line for a click or fill, naming the element.
//
// toolMilestone renders a call from its arguments, and for the acting tools the
// argument is a ref — "f16e211" — which tells the person reading a run's steps
// nothing about what their agent did. The name is already known from the page
// listing the agent chose the ref from, so it is used when known. "" means "use
// toolMilestone": an unknown ref is shown as itself rather than given a guessed
// name, because a wrong name in a run log is worse than an opaque one.
func (h *hostToolSet) browserMilestone(tc llm.ToolCall) string {
	if tc.Name != "browser_click" && tc.Name != "browser_fill" {
		return ""
	}
	var a struct {
		Ref   string `json:"ref"`
		Value string `json:"value"`
	}
	if json.Unmarshal(tc.Args, &a) != nil {
		return ""
	}
	name := h.refNames[a.Ref]
	if name == "" {
		return ""
	}
	name = truncateRunes(name, 40)
	if tc.Name == "browser_click" {
		return fmt.Sprintf("🔧 browser_click %q", name)
	}
	// The value is shown exactly as the model wrote it: a stored credential is
	// a ${NAME} placeholder at this point, never the secret itself.
	return fmt.Sprintf("🔧 browser_fill %q ← %s", name, truncateRunes(a.Value, 40))
}
