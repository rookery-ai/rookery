package coder

import (
	"strings"
	"testing"
)

// A milestone is shown to a human watching their agent or their chat work.
// shortenHostPaths already handled the PATHS half of making that readable;
// identifiers were the gap. A workspace or agent UUID tells the reader nothing
// and crowds out the part of the line that says what the tool actually did.
func TestToolMilestoneMasksIdentifiers(t *testing.T) {
	got := toolMilestone(toolCall("read_file",
		`{"path":"agents/4892b6a2-aad8-4826-a6a4-66fcbcf19875/state.md"}`), "", "")
	if strings.Contains(got, "4892b6a2") {
		t.Errorf("raw identifier reached a user-visible milestone: %q", got)
	}
	if !strings.Contains(got, "state.md") {
		t.Errorf("masking ate the meaningful part: %q", got)
	}
}

// Masking must run BEFORE truncation, for exactly the reason the existing
// comment gives for shortening: a 36-character id would otherwise spend most of
// the 60-character budget and truncate away the tail naming what was touched.
func TestToolMilestoneMasksBeforeTruncating(t *testing.T) {
	got := toolMilestone(toolCall("read_file",
		`{"path":"agents/4892b6a2-aad8-4826-a6a4-66fcbcf19875/notes/summary.md"}`), "", "")
	if !strings.Contains(got, "summary.md") {
		t.Errorf("tail lost to truncation, so masking ran too late: %q", got)
	}
}

// Over-masking would be its own bug: a milestone exists to tell the reader what
// happened, and hex that is not id-shaped is usually content they want — a
// short git SHA, a hash in a filename.
func TestToolMilestoneLeavesNonIdentifiersAlone(t *testing.T) {
	got := toolMilestone(toolCall("bash", `{"command":"git show 39fadbe"}`), "", "")
	if !strings.Contains(got, "39fadbe") {
		t.Errorf("masked something that is not an identifier: %q", got)
	}
}

// The mask is applied to whatever detail the milestone renders, so it covers an
// id arriving through any of the extracted argument fields — not just paths.
func TestToolMilestoneMasksIdentifiersInAnyField(t *testing.T) {
	got := toolMilestone(toolCall("search_files",
		`{"query":"run 65ceb074-28e5-481a-8b51-a37d8d1c2adc"}`), "", "")
	if strings.Contains(got, "65ceb074") {
		t.Errorf("identifier leaked through the query field: %q", got)
	}
}
