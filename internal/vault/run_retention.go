package vault

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PruneAgentRuns deletes one agent's run history beyond the newest keep runs:
// the agents/<id>/logs/run_*.md notes past the newest keep, and every agent_runs
// sidecar of this agent whose run is not in keepRunIDs. It returns how many files
// it removed.
//
// Notes are ordered by file NAME, which is RunNotePath's UTC timestamp, so lexical
// order is chronological. Nothing else in the agent's directory is touched —
// AGENT.md, state.md, tools/, and any other file under logs/ stay, because the
// platform deletes only the files it writes itself.
//
// Sidecars are matched by reading each one back, as UnreflectAgentRuns does: their
// path is keyed by run id and says nothing about the agent. One that cannot be read
// or parsed is skipped rather than deleted — an unreadable file is not evidence
// that it belongs to this agent.
//
// Best-effort, like reflection itself: a file already gone is not an error.
func (r *Reflector) PruneAgentRuns(workspaceID, agentID string, keep int, keepRunIDs []string) (int, error) {
	if r == nil || agentID == "" {
		return 0, nil
	}
	var removed int
	var firstErr error
	del := func(rel string) {
		if err := r.v.Delete(workspaceID, rel); err != nil {
			if !errors.Is(err, os.ErrNotExist) && firstErr == nil {
				firstErr = err
			}
			return
		}
		removed++
	}

	logsDir := filepath.Join("agents", safeName(agentID), "logs")
	if entries, err := r.v.List(workspaceID, logsDir); err == nil {
		var notes []string
		for _, e := range entries {
			if !e.IsDir && strings.HasPrefix(e.Name, "run_") && strings.HasSuffix(e.Name, ".md") {
				notes = append(notes, e.Path)
			}
		}
		sort.Sort(sort.Reverse(sort.StringSlice(notes)))
		if len(notes) > keep {
			for _, rel := range notes[keep:] {
				del(rel)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		firstErr = err
	}

	kept := make(map[string]bool, len(keepRunIDs))
	for _, id := range keepRunIDs {
		kept[id] = true
	}
	sidecarDir := filepath.Join(InternalDir, "db-export", "agent_runs")
	entries, err := r.v.List(workspaceID, sidecarDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return removed, firstErr
		}
		if firstErr == nil {
			firstErr = err
		}
		return removed, firstErr
	}
	for _, e := range entries {
		if e.IsDir || !strings.HasSuffix(e.Name, ".json") {
			continue
		}
		data, err := r.v.ReadNote(workspaceID, e.Path)
		if err != nil {
			continue
		}
		var sidecar struct {
			RunID   string `json:"RunID"`
			AgentID string `json:"AgentID"`
		}
		if err := json.Unmarshal(data, &sidecar); err != nil || sidecar.AgentID != agentID || sidecar.RunID == "" {
			continue
		}
		if !kept[sidecar.RunID] {
			del(e.Path)
		}
	}
	return removed, firstErr
}
