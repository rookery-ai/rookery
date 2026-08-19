package agentrunner

import (
	"path/filepath"
	"testing"

	"github.com/rookery-ai/rookery/internal/agentstate"
)

// A write made through set_state or `rookery state set` DURING a turn must
// survive that turn's end-of-turn save.
//
// applyAndSaveState used to write the run-start snapshot back wholesale, so any
// state the agent recorded through the new tools was discarded the moment the
// turn ended — re-creating, through the tools built to fix it, the exact
// "stored something, next run saw nothing" failure this change set exists to
// remove. It was invisible: the run succeeded, the file looked well-formed, and
// only the content was wrong.
func TestSetStateWritesSurviveTheEndOfTurnSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.md")

	// A prior run left this.
	if err := saveState(dir, "My Agent", map[string]interface{}{"cursor": "abc"}); err != nil {
		t.Fatal(err)
	}
	// The runner snapshots state at run start.
	runStart := map[string]interface{}{"cursor": "abc"}

	// Mid-turn, the agent calls set_state — which writes the FILE directly, not
	// the runner's in-memory map.
	if _, err := agentstate.Apply(path, "My Agent", map[string]interface{}{"seen": "xyz"}); err != nil {
		t.Fatal(err)
	}

	// The turn ends emitting no [STATE] at all.
	if err := applyAndSaveState(dir, "My Agent", runStart, nil, true); err != nil {
		t.Fatal(err)
	}

	got, _, err := agentstate.Get(path)
	if err != nil {
		t.Fatal(err)
	}
	if got["seen"] != "xyz" {
		t.Fatalf("the set_state write was discarded by the end-of-turn save: %#v", got)
	}
	if got["cursor"] != "abc" {
		t.Fatalf("pre-existing state lost: %#v", got)
	}
}

// The two doors compose: a set_state write and a [STATE] marker in the same
// turn must both land, rather than one silently winning.
func TestSetStateAndTheStateMarkerBothLandInOneTurn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.md")
	if err := saveState(dir, "My Agent", map[string]interface{}{}); err != nil {
		t.Fatal(err)
	}
	runStart := map[string]interface{}{}

	if _, err := agentstate.Apply(path, "My Agent", map[string]interface{}{"viaTool": 1}); err != nil {
		t.Fatal(err)
	}
	updates := []map[string]interface{}{{"viaMarker": 2}}
	if err := applyAndSaveState(dir, "My Agent", runStart, updates, true); err != nil {
		t.Fatal(err)
	}

	got, _, _ := agentstate.Get(path)
	if got["viaTool"] == nil {
		t.Errorf("the tool write was lost: %#v", got)
	}
	if got["viaMarker"] == nil {
		t.Errorf("the [STATE] marker was lost: %#v", got)
	}
}

// A [STATE] deletion must still delete, and combining several updates into one
// patch must not swallow it — a nil merged into an empty patch would erase the
// deletion rather than record it.
func TestStateMarkerDeletionStillDeletes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.md")
	if err := saveState(dir, "My Agent", map[string]interface{}{"gone": 1, "kept": 2}); err != nil {
		t.Fatal(err)
	}
	runStart := map[string]interface{}{"gone": 1, "kept": 2}

	updates := []map[string]interface{}{{"gone": nil}}
	if err := applyAndSaveState(dir, "My Agent", runStart, updates, true); err != nil {
		t.Fatal(err)
	}

	got, _, _ := agentstate.Get(path)
	if _, still := got["gone"]; still {
		t.Fatalf("null did not delete: %#v", got)
	}
	if got["kept"] == nil {
		t.Fatalf("a deletion removed an unrelated key: %#v", got)
	}
}
