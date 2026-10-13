package browser

import (
	"strings"
	"testing"
)

var countries = []selectOption{
	{Value: "", Label: "Your country *"},
	{Value: "NL", Label: "Netherlands"},
	{Value: "PT", Label: "Portugal"},
	{Value: "PR", Label: "Puerto Rico"},
	{Value: "US", Label: "United States of America (the)"},
	{Value: "UM", Label: "United States Minor Outlying Islands (the)"},
}

// The case the toolset could not do at all: a model told "country Portugal"
// passes the visible text, and that must choose the option.
func TestMatchOptionByLabelAndValue(t *testing.T) {
	cases := map[string]string{
		"Portugal":    "PT", // exact label
		"PT":          "PT", // exact value
		"portugal":    "PT", // label, case-insensitive
		"  Portugal ": "PT", // surrounding whitespace
		"netherl":     "NL", // unique substring of a label
	}
	for want, value := range cases {
		got, err := matchOption(countries, want)
		if err != nil || got != value {
			t.Errorf("matchOption(%q) = %q, %v; want %q", want, got, err, value)
		}
	}
}

// A substring that matches two options must NOT pick one: choosing the wrong
// country on a checkout form and carrying on is worse than an error the model
// can correct from.
func TestMatchOptionRefusesAmbiguityAndListsOptions(t *testing.T) {
	_, err := matchOption(countries, "United States")
	if err == nil {
		t.Fatal("an ambiguous substring chose an option")
	}
	if !strings.Contains(err.Error(), "United States of America (the)") ||
		!strings.Contains(err.Error(), "United States Minor Outlying Islands (the)") {
		t.Errorf("ambiguity error does not name the candidates: %v", err)
	}

	_, err = matchOption(countries, "Atlantis")
	if err == nil || !strings.Contains(err.Error(), "Portugal") {
		t.Errorf("a miss must list the available options so the model can retry: %v", err)
	}
}

// An exact match wins over a substring one: "Portugal" must not be reported as
// ambiguous just because another label happens to contain it.
func TestMatchOptionExactBeatsSubstring(t *testing.T) {
	opts := []selectOption{{Value: "a", Label: "Portugal"}, {Value: "b", Label: "Portugal (Azores)"}}
	got, err := matchOption(opts, "Portugal")
	if err != nil || got != "a" {
		t.Fatalf("exact label lost to a substring match: %q, %v", got, err)
	}
}

// A long dropdown must not blow the 8 KiB tool-result budget in its error.
func TestMatchOptionMissListIsBounded(t *testing.T) {
	var many []selectOption
	for i := 0; i < 300; i++ {
		many = append(many, selectOption{Value: strings.Repeat("v", 3), Label: strings.Repeat("Country ", 3)})
	}
	_, err := matchOption(many, "nope")
	if err == nil || len(err.Error()) > 1500 {
		t.Fatalf("miss error unbounded (%d bytes)", len(err.Error()))
	}
}
