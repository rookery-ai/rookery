package agentrunner

import (
	"encoding/json"
	"strings"
	"testing"
)

func decodeTranscript(t *testing.T, s string) []RunEvent {
	t.Helper()
	if s == "" {
		return nil
	}
	var out []RunEvent
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		t.Fatalf("transcript is not valid JSON: %v\n%s", err, s)
	}
	return out
}

// The reported gap: the agent page's run history could replay what the user had
// already read and nothing about how the agent got there. Tool calls and coder
// turns must land in ONE list, in the order they happened.
func TestTranscriptInterleavesToolCallsAndCoderTurns(t *testing.T) {
	tc := &transcriptCollector{}
	sink := tc.wrap(nil)

	sink("🔧 read_file(notes/a.md)")
	tc.add(EventCoder, "I read the note and nothing changed.")
	sink("🔧 web_search(weather)")
	tc.add(EventCoder, "[SILENT]")

	events := decodeTranscript(t, tc.encode())
	if len(events) != 4 {
		t.Fatalf("got %d events, want 4: %+v", len(events), events)
	}
	wantKinds := []string{EventProgress, EventCoder, EventProgress, EventCoder}
	for i, want := range wantKinds {
		if events[i].Kind != want {
			t.Errorf("event %d kind = %q, want %q", i, events[i].Kind, want)
		}
	}
	if events[0].Text != "🔧 read_file(notes/a.md)" {
		t.Errorf("first event = %q", events[0].Text)
	}
	if events[3].Text != "[SILENT]" {
		t.Errorf("last event = %q", events[3].Text)
	}
}

// wrap must forward as well as record, or turning on the transcript would
// silently take the live progress view away.
func TestTranscriptWrapStillForwardsToTheLiveSink(t *testing.T) {
	tc := &transcriptCollector{}
	var seen []string
	sink := tc.wrap(func(msg string) { seen = append(seen, msg) })

	sink("one")
	sink("two")

	if len(seen) != 2 || seen[0] != "one" || seen[1] != "two" {
		t.Errorf("live sink saw %v, want [one two]", seen)
	}
	if got := len(decodeTranscript(t, tc.encode())); got != 2 {
		t.Errorf("recorded %d events, want 2", got)
	}
}

// A scheduled run supplies no OnProgress at all — and that is exactly the run
// whose transcript matters most, because nobody watched it happen.
func TestTranscriptRecordsWhenThereIsNoLiveSink(t *testing.T) {
	tc := &transcriptCollector{}
	sink := tc.wrap(nil)
	sink("🔧 run_script(check.py)")

	events := decodeTranscript(t, tc.encode())
	if len(events) != 1 || events[0].Text != "🔧 run_script(check.py)" {
		t.Errorf("events = %+v, want the one milestone", events)
	}
}

// An empty transcript encodes as "" rather than "[]", so a run that recorded
// nothing is indistinguishable from one predating the column — in both cases
// there is nothing to show.
func TestTranscriptEmptyEncodesAsEmptyString(t *testing.T) {
	tc := &transcriptCollector{}
	if got := tc.encode(); got != "" {
		t.Errorf("encode() = %q, want empty", got)
	}
	// Blank milestones are not events.
	tc.wrap(nil)("")
	if got := tc.encode(); got != "" {
		t.Errorf("encode() after a blank line = %q, want empty", got)
	}
}

// Runs are kept indefinitely, so the transcript is capped. Over the budget the
// OLDEST events go and a marker takes their place — the end of a run is where
// it went wrong.
func TestTranscriptCapsSizeAndKeepsTheTail(t *testing.T) {
	tc := &transcriptCollector{}
	big := strings.Repeat("x", 512)
	for i := 0; i < 400; i++ {
		tc.add(EventProgress, big)
	}
	tc.add(EventCoder, "FINAL-ANSWER")

	encoded := tc.encode()
	if len(encoded) > maxTranscriptBytes {
		t.Errorf("encoded %d bytes, want <= %d", len(encoded), maxTranscriptBytes)
	}
	events := decodeTranscript(t, encoded)
	if len(events) == 0 {
		t.Fatal("expected some events")
	}
	if events[0].Kind != EventTruncated {
		t.Errorf("first event kind = %q, want %q", events[0].Kind, EventTruncated)
	}
	if last := events[len(events)-1]; last.Text != "FINAL-ANSWER" {
		t.Errorf("last event = %q, want the newest one kept", last.Text)
	}
	// Exactly one truncation notice, however many trims it took.
	markers := 0
	for _, e := range events {
		if e.Kind == EventTruncated {
			markers++
		}
	}
	if markers != 1 {
		t.Errorf("got %d truncation markers, want 1", markers)
	}
}

// One event larger than the whole budget must still yield something readable
// rather than an empty transcript.
func TestTranscriptClipsASingleOversizeEvent(t *testing.T) {
	tc := &transcriptCollector{}
	tc.add(EventCoder, strings.Repeat("y", maxTranscriptBytes*2))

	encoded := tc.encode()
	if encoded == "" {
		t.Fatal("an oversize event must not encode to nothing")
	}
	if len(encoded) > maxTranscriptBytes {
		t.Errorf("encoded %d bytes, want <= %d", len(encoded), maxTranscriptBytes)
	}
	events := decodeTranscript(t, encoded)
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if !strings.Contains(events[0].Text, "truncated") {
		t.Error("a clipped event must say it was clipped")
	}
}

// The vault run note takes the human-readable milestones, not the JSON — and
// only the milestones, since the coder's own words already have a section.
func TestTranscriptProgressLinesAreMilestonesOnly(t *testing.T) {
	tc := &transcriptCollector{}
	sink := tc.wrap(nil)
	sink("🔧 list_dir(.)")
	tc.add(EventCoder, "thinking out loud")
	sink("🔧 write_file(out.md)")

	got := tc.progressLines()
	want := []string{"🔧 list_dir(.)", "🔧 write_file(out.md)"}
	if len(got) != len(want) {
		t.Fatalf("progressLines() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}
