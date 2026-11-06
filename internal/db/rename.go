package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ErrNameTaken reports a rename onto a name another agent or skill already holds.
var ErrNameTaken = errors.New("name already taken")

// DraftEdit is the rewritten body of a workspace's agent draft. A draft is a copy of
// AGENT.md and its tools awaiting save, so a rename that left it alone would have the
// old name written back when the draft is saved. nil leaves the draft as it is.
type DraftEdit struct {
	AgentMD   string
	ToolsJSON string
}

func renameExec(tx *sql.Tx, query string, args ...any) (int64, error) {
	res, err := tx.Exec(query, args...)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return 0, ErrNameTaken
		}
		return 0, err
	}
	return res.RowsAffected()
}

func applyDraftEdit(tx *sql.Tx, workspaceID string, edit *DraftEdit) error {
	if edit == nil {
		return nil
	}
	_, err := tx.Exec(`UPDATE agent_drafts SET pending_agent_md=?, pending_tools_json=? WHERE workspace_id=?`,
		edit.AgentMD, edit.ToolsJSON, workspaceID)
	return err
}

// RenameAgent renames an agent and every row carrying a copy of its name, in one
// transaction. The inbox and pending-action labels are scoped by agent_id, so another
// agent that once held this name keeps its own history.
func (d *DB) RenameAgent(agentID, newName string, draft *DraftEdit) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var workspaceID string
	if err := tx.QueryRow(`SELECT workspace_id FROM agents WHERE id=?`, agentID).Scan(&workspaceID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if _, err := renameExec(tx, `UPDATE agents SET name=?, updated_at=datetime('now') WHERE id=?`, newName, agentID); err != nil {
		return err
	}
	for _, q := range []string{
		`UPDATE inbox_messages SET agent_name=? WHERE agent_id=?`,
		`UPDATE pending_actions SET agent_name=? WHERE agent_id=?`,
		// A create draft's agent_id is freshly minted and never matches; only an edit
		// draft points at a live agent.
		`UPDATE agent_drafts SET agent_name=? WHERE agent_id=?`,
	} {
		if _, err := tx.Exec(q, newName, agentID); err != nil {
			return fmt.Errorf("rename agent labels: %w", err)
		}
	}
	if err := applyDraftEdit(tx, workspaceID, draft); err != nil {
		return err
	}
	return tx.Commit()
}

// RenameSkill renames a skill and its attachments in one transaction. agent_skills is
// keyed by name with no foreign key, so an agent may already hold a dangling row with
// the new name: OR IGNORE leaves that row in place and the DELETE clears the old one
// the IGNORE skipped, which is the same end state.
func (d *DB) RenameSkill(workspaceID, skillID, oldName, newName, description string, draft *DraftEdit) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	n, err := renameExec(tx, `UPDATE skills SET name=?, description=? WHERE id=? AND workspace_id=?`,
		newName, description, skillID, workspaceID)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	const scope = ` AND agent_id IN (SELECT id FROM agents WHERE workspace_id=?)`
	if _, err := tx.Exec(`UPDATE OR IGNORE agent_skills SET skill_name=? WHERE skill_name=?`+scope, newName, oldName, workspaceID); err != nil {
		return fmt.Errorf("rename skill attachments: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM agent_skills WHERE skill_name=?`+scope, oldName, workspaceID); err != nil {
		return fmt.Errorf("rename skill attachments: %w", err)
	}
	if err := applyDraftEdit(tx, workspaceID, draft); err != nil {
		return err
	}
	return tx.Commit()
}
