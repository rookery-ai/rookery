package browser

import (
	"os"
	"strings"
	"testing"
)

// These fixtures are invented. The shapes are what matter: each one is a common
// way forms are built.

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func byRef(els []Element) map[string]Element {
	m := map[string]Element{}
	for _, e := range els {
		m[e.Ref] = e
	}
	return m
}

func TestRowCheckboxNamedFromRow(t *testing.T) {
	els := byRef(FilterInteractive(ParseAriaSnapshot(fixture(t, "row-checkbox.snapshot"))))
	cb, ok := els["f2e83"]
	if !ok {
		t.Fatalf("the row's checkbox is not offered: %+v", els)
	}
	if !strings.Contains(cb.Name, "Invoice") || !strings.Contains(cb.Name, "120.00") {
		t.Errorf("name = %q, want the row's cells", cb.Name)
	}
	if !strings.Contains(cb.Note, "named from its table row") || !strings.Contains(cb.Note, "disabled") {
		t.Errorf("note = %q", cb.Note)
	}
	// The header row's words must not leak into the data row's name.
	if strings.Contains(cb.Name, "Description") {
		t.Errorf("header text leaked into the row name: %q", cb.Name)
	}
}

func TestParagraphLabelsNameFields(t *testing.T) {
	els := byRef(FilterInteractive(ParseAriaSnapshot(fixture(t, "paragraph-labels.snapshot"))))
	if e := els["f3e260"]; !strings.Contains(e.Name, "e-mail for the payment confirmation") {
		t.Errorf("email field = %+v", e)
	}
	if e := els["f3e279"]; !strings.Contains(e.Name, "phone number") {
		t.Errorf("phone field = %+v", e)
	}
	// The button keeps its own name — it must never look like the field.
	if e := els["f3e282"]; e.Name != "Continue to payment" {
		t.Errorf("button = %+v", e)
	}
}

func TestPlaceholderNamesField(t *testing.T) {
	els := FilterInteractive(ParseAriaSnapshot("- textbox [ref=e5]:\n  - /placeholder: MM/YY\n"))
	if len(els) != 1 || els[0].Name != "MM/YY" || !strings.Contains(els[0].Note, "placeholder") {
		t.Fatalf("got %+v", els)
	}
	// A named field keeps its name but still carries the format hint.
	els = FilterInteractive(ParseAriaSnapshot("- textbox \"Expiry\" [ref=e6]:\n  - /placeholder: MM/YY\n"))
	if len(els) != 1 || els[0].Name != "Expiry" || !strings.Contains(els[0].Note, "placeholder MM/YY") {
		t.Fatalf("got %+v", els)
	}
}

// testdata/placeholder-value.snapshot is Playwright 1.62's real "ai" output for
// filled inputs. A field WITH a placeholder carries its value on a child
// "- text:" line, not inline — so the parser called every such field "empty"
// whatever it held, even straight after a fill that reported matched.
func TestPlaceholderFieldValueIsItsChildText(t *testing.T) {
	els := byRef(FilterInteractive(ParseAriaSnapshot(fixture(t, "placeholder-value.snapshot"))))
	if e := els["e3"]; !strings.Contains(e.Note, "value: TEST HOLDER") || strings.Contains(e.Note, "empty") {
		t.Errorf("filled placeholder field: %+v", e)
	}
	if e := els["e5"]; !strings.Contains(e.Note, "value: plain value") {
		t.Errorf("inline value: %+v", e)
	}
	if e := els["e7"]; !strings.Contains(e.Note, "empty") {
		t.Errorf("an empty placeholder field must still read empty: %+v", e)
	}
	// The value must not become the label of the next unlabelled field.
	if e := els["e10"]; e.Name == "250" {
		t.Errorf("a field's value was used as the next field's label: %+v", e)
	}
	if e := els["e9"]; e.Name != "Amount" || !strings.Contains(e.Note, "value: 250") {
		t.Errorf("unnamed placeholder field: %+v", e)
	}
}

func TestOpenMenuItemsBecomeOptions(t *testing.T) {
	els := FilterInteractive(ParseAriaSnapshot(fixture(t, "open-menu.snapshot")))
	var opts []string
	for _, e := range els {
		if e.Role == "option" {
			opts = append(opts, e.Name)
		}
	}
	if len(opts) != 12 || opts[0] != "01" || opts[11] != "12" {
		t.Fatalf("options = %v, want 01..12", opts)
	}
	// The controls outside the menu are untouched.
	m := byRef(els)
	if m["f5e90"].Role != "button" || m["f5e70"].Role != "button" {
		t.Errorf("non-menu controls changed: %+v", els)
	}
}

func TestButtonNamedFromChildText(t *testing.T) {
	els := byRef(FilterInteractive(ParseAriaSnapshot(fixture(t, "modal-hidden-names.snapshot"))))
	if e, ok := els["f3e160"]; !ok || e.Name != "Submit order" {
		t.Fatalf("got %+v", els)
	}
}

func TestUnlabelledFieldIsKept(t *testing.T) {
	els := byRef(FilterInteractive(ParseAriaSnapshot("- textbox [ref=e3]\n- button [ref=e4]\n")))
	if e, ok := els["e3"]; !ok || e.Name != "(unlabelled textbox)" || !strings.Contains(e.Note, "no visible label") {
		t.Errorf("unlabelled field: %+v", els)
	}
	if _, ok := els["e4"]; ok {
		t.Error("an unnamed button must still be dropped")
	}
}

func TestControlBetweenStopsInference(t *testing.T) {
	els := byRef(FilterInteractive(ParseAriaSnapshot("- text: Email\n- button \"X\" [ref=e1]\n- textbox [ref=e2]\n")))
	if els["e2"].Name == "Email" {
		t.Fatalf("label leaked past a control: %+v", els["e2"])
	}
}
