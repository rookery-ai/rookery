package agentdesigner

import (
	"testing"

	"github.com/rookery-ai/rookery/internal/prompts"
	"github.com/stretchr/testify/require"
)

var testPool = []prompts.SkillRef{
	{Name: "pdf", Description: "Read PDFs."},
	{Name: "csv", Description: "Read CSVs."},
	{Name: "web-research", Description: "Research on the web."},
}

func TestParseSelectorResponse(t *testing.T) {
	cases := []struct {
		name string
		resp string
		want []string
	}{
		{"bare list", "pdf, csv", []string{"pdf", "csv"}},
		// Shapes the newline-aware splitter handles without any colon heuristic.
		{"prose preamble then list", "Based on the agent's instructions, it needs:\npdf, csv", []string{"pdf", "csv"}},
		{"prose line then lone name", "The agent processes documents.\npdf", []string{"pdf"}},

		// DELIBERATE LIMIT, not an oversight: a single-line answer that buries the name
		// behind a colon is not recovered. Recovering it required splitting the tail on
		// ":", which cannot distinguish an affirmative tail from a negated one — see the
		// negation cases below, which that strategy got actively wrong.
		{"colon-buried single line is not recovered", "This agent reads PDFs, so: pdf", []string{}},

		// The reason for that limit. Each of these once returned ["pdf"]: the model
		// refused the skill and the parser attached it anyway. Attaching a rejected skill
		// to a live agent is the exact failure this function's fail-closed contract exists
		// to prevent, so a missed affirmative is the cheaper error.
		{"negation with colon", "This agent explicitly does NOT use: pdf", []string{}},
		{"negation terse", "Definitely not: pdf", []string{}},
		{"negation avoid", "The agent should avoid using: pdf", []string{}},
		{"negation plain", "This agent does not need pdf", []string{}},
		{"bullet list", "- pdf\n- web-research\n", []string{"pdf", "web-research"}},
		{"backticked", "`pdf`, `csv`", []string{"pdf", "csv"}},
		{"none", "none", []string{}},
		{"hallucinated dropped", "pdf, quantum-flux", []string{"pdf"}},
		{"empty", "", []string{}},
		{"all hallucinated", "alpha, beta", []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseSelectorResponse(tc.resp, testPool)
			require.NotNil(t, got, "must never return nil")
			require.Equal(t, tc.want, got)
		})
	}
}

// A nil coder must not panic — it degrades to "attach nothing".
func TestSelectSkillsNilCoder(t *testing.T) {
	got := SelectSkills(t.Context(), nil, "ws", "# Agent\nreads pdfs\n", testPool)
	require.NotNil(t, got)
	require.Empty(t, got)
}

func TestSelectSkillsEmptyPool(t *testing.T) {
	got := SelectSkills(t.Context(), nil, "ws", "# Agent\n", nil)
	require.NotNil(t, got)
	require.Empty(t, got)
}

// TestSelectSkills_Success drives the real Chat/Generate loop end to end (via the
// package's existing fake-CLI-coder harness) rather than only unit-testing the parser,
// closing the gap the brief's three tests leave: the loop that calls the coder at all.
func TestSelectSkills_Success(t *testing.T) {
	fake := newFakeCoder(t, "print('pdf, csv')\n")
	got := SelectSkills(t.Context(), fake, "ws", "# Agent\nReads PDFs and CSVs.\n", testPool)
	require.Equal(t, []string{"pdf", "csv"}, got)
}

// TestSelectSkills_CoderErrorFailsClosed exercises the retry-then-fail-closed path: the
// coder call itself errors on every attempt, and SelectSkills must still return a
// non-nil empty slice (never propagate the error, never attach a guess).
func TestSelectSkills_CoderErrorFailsClosed(t *testing.T) {
	fake := newFakeCoder(t, "import sys\nsys.exit(1)\n")
	got := SelectSkills(t.Context(), fake, "ws", "# Agent\nReads PDFs.\n", testPool)
	require.NotNil(t, got)
	require.Empty(t, got)
}
