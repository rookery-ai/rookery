package agentdesigner

import (
	"reflect"
	"testing"

	"github.com/rookery-ai/rookery/internal/prompts"
)

// installed pool used across all cases: two core skills (lowercase) + one
// multi-word user skill + one hyphenated user skill. Case-insensitive matching
// and name-with-space preservation are both exercised.
var testInstalled = []prompts.SkillRef{
	{Name: "csv", Description: "csv"},
	{Name: "pdf", Description: "pdf"},
	{Name: "google-workspace", Description: "gw"},
	{Name: "Google Workspace", Description: "gw full"}, // multi-word canonical name
	{Name: "markdown", Description: "md"},
}

func TestParseSkillsLine_Variations(t *testing.T) {
	cases := []struct {
		name string
		md   string
		want []string
	}{
		{
			name: "exact canonical",
			md:   "# Suggested schedule: none\n# Skills: csv, pdf\nYou are an agent.\n",
			want: []string{"csv", "pdf"},
		},
		{
			name: "lowercase heading level 2",
			md:   "## skills: csv, pdf\nbody",
			want: []string{"csv", "pdf"},
		},
		{
			name: "and delimiter",
			md:   "# Skills: csv and pdf",
			want: []string{"csv", "pdf"},
		},
		{
			name: "or delimiter",
			md:   "# Skills: csv or pdf",
			want: []string{"csv", "pdf"},
		},
		{
			name: "semicolon delimiter",
			md:   "# Skills: csv; pdf",
			want: []string{"csv", "pdf"},
		},
		{
			name: "pipe delimiter",
			md:   "# Skills: csv | pdf",
			want: []string{"csv", "pdf"},
		},
		{
			name: "backticks around names",
			md:   "# Skills: `csv`, `pdf`",
			want: []string{"csv", "pdf"},
		},
		{
			name: "quotes around names",
			md:   "# Skills: \"csv\", 'pdf'",
			want: []string{"csv", "pdf"},
		},
		{
			name: "explicit none",
			md:   "# Skills: none\nbody",
			want: []string{},
		},
		{
			name: "bullet list form",
			md:   "# Skills:\n- csv\n- pdf\n\nNext paragraph.",
			want: []string{"csv", "pdf"},
		},
		{
			name: "asterisk bullet list form",
			md:   "## Skills\n* csv\n* pdf\n\nbody",
			want: []string{"csv", "pdf"},
		},
		{
			name: "numbered list form",
			md:   "# Skills\n1. csv\n2. pdf\nbody",
			want: []string{"csv", "pdf"},
		},
		{
			name: "case mismatch upper",
			md:   "# Skills: CSV, PDF",
			want: []string{"csv", "pdf"},
		},
		{
			name: "mixed case",
			md:   "# Skills: Csv, pDf",
			want: []string{"csv", "pdf"},
		},
		{
			name: "trailing parenthetical prose",
			md:   "# Skills: csv, pdf (for processing documents)",
			want: []string{"csv", "pdf"},
		},
		{
			name: "trailing em-dash prose",
			md:   "# Skills: csv — used for parsing",
			want: []string{"csv"},
		},
		{
			name: "line after a blank line (not at top)",
			md:   "# Suggested schedule: */10 * * * *\n\n# Skills: csv, pdf\nbody",
			want: []string{"csv", "pdf"},
		},
		{
			name: "required skills qualifier",
			md:   "# Required skills: csv, pdf",
			want: []string{"csv", "pdf"},
		},
		{
			name: "needed skills qualifier",
			md:   "## Needed skills: csv",
			want: []string{"csv"},
		},
		{
			name: "uses skills qualifier",
			md:   "# Uses skills: csv, pdf",
			want: []string{"csv", "pdf"},
		},
		{
			name: "singular skill heading",
			md:   "# Skill: csv",
			want: []string{"csv"},
		},
		{
			name: "dash separator instead of colon",
			md:   "# Skills - csv, pdf",
			want: []string{"csv", "pdf"},
		},
		{
			name: "equals separator",
			md:   "# Skills = csv, pdf",
			want: []string{"csv", "pdf"},
		},
		{
			name: "multi-word name preserved (no bare-space split)",
			md:   "# Skills: csv, Google Workspace",
			want: []string{"csv", "Google Workspace"},
		},
		{
			name: "hyphenated name",
			md:   "# Skills: csv, google-workspace",
			want: []string{"csv", "google-workspace"},
		},
		{
			name: "hallucinated names dropped",
			md:   "# Skills: csv, nonexistent-skill, pdf",
			want: []string{"csv", "pdf"},
		},
		{
			name: "plus delimiter",
			md:   "# Skills: csv + pdf",
			want: []string{"csv", "pdf"},
		},
		{
			name: "duplicate names deduped",
			md:   "# Skills: csv, csv, pdf",
			want: []string{"csv", "pdf"},
		},
		{
			name: "trailing period on names stripped",
			md:   "# Skills: csv., pdf.",
			want: []string{"csv", "pdf"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseSkillsLine(tc.md, testInstalled)
			if tc.want == nil {
				tc.want = []string{}
			}
			if got == nil {
				got = []string{}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseSkillsLine(%q) = %v, want %v", tc.md, got, tc.want)
			}
		})
	}
}

// TestParseSkillsLine_NoHeaderReturnsNil asserts the contract that nil means
// "no skills header found at all" — distinct from "header found but empty".
// Callers rely on this to distinguish "coder forgot the line" from "coder said none".
func TestParseSkillsLine_NoHeaderReturnsNil(t *testing.T) {
	md := "# Suggested schedule: none\nYou are a test agent with no skills line.\n"
	got := parseSkillsLine(md, testInstalled)
	if got != nil {
		t.Errorf("parseSkillsLine = %v, want nil when no skills header present", got)
	}
}

// TestParseSkillsLine_EmptyInstalledReturnsNil guards against attaching when the
// user has no skills pool to validate against.
func TestParseSkillsLine_EmptyInstalledReturnsNil(t *testing.T) {
	md := "# Skills: csv, pdf\n"
	got := parseSkillsLine(md, nil)
	if got != nil {
		t.Errorf("parseSkillsLine = %v, want nil when installed pool is empty", got)
	}
}

// TestParseSkillsLine_HeaderFoundEmptyNonNil verifies the contract: a header that
// says "none" returns a non-nil empty slice (header was present), not nil.
func TestParseSkillsLine_HeaderFoundEmptyNonNil(t *testing.T) {
	got := parseSkillsLine("# Skills: none", testInstalled)
	if got == nil {
		t.Fatalf("parseSkillsLine = nil, want non-nil empty slice for explicit none")
	}
	if len(got) != 0 {
		t.Errorf("parseSkillsLine = %v, want empty slice", got)
	}
}
