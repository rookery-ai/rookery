package agentstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "state.md")
	if err := os.WriteFile(p, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
	return p
}

const canonical = "# State — a\n\n*Managed by Rookery. The block below is this agent's memory between runs — edit it if you need to fix something by hand.*\n\n" +
	"```json\n{\n  \"seen\": 1\n}\n```\n"

// A canonical file must survive a no-op Apply byte-for-byte. This is the
// regression that would mean working agents had been broken.
func TestCanonicalFileIsByteIdenticalAfterNoOpApply(t *testing.T) {
	p := write(t, canonical)
	if _, err := Apply(p, "a", nil); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != canonical {
		t.Fatalf("file changed:\n--- want ---\n%s\n--- got ---\n%s", canonical, got)
	}
}

// The canonical fixture above cannot detect an unconditional re-render: it is
// itself RenderTemplate's output, so re-rendering it reproduces it byte for
// byte. This fixture is a file that is perfectly readable but NOT in template
// layout — a preamble the user wrote where the intro would be. Only a splice
// preserves it; a re-render moves the preamble below the fence and inserts the
// managed-by intro. Production files look like this, so this is the test that
// actually pins "leave everything outside the fence alone".
const customised = "# State — a\n\nPreamble the user wrote themselves.\n\n" +
	"```json\n{\n  \"seen\": 1\n}\n```\n\n## Notes\n\nHand-written.\n"

func TestUnusualButValidLayoutIsSplicedNotRewritten(t *testing.T) {
	p := write(t, customised)
	if _, err := Apply(p, "a", nil); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != customised {
		t.Fatalf("file changed:\n--- want ---\n%s\n--- got ---\n%s", customised, got)
	}
}

// The hn-watch shape: a VALID but EMPTY fence with the agent's real memory
// stranded one line below it. ReadState returned {} here, which is why the
// agent re-baselined and went silent on every run forever.
func TestRecoversStateStrandedBelowAnEmptyFence(t *testing.T) {
	p := write(t, "```json\n{}\n```\n{\"reported_ids\": [49355606, 49358259]}\n\n## Notes\n\nFirst run.\n")

	st, understood, err := Get(p)
	if err != nil {
		t.Fatal(err)
	}
	if !understood {
		t.Fatal("a recoverable file must report understood=true")
	}
	ids, ok := st["reported_ids"].([]any)
	if !ok || len(ids) != 2 {
		t.Fatalf("stranded state not recovered: %#v", st)
	}
}

// Recovery is not enough on its own — the file must end up canonical so the
// next run reads it the normal way.
func TestRecoveredFileIsNormalisedOnApply(t *testing.T) {
	p := write(t, "```json\n{}\n```\n{\"seen\": 7}\n")
	if _, err := Apply(p, "a", nil); err != nil {
		t.Fatal(err)
	}
	st, _, err := Get(p)
	if err != nil {
		t.Fatal(err)
	}
	if st["seen"] == nil {
		t.Fatalf("state lost by normalisation: %#v", st)
	}
	raw, _ := os.ReadFile(p)
	if got := string(raw); !contains(got, "# State — a") {
		t.Fatalf("file not normalised:\n%s", got)
	}
}

// The property the orphan-splice comment in the old WriteState was protecting,
// restated for the recovery path: after normalising a damaged file, the fence
// we just wrote must still be the FIRST fence, or the next read returns the
// Notes example instead of the agent's memory.
func TestNormalisedFenceStaysFirstWhenNotesCarryTheirOwnFence(t *testing.T) {
	p := write(t, "```json\n{\"real\": 1}\n\n## Notes\n\n```json\n{\"example\": 2}\n```\n")
	if _, err := Apply(p, "a", nil); err != nil {
		t.Fatal(err)
	}
	st, understood, err := Get(p)
	if err != nil || !understood {
		t.Fatalf("understood=%v err=%v", understood, err)
	}
	if st["real"] == nil || st["example"] != nil {
		t.Fatalf("wrong fence is first after normalisation: %#v\n%s", st, mustRead(t, p))
	}
	if !contains(mustRead(t, p), "## Notes") {
		t.Fatalf("notes prose lost:\n%s", mustRead(t, p))
	}
}

