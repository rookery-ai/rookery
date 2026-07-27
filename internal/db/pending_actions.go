package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Pending-action lifecycle. A row starts Pending and moves to exactly one terminal
// state; the transition is guarded by a conditional UPDATE so two approvals racing
// (chat and the web inbox, say) cannot both send the post.
const (
	PendingStatusPending  = "pending"
	PendingStatusApproved = "approved"
	PendingStatusRejected = "rejected"
	PendingStatusFailed   = "failed"
	PendingStatusExpired  = "expired"
)

// PendingAction is a connector call parked for the owner's approval.
type PendingAction struct {
	ID           string
	WorkspaceID  string
	AgentID      string // empty for chat-originated calls
	AgentName    string // denormalized; survives agent delete
	ConnectionID string
	Provider     string
	Action       string
	ArgsJSON     string
	Summary      string
	Status       string
	ResultJSON   string
	Error        string
	CreatedAt    time.Time
	ResolvedAt   *time.Time
}

// CreatePendingAction parks a call. The caller supplies the id so the queue ticket
// handed to the coder and the stored row cannot disagree.
func (d *DB) CreatePendingAction(ctx context.Context, p *PendingAction) error {
	var agentID any
	if p.AgentID != "" {
		agentID = p.AgentID
	}
	_, err := d.ExecContext(ctx, `INSERT INTO pending_actions
		(id,workspace_id,agent_id,agent_name,connection_id,provider,action,args_json,summary,status,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,datetime('now'))`,
		p.ID, p.WorkspaceID, agentID, p.AgentName, p.ConnectionID, p.Provider,
		p.Action, p.ArgsJSON, p.Summary, PendingStatusPending)
	return err
}

const pendingCols = `id,workspace_id,COALESCE(agent_id,''),agent_name,connection_id,provider,action,
	args_json,summary,status,result_json,error,created_at,resolved_at`

func scanPending(s interface{ Scan(...any) error }) (*PendingAction, error) {
	var p PendingAction
	var createdAt string
	var resolvedAt sql.NullString
	err := s.Scan(&p.ID, &p.WorkspaceID, &p.AgentID, &p.AgentName, &p.ConnectionID,
		&p.Provider, &p.Action, &p.ArgsJSON, &p.Summary, &p.Status,
		&p.ResultJSON, &p.Error, &createdAt, &resolvedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.CreatedAt = scanTime(createdAt)
	if resolvedAt.Valid && resolvedAt.String != "" {
		t := scanTime(resolvedAt.String)
		p.ResolvedAt = &t
	}
	return &p, nil
}

// GetPendingAction fetches one parked call, scoped to a workspace so an id from
// another tenant cannot be resolved.
func (d *DB) GetPendingAction(ctx context.Context, workspaceID, id string) (*PendingAction, error) {
	row := d.QueryRowContext(ctx,
		`SELECT `+pendingCols+` FROM pending_actions WHERE workspace_id=? AND id=?`, workspaceID, id)
	return scanPending(row)
}

// ListPendingActions returns a workspace's parked calls, newest first. status may be
// empty for all statuses.
func (d *DB) ListPendingActions(ctx context.Context, workspaceID, status string, limit int) ([]*PendingAction, error) {
	q := `SELECT ` + pendingCols + ` FROM pending_actions WHERE workspace_id=?`
	args := []any{workspaceID}
	if status != "" {
		q += ` AND status=?`
		args = append(args, status)
	}
	q += ` ORDER BY created_at DESC, rowid DESC LIMIT ?`
	if limit <= 0 {
		limit = 50
	}
	args = append(args, limit)

	rows, err := d.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*PendingAction
	for rows.Next() {
		p, err := scanPending(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ClaimPendingAction atomically moves a row out of `pending` into `next`, returning
// ErrNotFound when it was not pending any more.
//
// This is the concurrency guard for the whole gate: chat's /approve and the web
// inbox's button can fire at the same moment, and an unguarded read-then-update would
// let both proceed and publish the post twice. The status column IS the lock.
func (d *DB) ClaimPendingAction(ctx context.Context, workspaceID, id, next string) (*PendingAction, error) {
	switch next {
	case PendingStatusApproved, PendingStatusRejected, PendingStatusExpired:
	default:
		return nil, fmt.Errorf("cannot claim into status %q", next)
	}
	res, err := d.ExecContext(ctx,
		`UPDATE pending_actions SET status=?, resolved_at=datetime('now')
		 WHERE workspace_id=? AND id=? AND status=?`,
		next, workspaceID, id, PendingStatusPending)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, ErrNotFound
	}
	return d.GetPendingAction(ctx, workspaceID, id)
}

// FinishPendingAction records the outcome of an approved call's real send.
func (d *DB) FinishPendingAction(ctx context.Context, workspaceID, id, resultJSON, errMsg string) error {
	status := PendingStatusApproved
	if errMsg != "" {
		status = PendingStatusFailed
	}
	_, err := d.ExecContext(ctx,
		`UPDATE pending_actions SET status=?, result_json=?, error=?, resolved_at=datetime('now')
		 WHERE workspace_id=? AND id=?`,
		status, resultJSON, errMsg, workspaceID, id)
	return err
}

// ExpirePendingActionsOlderThan marks stale parked calls expired. A post approved a
// week after it was drafted is almost never what the user wants, and an unbounded
// queue turns into a list nobody reads.
func (d *DB) ExpirePendingActionsOlderThan(ctx context.Context, age time.Duration) (int64, error) {
	// `<=`, not `<`: created_at has one-second resolution, so a row parked in the same
	// second as the cutoff would otherwise never be caught by an age of zero and would
	// be off-by-one-second for every other age.
	cutoff := time.Now().UTC().Add(-age).Format("2006-01-02 15:04:05")
	res, err := d.ExecContext(ctx,
		`UPDATE pending_actions SET status=?, resolved_at=datetime('now')
		 WHERE status=? AND created_at <= ?`,
		PendingStatusExpired, PendingStatusPending, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
