package agentstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// state.md is read on the runner's per-turn hot path, and an agent may grow it
// without limit through its `## Notes` section. MaxStateSize bounds the state
// BODY being written; nothing bounded the FILE being read, so the read was an
// unbounded os.ReadFile over a file the agent itself controls.
//
// The cap REJECTS rather than truncates, matching internal/iolimit. Truncating
// would be actively worse than failing: a cut-off state.md loses its closing
// fence, so it would parse as a DAMAGED file and land in the recovery scan —
// converting a size problem into a data-loss problem, silently.
func TestOversizedStateFileIsRejectedNotTruncated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.md")

	// Real state, then prose past the cap.
	body := "# State — big\n\n```json\n{\"cursor\":\"abc\"}\n```\n\n## Notes\n" +
		strings.Repeat("x", MaxStateFileSize+1)
	if err := os.WriteFile(path, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}

	st, understood, err := Get(path)
	if err == nil {
		t.Fatal("an over-cap state.md must be an error, not silently truncated state")
	}
	if understood {
		t.Error("an unreadable file must never report understood=true: the runner's " +
			"stateReadOK guard is what stops it overwriting hand-recoverable content")
	}
	if len(st) != 0 {
		t.Errorf("no state may be returned from a file that could not be read: %#v", st)
	}
	if !strings.Contains(err.Error(), "state.md") {
		t.Errorf("the error must name the file so the owner can find it; got %v", err)
	}
}

// The cap must not be so tight that ordinary use trips it. MaxStateFileSize is
// deliberately far above MaxStateSize because the document legitimately holds
// prose the state cap never counts.
func TestAGenerouslyLargeButLegitimateStateFileStillReads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.md")

	notes := strings.Repeat("some genuinely useful note prose. ", 2000) // ~68 KB
	body := "# State — big\n\n```json\n{\"cursor\":\"abc\"}\n```\n\n## Notes\n" + notes
	if len(body) <= MaxStateSize {
		t.Fatalf("fixture must exceed MaxStateSize to be meaningful; got %d", len(body))
	}
	if err := os.WriteFile(path, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}

	st, understood, err := Get(path)
	if err != nil {
		t.Fatalf("a large but legitimate state.md must still read: %v", err)
	}
	if !understood || st["cursor"] != "abc" {
		t.Fatalf("state lost from a legitimate large file: understood=%v state=%#v", understood, st)
	}
}

// The read cap and the write cap govern different things and must not be
// collapsed into one another: the file holds prose the state body never does.
func TestReadCapIsLargerThanTheWriteCap(t *testing.T) {
	if MaxStateFileSize <= MaxStateSize {
		t.Fatalf("MaxStateFileSize (%d) must exceed MaxStateSize (%d): the document "+
			"carries `## Notes` prose that the state body cap never counts, so equal "+
			"caps would reject files whose STATE is comfortably within limits",
			MaxStateFileSize, MaxStateSize)
	}
}
