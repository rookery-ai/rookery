package coder

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/browser"
	"github.com/rookery-ai/rookery/internal/llm"
)

// The helper redacts what it collects. This pins the second layer: whatever the
// helper hands back, the host does not pass a typed secret to the model — not
// raw, not reformatted, not as its masked tail — and the step line shows only
// the placeholder.
func TestBrowserFillResultNeverCarriesTheSecret(t *testing.T) {
	const card = "4111111111114242"
	leaky := browser.Result{
		FinalURL: "https://shop.example/form?pan=" + card,
		Title:    "Pay 4111 1111 1111 4242",
		Text:     "Card •••• 4242 accepted. Number 4111-1111-1111-4242.",
		ActNote:  "fill: matched (" + card + ")",
		Elements: []browser.Element{
			{Ref: "e1", Role: "textbox", Name: "Card number", Note: "value: 4111 1111 1111 4242"},
		},
	}
	fb := &fakeBrowser{available: true, result: leaky}
	h := &hostToolSet{
		browser:          fb,
		includeExecTools: true,
		subprocessEnv:    map[string]string{"CARD": card},
	}
	out := h.dispatchBrowserAct(context.Background(), "browser_fill", browserCallArgs{Ref: "e1", Value: "${CARD}"})
	for _, leak := range []string{card, "4111 1111", "4111-1111", "4242"} {
		if strings.Contains(out, leak) {
			t.Fatalf("tool result carries %q:\n%s", leak, out)
		}
	}
	if fb.lastAct.SecretLabel != "${CARD}" || !fb.lastAct.ValueIsSecret {
		t.Errorf("helper was not told the placeholder: %+v", fb.lastAct)
	}

	args, _ := json.Marshal(map[string]string{"ref": "e1", "value": "${CARD}"})
	h.rememberRefs(leaky.Elements)
	if m := h.browserMilestone(llm.ToolCall{Name: "browser_fill", Args: args}); strings.Contains(m, "4111") || !strings.Contains(m, "${CARD}") {
		t.Fatalf("milestone = %q", m)
	}
}
