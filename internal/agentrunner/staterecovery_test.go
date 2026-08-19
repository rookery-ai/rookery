package agentrunner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rookery-ai/rookery/internal/agentdesigner"
)

// The hn-watch failure, pinned at the runner's OWN call site rather than at
// agentstate's.
//
// Two live agents wrote their memory just below the json fence instead of
// inside it. The reader saw an empty fence, returned {}, and they concluded on
// every single run that they had never run before — re-deriving a baseline,
// emitting [SILENT], and costing ~930k tokens an hour to say nothing. The run
// log said `warnings=0` throughout.
//
// This asserts the exact two-line composition runCoderAgent performs: read the
// file, and derive stateReadOK from whether that read errored. Testing
// agentstate.Get directly would prove the package works while leaving the
// wiring — which is where the bug actually lived — unexercised.
func TestRunnerRecoversStateStrandedOutsideTheFence(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.md")
	seed := "```json\n{}\n```\n{\"reported_ids\": [49355606, 49358259]}\n\n## Notes\n\nFirst run.\n"
	if err := os.WriteFile(p, []byte(seed), 0o640); err != nil {
		t.Fatal(err)
	}

	stateMap, err := agentdesigner.ReadState(p)
	stateReadOK := err == nil

	if !stateReadOK {
		t.Fatalf("a recoverable file must read cleanly, got: %v", err)
	}
	ids, ok := stateMap["reported_ids"].([]any)
	if !ok || len(ids) != 2 {
		t.Fatalf("the agent's memory was not recovered: %#v", stateMap)
	}
}

// The other half of the same guard, and the reason ReadState still reports an
// unparseable file as an error rather than as empty state.
//
// stateReadOK is what stops applyAndSaveState's no-update turn from writing
// back. If a file we could not parse ever read as OK, that turn would replace
// hand-recoverable bytes with {} — the failure the guard exists to prevent,
// reintroduced by the change meant to fix it.
func TestRunnerTreatsAnUnparseableStateFileAsNotOK(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.md")
	seed := "# State — X\n\n```json\nnot valid json at all\n```\n"
	if err := os.WriteFile(p, []byte(seed), 0o640); err != nil {
		t.Fatal(err)
	}

	_, err := agentdesigner.ReadState(p)
	if stateReadOK := err == nil; stateReadOK {
		t.Fatal("an unparseable state file must not read as OK")
	}
}
