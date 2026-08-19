package agentdesigner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStateRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.md")
	if err := WriteState(p, "Gmail Digest", map[string]any{"last_seen_id": "abc", "count": float64(4)}); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadState(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got["last_seen_id"] != "abc" || got["count"] != json.Number("4") {
		t.Fatalf("round-trip mismatch: %#v", got)
	}
	raw, _ := os.ReadFile(p)
	if !strings.Contains(string(raw), "# State — Gmail Digest") {
		t.Fatalf("template heading missing:\n%s", raw)
	}
}

// TestStateRoundTripLargeIntegerPreservesFidelity is the direct regression
// test for the reviewer-reported bug: a plain json.Unmarshal into
// map[string]any decodes every JSON number as float64, and float64 cannot
// represent integers above 2^53 exactly — 9007199254740993 (2^53+1) silently
// rounds to 9007199254740992. Discord snowflake IDs are 64-bit and routinely
// exceed 2^53, and "remember the last ID I saw" is the most common thing an
// agent stores in state, so this is not a theoretical edge case. ReadState
// must hand back the exact literal digits, not a rounded float.
func TestStateRoundTripLargeIntegerPreservesFidelity(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.md")
	const bigID = "9007199254740993" // 2^53 + 1 — the smallest int float64 cannot represent exactly
	if err := WriteState(p, "X", map[string]any{"last_id": json.Number(bigID)}); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadState(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	n, ok := got["last_id"].(json.Number)
	if !ok {
		t.Fatalf("last_id decoded as %T, not json.Number: %#v", got["last_id"], got["last_id"])
	}
	if n.String() != bigID {
		t.Fatalf("large integer lost precision: got %s, want %s", n.String(), bigID)
	}
	// Also assert against the literal bytes on disk, independent of ReadState's
	// own decode — this is what a human (or the KB editor) would see.
	raw, _ := os.ReadFile(p)
	if !strings.Contains(string(raw), bigID) {
		t.Fatalf("state.md does not contain the exact literal digits %s:\n%s", bigID, raw)
	}
}

// TestStateRoundTripFractionalFloatSurvives guards the other direction: a
// genuinely fractional value must keep round-tripping correctly after the
// UseNumber fix — it must not regress to strings or integers.
func TestStateRoundTripFractionalFloatSurvives(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.md")
	if err := WriteState(p, "X", map[string]any{"ratio": 1.5}); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadState(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	n, ok := got["ratio"].(json.Number)
	if !ok {
		t.Fatalf("ratio decoded as %T, not json.Number: %#v", got["ratio"], got["ratio"])
	}
	f, err := n.Float64()
	if err != nil {
		t.Fatalf("ratio is not a valid float: %v", err)
	}
	if f != 1.5 {
		t.Fatalf("fractional value did not round-trip: got %v, want 1.5", f)
	}
	if n.String() != "1.5" {
		t.Fatalf("fractional literal mangled: got %q, want %q", n.String(), "1.5")
	}
}

func TestWriteStatePreservesProseOutsideFence(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.md")
	seed := "# State — X\n\n_intro_\n\n```json\n{\"a\":1}\n```\n\n## Notes\n\nAgent wrote this.\n"
	if err := os.WriteFile(p, []byte(seed), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := WriteState(p, "X", map[string]any{"a": float64(2)}); err != nil {
		t.Fatalf("write: %v", err)
	}
	raw, _ := os.ReadFile(p)
	if !strings.Contains(string(raw), "## Notes") || !strings.Contains(string(raw), "Agent wrote this.") {
		t.Fatalf("prose lost:\n%s", raw)
	}
	if !strings.Contains(string(raw), "_intro_") {
		t.Fatalf("intro lost:\n%s", raw)
	}
	got, _ := ReadState(p)
	if got["a"] != json.Number("2") {
		t.Fatalf("state not updated: %#v", got)
	}
}

func TestReadStateMissingFileAndMissingFence(t *testing.T) {
	dir := t.TempDir()
	got, err := ReadState(filepath.Join(dir, "nope.md"))
	if err != nil || len(got) != 0 {
		t.Fatalf("missing file: %#v %v", got, err)
	}

	p := filepath.Join(dir, "state.md")
	os.WriteFile(p, []byte("# State — X\n\nno fence here\n"), 0o640)
	got, err = ReadState(p)
	if err != nil || len(got) != 0 {
		t.Fatalf("missing fence should self-heal to empty: %#v %v", got, err)
	}
	// Next write appends a fence and keeps the existing prose.
	if err := WriteState(p, "X", map[string]any{"k": "v"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	if !strings.Contains(string(raw), "no fence here") || !strings.Contains(string(raw), "```json") {
		t.Fatalf("append failed:\n%s", raw)
	}
}

func TestWriteStateDollarSignSafe(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.md")
	if err := WriteState(p, "X", map[string]any{"cmd": "$1 and $0 and ${x}"}); err != nil {
		t.Fatal(err)
	}
	got, _ := ReadState(p)
	if got["cmd"] != "$1 and $0 and ${x}" {
		t.Fatalf("dollar signs corrupted: %#v", got)
	}
}

func TestReadStateUnterminatedFenceRecoversItsContent(t *testing.T) {
	// This test used to assert the opposite — that a fence with no closing
	// backticks degrades to empty. That discard is now understood to BE the
	// bug: the JSON inside a damaged fence is the agent's own memory, written
	// by an agent that fumbled the format, and throwing it away is what made
	// two live agents re-baseline on every run and go permanently silent.
	//
	// The fence is still not "matched" — findStateFence still reports damage.
	// Recovery is what picks the content back up.
	p := filepath.Join(t.TempDir(), "state.md")
	seed := "# State — X\n\n```json\n{\"stale\": \"data\"}\n\nno close fence\n"
	if err := os.WriteFile(p, []byte(seed), 0o640); err != nil {
		t.Fatal(err)
	}
	got, err := ReadState(p)
	if err != nil {
		t.Fatalf("a recoverable file must not error: %v", err)
	}
	if got["stale"] != "data" {
		t.Fatalf("the agent's own memory was discarded: %#v", got)
	}
}

func TestWriteStateStripsOrphanedOpener(t *testing.T) {
	// An unterminated fence opener must be stripped when writing a new fence,
	// so a later ReadState returns only the new data, not stale+new confusion.
	p := filepath.Join(t.TempDir(), "state.md")
	seed := "# State — X\n\n_intro_\n\n```json\n{\"stale\": \"value\"}\n\n## Notes\n\nAgent prose.\n"
	if err := os.WriteFile(p, []byte(seed), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := WriteState(p, "X", map[string]any{"fresh": "data"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	raw, _ := os.ReadFile(p)
	// The orphaned opener should be gone.
	if strings.Count(string(raw), "```json") != 1 {
		t.Fatalf("orphaned ```json opener not stripped, found %d fences:\n%s",
			strings.Count(string(raw), "```json"), raw)
	}
	// Agent prose must be preserved.
	if !strings.Contains(string(raw), "Agent prose") {
		t.Fatalf("agent prose lost:\n%s", raw)
	}
	// New state must be readable.
	got, _ := ReadState(p)
	if got["fresh"] != "data" {
		t.Fatalf("new state not readable: %#v", got)
	}
}

func TestReadStateUnmarshalError(t *testing.T) {
	// Invalid JSON in a well-formed fence must return a wrapped error, not degrade to empty.
	p := filepath.Join(t.TempDir(), "state.md")
	seed := "# State — X\n\n```json\nnot valid json at all\n```\n"
	if err := os.WriteFile(p, []byte(seed), 0o640); err != nil {
		t.Fatal(err)
	}
	_, err := ReadState(p)
	if err == nil {
		t.Fatalf("invalid JSON should return an error, not degrade to empty")
	}
	if !strings.Contains(err.Error(), "state.md json block") {
		t.Fatalf("error should mention state.md json block: %v", err)
	}
}

func TestWriteStateWhitespaceOnlyFile(t *testing.T) {
	// A whitespace-only existing file should be treated as empty and replaced with a fresh template.
	p := filepath.Join(t.TempDir(), "state.md")
	if err := os.WriteFile(p, []byte("   \n\n  \t  \n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := WriteState(p, "X", map[string]any{"k": "v"}); err != nil {
		t.Fatalf("write to whitespace-only file: %v", err)
	}
	raw, _ := os.ReadFile(p)
	if !strings.Contains(string(raw), "# State — X") {
		t.Fatalf("template not created for whitespace-only file:\n%s", raw)
	}
}

func TestWriteStateDirectoryError(t *testing.T) {
	// Attempting to write to a path that is a directory must return an error.
	dir := t.TempDir()
	if err := WriteState(dir, "X", map[string]any{"k": "v"}); err == nil {
		t.Fatalf("writing to a directory should return an error, not silently fail")
	}
}

// TestWriteStateNotesDestructionCritical is the regression test for the
// Critical bug: an orphaned ```json opener near the top, followed later by a
// legitimate complete json fence inside an agent-written "## Notes" section.
// The old lazy regex spanned from the orphan opener all the way to the
// Notes fence's closing backticks, and WriteState's replace branch spliced
// out everything in between -- deleting the "## Notes" heading and the
// agent's prose. A correct implementation must only ever touch the ONE
// orphaned opener line, never anything past it.
func TestWriteStateNotesDestructionCritical(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.md")
	seed := "# State — X\n\n_intro_\n\n```json\n{\"stale\": \"orphan\"}\n\n" +
		"## Notes\n\nAgent wrote this.\n\n```json\n{\"notes_data\": 1}\n```\n"
	if err := os.WriteFile(p, []byte(seed), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := WriteState(p, "X", map[string]any{"fresh": "data"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	raw, _ := os.ReadFile(p)
	s := string(raw)
	if !strings.Contains(s, "## Notes") {
		t.Fatalf("Notes heading lost:\n%s", s)
	}
	if !strings.Contains(s, "Agent wrote this.") {
		t.Fatalf("Notes prose lost:\n%s", s)
	}
	if !strings.Contains(s, "```json\n{\"notes_data\": 1}\n```") {
		t.Fatalf("Notes fence lost or mangled:\n%s", s)
	}
	// Only the orphaned opener LINE is deleted (never lines past it, since a
	// damaged file's later content could be real prose) — so the stale
	// payload text may still appear as inert, unfenced prose. What must be
	// gone is the *state*: the stale key must be unreachable via ReadState.
	got, err := ReadState(p)
	if err != nil {
		t.Fatalf("read after write: %v", err)
	}
	if _, hasStale := got["stale"]; hasStale {
		t.Fatalf("stale orphan value still readable as state: %#v", got)
	}
	if got["fresh"] != "data" {
		t.Fatalf("new state not readable: %#v", got)
	}

	// A SECOND WriteState must not destroy the Notes fence either. If the
	// first write had appended the new fence at the end of the file (after
	// the Notes fence), the Notes fence would become the new "first" fence,
	// and this second write would splice over it — reintroducing the
	// Critical bug one write later. Writing the fence in place at the
	// (now-fresh) orphan/state position instead of at EOF prevents that.
	if err := WriteState(p, "X", map[string]any{"fresher": "data2"}); err != nil {
		t.Fatalf("second write: %v", err)
	}
	raw2, _ := os.ReadFile(p)
	s2 := string(raw2)
	if !strings.Contains(s2, "```json\n{\"notes_data\": 1}\n```") {
		t.Fatalf("Notes fence destroyed by a second WriteState:\n%s", s2)
	}
	got2, err := ReadState(p)
	if err != nil {
		t.Fatalf("read after second write: %v", err)
	}
	if got2["fresher"] != "data2" {
		t.Fatalf("second write's state not readable: %#v", got2)
	}
}

// TestReadStateOrphanThenLaterFence covers ReadState (not just WriteState) on
// the same damaged shape as the Critical case: an orphaned opener followed by
// a legitimate later fence. The state fence is by construction the FIRST
// fence; since the first one is malformed, ReadState must degrade to empty,
// never fall through to the Notes fence and never error.
func TestReadStateOrphanThenLaterFence(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.md")
	seed := "# State — X\n\n_intro_\n\n```json\n{\"stale\": \"orphan\"}\n\n" +
		"## Notes\n\nAgent wrote this.\n\n```json\n{\"notes_data\": 1}\n```\n"
	if err := os.WriteFile(p, []byte(seed), 0o640); err != nil {
		t.Fatal(err)
	}
	got, err := ReadState(p)
	if err != nil {
		t.Fatalf("a recoverable file must not error: %v", err)
	}
	// The guarantee this test exists for is UNCHANGED and is the important
	// half: the Notes fence is never the source of state. What changed is that
	// the orphaned state fence's own content is now recovered rather than
	// discarded — it is the agent's memory, and discarding it is the bug.
	if got["notes_data"] != nil {
		t.Fatalf("the Notes fence must never be read as state: %#v", got)
	}
	if got["stale"] != "orphan" {
		t.Fatalf("the orphaned state fence's own content was discarded: %#v", got)
	}
}

// TestWriteStatePreservesBlankLineOutsideFence guards against a splice that
// consumes surrounding whitespace: a line-index replace of [Open..Close]
// inclusive must leave everything before Open and after Close byte-identical,
// including a blank line right after the closing fence.
func TestWriteStatePreservesBlankLineOutsideFence(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.md")
	seed := "# State — X\n\n```json\n{\"a\":1}\n```\n\n## Notes\n\nprose\n"
	if err := os.WriteFile(p, []byte(seed), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := WriteState(p, "X", map[string]any{"a": float64(2)}); err != nil {
		t.Fatalf("write: %v", err)
	}
	raw, _ := os.ReadFile(p)
	if !strings.Contains(string(raw), "```\n\n## Notes") {
		t.Fatalf("blank line between fence and Notes heading lost:\n%s", raw)
	}
}

// TestWriteStateLeavesNotesFenceByteIdentical checks the happy path: a
// well-formed state fence plus a second fence in Notes. ReadState must
// return the FIRST fence's object, and WriteState must leave the Notes
// fence completely untouched.
func TestWriteStateLeavesNotesFenceByteIdentical(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.md")
	notesFence := "```json\n{\"notes\":2}\n```"
	seed := "# State — X\n\n```json\n{\"a\":1}\n```\n\n## Notes\n\nprose\n\n" + notesFence + "\n"
	if err := os.WriteFile(p, []byte(seed), 0o640); err != nil {
		t.Fatal(err)
	}
	got, err := ReadState(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got["a"] != json.Number("1") {
		t.Fatalf("expected first fence's object: %#v", got)
	}
	if err := WriteState(p, "X", map[string]any{"a": float64(9)}); err != nil {
		t.Fatalf("write: %v", err)
	}
	raw, _ := os.ReadFile(p)
	if !strings.Contains(string(raw), notesFence) {
		t.Fatalf("Notes fence not byte-identical after write:\n%s", raw)
	}
}

// TestReadStateTrailingTextOnCloserIsNotAClose documents CommonMark's rule:
// a closing code fence line may contain only whitespace after the backticks.
// "```trailing" is therefore a fence OPENER (a fence line carrying an info
// string), not a closer, so the block never terminates. Degrading to an
// empty map here is correct behavior, not a regression.
func TestReadStateTrailingTextOnCloserIsNotAClose(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.md")
	seed := "# State — X\n\n```json\n{\"a\":1}\n```trailing\n"
	if err := os.WriteFile(p, []byte(seed), 0o640); err != nil {
		t.Fatal(err)
	}
	got, err := ReadState(p)
	if err != nil {
		t.Fatalf("a recoverable file must not error: %v", err)
	}
	// "```trailing" is still an OPENER, not a closer — that classification is
	// what this test guards and it has not changed. The block therefore never
	// terminates, and the content inside it is now recovered rather than
	// discarded, for the same reason as the other damaged-fence cases.
	if got["a"] == nil {
		t.Fatalf("the agent's own memory was discarded: %#v", got)
	}
}
