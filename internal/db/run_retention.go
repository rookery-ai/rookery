package db

import "strings"

// AgentRef names one agent and its workspace, for passes that visit every agent.
type AgentRef struct {
	ID          string
	WorkspaceID string
}

// ListAllAgentRefs returns every agent on the install, across workspaces.
func (d *DB) ListAllAgentRefs() ([]AgentRef, error) {
	rows, err := d.Query(`SELECT id, workspace_id FROM agents ORDER BY workspace_id, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentRef
	for rows.Next() {
		var r AgentRef
		if err := rows.Scan(&r.ID, &r.WorkspaceID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PruneAgentRuns keeps the record of an agent's newest keep runs and slims every
// older finished run down to its numbers, returning the ids it kept (newest first).
//
// A slimmed row keeps its id, timestamps, status columns and usage, and loses
// transcript, stdout and stderr — where nearly all of a row's size is. It is NOT
// deleted, and that is the point: day and month spending limits sum agent_runs
// usage since the period start (AgentSpendSince), so deleting rows would let an
// agent that runs every few minutes forget what it spent this month.
//
// A run still in progress (finished_at IS NULL) is never touched, whatever its age.
// Ties on started_at — a stop followed by an immediate re-run share a second — are
// broken by rowid, so exactly keep runs survive.
func (d *DB) PruneAgentRuns(agentID string, keep int) ([]string, error) {
	tx, err := d.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit

	rows, err := tx.Query(`SELECT id FROM agent_runs WHERE agent_id=?
		ORDER BY started_at DESC, rowid DESC LIMIT ?`, agentID, keep)
	if err != nil {
		return nil, err
	}
	var kept []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		kept = append(kept, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(kept) < keep {
		return kept, tx.Commit()
	}

	args := []any{agentID}
	for _, id := range kept {
		args = append(args, id)
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(kept)), ",")
	if _, err := tx.Exec(`UPDATE agent_runs SET transcript='', stdout='', stderr=''
		WHERE agent_id=? AND finished_at IS NOT NULL AND id NOT IN (`+placeholders+`)
		AND (COALESCE(transcript,'')<>'' OR COALESCE(stdout,'')<>'' OR COALESCE(stderr,'')<>'')`,
		args...); err != nil {
		return nil, err
	}
	return kept, tx.Commit()
}
