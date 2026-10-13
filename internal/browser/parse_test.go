package browser

import (
	"os"
	"strings"
	"testing"
)

// The fixture is a REAL snapshot captured from https://github.com/login through
// the guarded proxy, not a hand-written approximation. That matters: the whole
// value of this parser is that it matches what Playwright actually emits, and a
// fixture invented to match the parser would prove nothing.
func loadFixture(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/github-login.snapshot")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(b)
}

func TestParseAriaSnapshotFindsTheLoginControls(t *testing.T) {
	els := ParseAriaSnapshot(loadFixture(t))
	byRef := map[string]Element{}
	for _, e := range els {
		byRef[e.Ref] = e
	}

	for _, want := range []struct{ ref, role, name string }{
		{"e25", "textbox", "Username or email address"},
		{"e28", "textbox", "Password"},
		{"e31", "button", "Sign in"},
		{"e29", "link", "Forgot password?"},
	} {
		got, ok := byRef[want.ref]
		if !ok {
			t.Fatalf("ref %s missing from parse", want.ref)
		}
		if got.Role != want.role || got.Name != want.name {
			t.Errorf("ref %s = %q %q, want %q %q", want.ref, got.Role, got.Name, want.role, want.name)
		}
	}
}

// A "- /url: ..." line describes the link above it. Parsed as an element it
// would put an unclickable "/url" row under every single link on the page,
// crowding out real controls in a list that is already capped.
func TestParseAriaSnapshotSkipsPropertyLines(t *testing.T) {
	for _, e := range ParseAriaSnapshot(loadFixture(t)) {
		if strings.HasPrefix(e.Role, "/") {
			t.Fatalf("property line parsed as an element: %+v", e)
		}
	}
}

// Every row the model is offered must be addressable. A row without a ref
// cannot be clicked, so offering it invites a call that can only fail.
func TestParseAriaSnapshotDropsRowsWithoutARef(t *testing.T) {
	snap := "- button \"No ref here\"\n- button \"Has one\" [ref=e5]\n"
	els := ParseAriaSnapshot(snap)
	if len(els) != 1 || els[0].Ref != "e5" {
		t.Fatalf("got %+v, want only the e5 row", els)
	}
}

func TestParseAriaSnapshotHandlesQuotesInNames(t *testing.T) {
	els := ParseAriaSnapshot(`- button "Say \"hi\" now" [ref=e3]`)
	if len(els) != 1 {
		t.Fatalf("got %d elements, want 1", len(els))
	}
	if els[0].Name != `Say "hi" now` {
		t.Errorf("name = %q", els[0].Name)
	}
}

// A textbox's current contents decide whether the model must clear the field
// before typing. Losing it produces double-typed values in forms.
func TestParseAriaSnapshotCapturesFieldValueAndEmptiness(t *testing.T) {
	els := ParseAriaSnapshot("- textbox \"Name\" [ref=e1]: Ilija\n- textbox \"Email\" [ref=e2]")
	if len(els) != 2 {
		t.Fatalf("got %d", len(els))
	}
	if !strings.Contains(els[0].Note, "value: Ilija") {
		t.Errorf("filled field note = %q", els[0].Note)
	}
	if els[1].Note != "empty" {
		t.Errorf("empty field note = %q", els[1].Note)
	}
}

func TestParseAriaSnapshotReportsDisabledAndChecked(t *testing.T) {
	els := ParseAriaSnapshot("- button \"Pay\" [disabled] [ref=e1]\n- checkbox \"Agree\" [checked] [ref=e2]")
	if !strings.Contains(els[0].Note, "disabled") {
		t.Errorf("note = %q, want disabled", els[0].Note)
	}
	if !strings.Contains(els[1].Note, "checked") {
		t.Errorf("note = %q, want checked", els[1].Note)
	}
}

// [cursor=pointer] is on most rows of a real page and tells the model nothing.
// The element list is capped, so noise here costs real controls at the bottom.
func TestParseAriaSnapshotDropsNoiseAttributes(t *testing.T) {
	els := ParseAriaSnapshot(`- link "Terms" [ref=e39] [cursor=pointer]`)
	if len(els) != 1 {
		t.Fatalf("got %d", len(els))
	}
	if strings.Contains(els[0].Note, "cursor") || strings.Contains(els[0].Note, "pointer") {
		t.Errorf("note carries noise: %q", els[0].Note)
	}
}

