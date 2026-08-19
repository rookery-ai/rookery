package agentdesigner

import (
	"fmt"
	"path/filepath"

	"github.com/rookery-ai/rookery/internal/agentstate"
)

// The state.md format itself lives in internal/agentstate, which is the single
// choke point every write path converges on — the [STATE] marker, the API
// engine's set_state tool, and the CLI coder's loopback bridge. The functions
// below are the names this package has always exported and are kept so every
// existing call site (the runner, the startup migration, the designer, the KB
// guard) is untouched by that move.
//
// Two of four agents on a live install wrote their memory OUTSIDE the json
// fence, so the reader saw {} and they re-baselined on every run — permanently
// silent, at roughly 930k tokens an hour. Recovering from that is why the
// format needed one owner rather than one reader and several writers.

// StateFilePath returns the path to an agent's state.md — its memory between
// runs, kept as a markdown document so it is readable in the knowledge base.
func StateFilePath(vaultsBase, workspaceID, agentID string) string {
	return filepath.Join(AgentDir(vaultsBase, workspaceID, agentID), "state.md")
}

// RenderStateTemplate builds a fresh state.md.
//
// The template's own constraints (why the intro is italic prose rather than an
// HTML comment, and why it must stay on one physical line) live with the
// implementation in agentstate.RenderTemplate — they are properties of the
// format, not of this package.
func RenderStateTemplate(agentName, jsonBody string) string {
	return agentstate.RenderTemplate(agentName, jsonBody)
}

// ReadState returns the state object held in state.md.
//
// agentstate.Get reports understanding as a bool; this signature has always
// reported it as an error, and that difference is load-bearing rather than
// cosmetic. The runner derives `stateReadOK := err == nil` from this call and
// uses it to decide whether a no-update turn may write state back. Returning
// (empty, nil) for a file we could not parse would flip that flag to "fine" and
// let the very next turn replace hand-recoverable bytes with {} — the failure
// the guard exists to prevent.
//
// So a file that could not be understood is an error here, exactly as before.
// Callers wanting the distinction without the error call agentstate.Get.
func ReadState(path string) (map[string]any, error) {
	st, understood, err := agentstate.Get(path)
	if err != nil {
		return st, err
	}
	if !understood {
		return st, fmt.Errorf("state.md json block: could not be parsed")
	}
	return st, nil
}

// WriteState sets state.md's machine state to exactly `state`, preserving any
// prose around the fence.
//
// Whole-state write, NOT a patch: every caller of this function has already
// done its own merging and passes the full map it intends the file to hold.
// agentstate.Apply is the patch-merging counterpart, and conflating the two
// would silently turn a deletion into a no-op.
func WriteState(path, agentName string, state map[string]any) error {
	_, err := agentstate.Replace(path, agentName, state)
	return err
}
