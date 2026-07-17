package skilllibrary

import "testing"

// TestIsCoreSkill_CaseInsensitive is the SP4 carry-over regression guard: a
// core-skill guard call site that receives an upper/mixed-case slug (e.g. a
// URL param typed by hand, or a designer-parsed name that wasn't lowercased)
// must still be recognized as a core skill.
func TestIsCoreSkill_CaseInsensitive(t *testing.T) {
	cases := []struct {
		slug string
		want bool
	}{
		{"pdf", true},
		{"PDF", true},
		{"Pdf", true},
		{"pDf", true},
		{"csv", true},
		{"CSV", true},
		{"not-a-real-skill", false},
		{"NOT-A-REAL-SKILL", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsCoreSkill(c.slug); got != c.want {
			t.Errorf("IsCoreSkill(%q) = %v, want %v", c.slug, got, c.want)
		}
	}
}
