package agentdesigner

import (
	"strings"
	"testing"
)

// TestGenerationPreviewFallback covers the tolerant-advance path: when the coder
// finishes a valid build but does NOT emit a clean [TEST_OUTPUT]…[/TEST_OUTPUT]
// block, generationPreviewFallback must still surface something reviewable (so the
// user reaches StateVerifying instead of an approve→rebuild loop) while stripping
// every agent-protocol / spec marker.
func TestGenerationPreviewFallback(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wantSubstr []string // must all appear
		wantAbsent []string // must none appear
	}{
		{
			name:       "prefers [CHAT] content",
			in:         "some reasoning\n[CHAT] Good morning! Here is your summary: 3 new items.\n[STATE]{\"seen\":3}[/STATE]",
			wantSubstr: []string{"Good morning", "3 new items"},
			wantAbsent: []string{"[CHAT]", "[STATE]", "seen"},
		},
		{
			name:       "strips technical spec and state when no chat",
			in:         "Here is what the agent will do.\n[TECHNICAL SPEC]\nTier: 1\n[/TECHNICAL SPEC]\n[STATE]{\"x\":1}[/STATE]",
			wantSubstr: []string{"what the agent will do"},
			wantAbsent: []string{"[TECHNICAL SPEC]", "Tier: 1", "[STATE]"},
		},
		{
			name:       "cuts chat at a following marker",
			in:         "[CHAT] Sent draft to you.\n[TEST_OUTPUT]raw dump[/TEST_OUTPUT]",
			wantSubstr: []string{"Sent draft to you"},
			wantAbsent: []string{"raw dump", "[TEST_OUTPUT]"},
		},
		{
			name:       "empty when only markers remain",
			in:         "[SILENT]",
			wantSubstr: nil,
			wantAbsent: []string{"[SILENT]"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := generationPreviewFallback(tc.in)
			for _, want := range tc.wantSubstr {
				if !strings.Contains(got, want) {
					t.Errorf("preview %q missing %q", got, want)
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("preview %q should not contain %q", got, absent)
				}
			}
			if tc.wantSubstr == nil && strings.TrimSpace(got) != "" {
				t.Errorf("expected empty preview, got %q", got)
			}
		})
	}
}
