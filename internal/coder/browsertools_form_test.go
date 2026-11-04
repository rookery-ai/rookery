package coder

import (
	"context"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/browser"
)

func formResult() browser.Result {
	return browser.Result{
		FinalURL: "https://shop.example/form",
		Text:     "Review your order",
		Elements: []browser.Element{
			{Ref: "e1", Role: "textbox", Name: "Card"},
			{Ref: "e2", Role: "button", Name: "Month"},
			{Ref: "e3", Role: "button", Name: "Pay"},
		},
	}
}

func TestActResultShowsActNoteFirst(t *testing.T) {
	res := formResult()
	res.ActNote = "fill: matched"
	out, _, _ := renderActResult(res, "", "", true)
	note, list := strings.Index(out, "fill: matched"), strings.Index(out, "interactive controls")
	if note < 0 || list < 0 || note > list {
		t.Fatalf("act note must come before the controls:\n%s", out)
	}
}

func TestUnchangedControlsCompacted(t *testing.T) {
	_, es, ts := renderActResult(formResult(), "", "", false)
	out, _, _ := renderActResult(formResult(), es, ts, false)
	if !strings.Contains(out, "interactive controls: unchanged (3)") || strings.Contains(out, `"Card"`) {
		t.Fatalf("second identical listing not compacted:\n%s", out)
	}
	if !strings.Contains(out, "page text: unchanged") || strings.Contains(out, "Review your order") {
		t.Fatalf("unchanged text not compacted:\n%s", out)
	}
	full, _, _ := renderActResult(formResult(), es, ts, true)
	if !strings.Contains(full, `"Card"`) || !strings.Contains(full, "Review your order") {
		t.Fatalf("a full render must always list everything:\n%s", full)
	}
	changed := formResult()
	changed.Elements[0].Note = "filled from ${CARD}"
	out, _, _ = renderActResult(changed, es, ts, false)
	if !strings.Contains(out, "filled from ${CARD}") {
		t.Fatalf("a changed listing must be shown:\n%s", out)
	}
}

// A click on a page that never reacts still "succeeds" as a call, so the turn budget counted it as progress and the run
// could go on until the hard ceiling. A click that changes nothing is fruitless.
func TestClickThatChangesNothingIsFruitlessAndSaysSo(t *testing.T) {
	fb := &fakeBrowser{available: true, result: formResult()}
	h := &hostToolSet{browser: fb, includeExecTools: true}
	first := h.dispatchBrowserAct(context.Background(), "browser_click", browserCallArgs{Ref: "e2"})
	if h.lastFruitless {
		t.Fatalf("the first click has nothing to compare against and must not count as fruitless:\n%s", first)
	}
	second := h.dispatchBrowserAct(context.Background(), "browser_click", browserCallArgs{Ref: "e2"})
	if !h.lastFruitless {
		t.Fatal("a click that changed nothing was counted as progress")
	}
	if !strings.Contains(second, "nothing on the page changed") {
		t.Fatalf("the model was not told the click did nothing:\n%s", second)
	}
	// A fill always changes its field, so it is never caught by this.
	fb.result.Elements[0].Note = "value: x"
	h.lastFruitless = false
	h.dispatchBrowserAct(context.Background(), "browser_fill", browserCallArgs{Ref: "e1", Value: "x"})
	if h.lastFruitless {
		t.Error("a fill that changed its field was counted as fruitless")
	}
}

func TestPressStreakNudge(t *testing.T) {
	h := &hostToolSet{}
	for i := 1; i < 8; i++ {
		if n := h.notePress("Tab"); n != "" {
			t.Fatalf("nudged too early, at press %d: %q", i, n)
		}
	}
	if n := h.notePress("Tab"); !strings.Contains(n, "pressed Tab 8 times in a row") {
		t.Fatalf("8th press: %q", n)
	}
	if n := h.notePress("Enter"); n != "" {
		t.Fatalf("a different key must reset the streak: %q", n)
	}
}

func TestFillDescriptionMentionsSlicesAndDropdowns(t *testing.T) {
	h := &hostToolSet{browser: &fakeBrowser{available: true}, includeExecTools: true}
	for _, tool := range h.browserTools() {
		if tool.Name != "browser_fill" {
			continue
		}
		for _, want := range []string{"${NAME[-2:]}", "dropdown", "not a field", "key by key"} {
			if !strings.Contains(tool.Description, want) {
				t.Errorf("browser_fill description lacks %q", want)
			}
		}
		return
	}
	t.Fatal("browser_fill not offered")
}
