package coder

import (
	"encoding/json"

	"github.com/rookery-ai/rookery/internal/agentstate"
)

// statetools.go gives the API engine's get_state/set_state tools a first-class door
// onto an agent's state.md, alongside the [STATE] marker and the CLI bridge. All three
// converge on agentstate.Apply — the single writer — so a tool call and a marker
// emission cannot merge state differently. This is the fix for the failure mode Tasks
// 1-3 made recoverable: an agent hand-editing state.md with write_file/edit_file and
// putting its JSON outside the fence, where the reader never looked.

// stateFilePath is the agent's own state.md. It is only ever asked for when
// includeExecTools is true, which is exactly the condition under which workDir is a
// real agent's own directory (a run or a build) rather than the vault root used for
// chat — so this never resolves to a path outside any agent's control.
func (h *hostToolSet) stateFilePath() string {
	return agentstate.StateFilePath(h.workDir)
}

// getState returns the agent's current understood state as JSON, capped like every
// other tool result. It deliberately ignores the "understood" flag agentstate.Get also
// returns: from the model's point of view there is no useful difference between "empty
// because nothing has been stored yet" and "empty because the file was unreadable and
// recovery found nothing" — both mean start fresh.
func (h *hostToolSet) getState() (string, error) {
	st, _, err := agentstate.Get(h.stateFilePath())
	if err != nil {
		return "", err
	}
	body, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return "", err
	}
	// truncate() appends a non-JSON "…[truncated: …]" note on cut, so an oversized
	// state never comes back looking like a complete, parseable JSON value — the
	// worst outcome available here, per the state-tools brief. It cannot happen in
	// practice today (agentstate.Apply enforces MaxStateSize on every write, well
	// under maxToolResult), but get_state does not assume that invariant holds.
	return truncate(string(body)), nil
}

// setState merges patch into state.md via agentstate.Apply, the single writer shared
// with the [STATE] marker and the CLI bridge. A key absent from patch is left
// untouched; a key mapped to JSON null is deleted — the same semantic the [STATE]
// marker has always had, restated in the tool description so the model calling it
// knows this is a patch, not a wholesale replacement.
func (h *hostToolSet) setState(patch map[string]any) (string, error) {
	if _, err := agentstate.Apply(h.stateFilePath(), h.agentName, patch); err != nil {
		return "", err
	}
	return "ok: state updated", nil
}