func TestFilterInteractiveKeepsOnlyActionableNamedControls(t *testing.T) {
	els := FilterInteractive(ParseAriaSnapshot(loadFixture(t)))
	for _, e := range els {
		if e.Name == "" {
			t.Errorf("unnamed control offered: %+v", e)
		}
		if !interactiveRoles[e.Role] {
			t.Errorf("non-interactive role offered: %+v", e)
		}
	}
	// The three controls a sign-in flow actually needs must survive filtering.
	var names []string
	for _, e := range els {
		names = append(names, e.Name)
	}
	joined := strings.Join(names, "|")
	for _, want := range []string{"Username or email address", "Password", "Sign in"} {
		if !strings.Contains(joined, want) {
			t.Errorf("filtering dropped %q; kept: %s", want, joined)
		}
	}
}

// Refs arrive as strings, so a plain sort puts e10 before e2. To a model that
// reads as the page having reshuffled between two calls, which invites it to
// re-scan instead of acting on the ref it already chose.
func TestSortElementsStableOrdersRefsNumerically(t *testing.T) {
	els := []Element{{Ref: "e10"}, {Ref: "e2"}, {Ref: "e1"}}
	SortElementsStable(els)
	got := []string{els[0].Ref, els[1].Ref, els[2].Ref}
	want := []string{"e1", "e2", "e10"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// A form field whose <label> is not linked to it has no accessible name, so the
// filter used to drop it — and the field vanished from the agent's view while a
// human sees "Username:" right beside it. demoblaze.com's login is exactly this
// (label for="log-name" on an input with id="loginusername"); a real run clicked
// "Log in" 24 times and pressed Tab ~100 times looking for fields it was never
// shown. The visible label IS in the snapshot: the text line just before the field.
func TestParseAriaSnapshotNamesUnlabeledFieldsFromTheTextBeforeThem(t *testing.T) {
	snap := `- dialog "Log in" [ref=e150]:
  - document [ref=e151]:
    - heading "Log in" [level=5] [ref=e152]
    - button "Close" [ref=e153]
    - text: "Username:"
    - textbox [ref=e155]
    - text: "Password:"
    - textbox [ref=e157]
    - button "Close" [ref=e158]
    - button "Log in" [ref=e159]`
	els := FilterInteractive(ParseAriaSnapshot(snap))
	byRef := map[string]Element{}
	for _, e := range els {
		byRef[e.Ref] = e
	}
	user, ok := byRef["e155"]
	if !ok || user.Name != "Username" {
		t.Fatalf("unlabeled username field not offered under its visible label: %+v", els)
	}
	if pw, ok := byRef["e157"]; !ok || pw.Name != "Password" {
		t.Fatalf("unlabeled password field not offered: %+v", els)
	}
	if !strings.Contains(user.Note, "label inferred") {
		t.Errorf("an inferred name must say so, or the model trusts it as a real label: %q", user.Note)
	}
}

// The inference is narrow on purpose: only a FORM FIELD borrows a name, and only
// from text directly before it. An unnamed button still has nothing a model
// could choose by, and text that a named control sits between must not leak
// past it onto a later field.
func TestParseAriaSnapshotInfersNamesNarrowly(t *testing.T) {
	snap := `- text: "Shipping"
- button [ref=e1]
- text: "Notes"
- button "Help" [ref=e2]
- textbox [ref=e3]
- textbox "Email" [ref=e4]`
	els := FilterInteractive(ParseAriaSnapshot(snap))
	for _, e := range els {
		if e.Ref == "e1" {
			t.Errorf("an unnamed BUTTON borrowed a name: %+v", e)
		}
		if e.Ref == "e3" {
			t.Errorf("text separated from the field by another control was used as its label: %+v", e)
		}
		if e.Ref == "e4" && e.Name != "Email" {
			t.Errorf("a real accessible name was replaced: %+v", e)
		}
	}
}
