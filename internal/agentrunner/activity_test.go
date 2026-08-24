package agentrunner

import (
	"strings"
	"testing"
)

// The run panel no longer reprints the activity — it links to the run's
// knowledge-base note as "the full log". That makes this note the ONLY place
// the record lives, so it has to carry every kind of event.
//
// progressLines() returned tool calls alone, so had the note kept using it, a
// coder turn would have been visible in the panel before this change and
// nowhere at all after it: relocated into nothing.
func TestActivityLinesCarryEveryEventKindNotJustToolCalls(t *testing.T) {
	tc := &transcriptCollector{}
	tc.add(EventProgress, "🔧 read_file(inbox.md)")
	tc.add(EventCoder, "Three emails matched.")
	tc.add(EventSummary, "read_file×1")

	lines := tc.activityLines()
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3: %q", len(lines), lines)
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"read_file(inbox.md)", "Three emails matched.", "read_file×1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("activity dropped %q:\n%s", want, joined)
		}
	}

	// A progress line already carries its own marker; the other kinds need a
	// label or they are indistinguishable from each other in a flat list.
	if strings.HasPrefix(lines[0], "**") {
		t.Errorf("a tool call was labelled redundantly: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "**coder:**") {
		t.Errorf("a coder turn was not labelled: %q", lines[1])
	}
}

// progressLines still backs anything that genuinely wants tool calls only, and
// must not have been widened by the change above.
func TestProgressLinesStillReturnsOnlyToolCalls(t *testing.T) {
	tc := &transcriptCollector{}
	tc.add(EventProgress, "🔧 glob(*.md)")
	tc.add(EventCoder, "prose")

	got := tc.progressLines()
	if len(got) != 1 || !strings.Contains(got[0], "glob") {
		t.Errorf("progressLines returned %q, want just the tool call", got)
	}
}
