package agentstate

import (
	"os"
	"strings"
	"testing"
)

// A damaged file and a fresh agent must not look alike. `understood` decides
// whether the runner's no-update turn may write state back; if a file whose
// fence was mangled reports understood=true, that turn replaces
// hand-recoverable bytes with {}. An orphaned opener is damage by
// construction, so failing to recover anything from one is NOT "fresh".
func TestOrphanFenceWithGarbageIsNotUnderstood(t *testing.T) {
	p := write(t, "```json\n{not json\n")
	_, understood, err := Get(p)
	if err != nil {
		t.Fatal(err)
	}
	if understood {
		t.Fatal("an unrecoverable orphaned fence must report understood=false")
	}
}

// Recovery moves a stranded object into the fence. What it must NOT do is
// quietly eat the prose around it. This is the reviewer's case: a JSON payload
// quoted mid-sentence, where excising the object leaves the sentence dangling.
func TestRecoveryKeepsTheProseAroundAStrandedObject(t *testing.T) {
	p := write(t, "```json\n{}\n```\n\n## Notes\n\nLast error from the API: {\"error\":\"rate limited\"}\n")
	if _, err := Apply(p, "a", nil); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	got := string(raw)
	if !strings.Contains(got, "## Notes") {
		t.Errorf("Notes heading lost:\n%s", got)
	}
	if !strings.Contains(got, "Last error from the API") {
		t.Errorf("prose around the recovered object lost:\n%s", got)
	}
	st, _, _ := Get(p)
	if st["error"] == nil {
		t.Errorf("stranded object not adopted: %#v", st)
	}
}

// The reviewer's other case: a legitimate json fence under ## Notes with an
// empty state fence above it. The example is adopted — the documented residual
// risk of the scan — but the Notes section must survive, and the state fence
// must still come FIRST, or the next read finds the wrong one.
func TestRecoveryLeavesNotesIntactAndKeepsTheStateFenceFirst(t *testing.T) {
	p := write(t, "```json\n{}\n```\n\n## Notes\n\n```json\n{\"id\": 49355606}\n```\n")
	if _, err := Apply(p, "a", nil); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	got := string(raw)
	if !strings.Contains(got, "## Notes") {
		t.Errorf("Notes section lost:\n%s", got)
	}
	if !strings.Contains(got, "# State \u2014 a") {
		t.Errorf("file not normalised:\n%s", got)
	}
	if strings.Index(got, "```json") > strings.Index(got, "## Notes") {
		t.Errorf("state fence is no longer first:\n%s", got)
	}
}

// fenceByteRange charges every line a trailing newline, which the final line
// does not have when the file ends without one. That overrun made the prose cut
// a no-op, leaving the old empty fence behind in the output alongside the new
// one.
// The fence must be the LAST thing in the file for this to bite: only then does
// fenceByteRange's per-line "+1 for the newline" run past the end. An earlier
// draft of this test put the stranded object last, so the overrun never
// happened and the test passed against the unfixed code.
func TestRecoveryWorksWithoutATrailingNewline(t *testing.T) {
	p := write(t, "{\"seen\": 4}\n```json\n{}\n```")
	if _, err := Apply(p, "a", nil); err != nil {
		t.Fatal(err)
	}
	st, _, _ := Get(p)
	if st["seen"] == nil {
		t.Fatalf("state lost with no trailing newline: %#v", st)
	}
	raw, _ := os.ReadFile(p)
	if n := strings.Count(string(raw), "```json"); n != 1 {
		t.Errorf("expected exactly one json fence, got %d:\n%s", n, raw)
	}
}