// A legitimate fence inside ## Notes must never be mistaken for state when the
// state fence itself is populated.
func TestPopulatedFenceWinsOverALaterFence(t *testing.T) {
	p := write(t, "```json\n{\"real\": 1}\n```\n\n## Notes\n\n```json\n{\"example\": 2}\n```\n")
	st, _, err := Get(p)
	if err != nil {
		t.Fatal(err)
	}
	if st["real"] == nil || st["example"] != nil {
		t.Fatalf("wrong fence used: %#v", st)
	}
}

// nil deletes; a patch merges rather than replaces.
func TestPatchMergesAndNilDeletes(t *testing.T) {
	p := write(t, canonical)
	if _, err := Apply(p, "a", map[string]any{"other": 2}); err != nil {
		t.Fatal(err)
	}
	st, _, _ := Get(p)
	if st["seen"] == nil || st["other"] == nil {
		t.Fatalf("patch replaced instead of merging: %#v", st)
	}
	if _, err := Apply(p, "a", map[string]any{"seen": nil}); err != nil {
		t.Fatal(err)
	}
	st, _, _ = Get(p)
	if _, still := st["seen"]; still {
		t.Fatalf("nil did not delete: %#v", st)
	}
}

// A file we genuinely cannot understand must report understood=false so the
// caller's no-update turn stays a no-op rather than overwriting it with {}.
func TestUnparseableFenceReportsNotUnderstood(t *testing.T) {
	p := write(t, "```json\n{not json\n```\n")
	_, understood, _ := Get(p)
	if understood {
		t.Fatal("a broken fence must report understood=false")
	}
}

// ...and a no-op Apply over such a file must leave it exactly as it was, so a
// human can still recover it by hand.
func TestUnparseableFenceSurvivesANoOpApply(t *testing.T) {
	const broken = "```json\n{not json\n```\n"
	p := write(t, broken)
	if _, err := Apply(p, "a", nil); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, p); got != broken {
		t.Fatalf("broken file was overwritten:\n%s", got)
	}
}

// A missing file is empty memory, not an error.
func TestMissingFileIsEmptyAndUnderstood(t *testing.T) {
	st, understood, err := Get(filepath.Join(t.TempDir(), "none.md"))
	if err != nil || !understood || len(st) != 0 {
		t.Fatalf("got %#v understood=%v err=%v", st, understood, err)
	}
}

// Integer fidelity above 2^53 — a 64-bit Discord snowflake is the single most
// common thing an agent stores, and plain json.Unmarshal rounds it.
func TestLargeIntegersRoundTripExactly(t *testing.T) {
	p := write(t, "# State — a\n\n```json\n{\n  \"last\": 1401234567890123456\n}\n```\n")
	if _, err := Apply(p, "a", map[string]any{"other": "x"}); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, p); !contains(got, "1401234567890123456") {
		t.Fatalf("snowflake rounded:\n%s", got)
	}
}

// Replace is a whole-state write: it discards what was there rather than
// merging, and it leaves prose outside the fence alone.
func TestReplaceDiscardsRatherThanMerges(t *testing.T) {
	p := write(t, canonical)
	if _, err := Replace(p, "a", map[string]any{"other": 2}); err != nil {
		t.Fatal(err)
	}
	st, _, _ := Get(p)
	if _, still := st["seen"]; still {
		t.Fatalf("Replace merged instead of replacing: %#v", st)
	}
	if st["other"] == nil {
		t.Fatalf("Replace lost its own state: %#v", st)
	}
}

func TestReplaceCreatesAMissingFileFromTheTemplate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.md")
	if _, err := Replace(p, "a", map[string]any{"seen": 1}); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, p); !contains(got, "# State — a") || !contains(got, "\"seen\": 1") {
		t.Fatalf("template not rendered:\n%s", got)
	}
}

func TestApplyRefusesOversizedState(t *testing.T) {
	p := write(t, canonical)
	if _, err := Apply(p, "a", map[string]any{"blob": strings.Repeat("x", MaxStateSize+1)}); err == nil {
		t.Fatal("oversized state was accepted")
	}
}

func TestStateFilePath(t *testing.T) {
	if got := StateFilePath("/vault/agents/x"); got != filepath.Join("/vault/agents/x", "state.md") {
		t.Fatalf("got %q", got)
	}
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func contains(hay, needle string) bool {
	return len(hay) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(hay); i++ {
			if hay[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
