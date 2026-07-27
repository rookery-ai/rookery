package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ServiceProviderConfig holds a workspace's OAuth app credentials for one provider
// (client_id/secret, encrypted under the system key).
type ServiceProviderConfig struct {
	ID, WorkspaceID, Provider                string
	EncryptedClientID, EncryptedClientSecret string
	CreatedAt, UpdatedAt                     string
}

// ServiceConnection is one connected account (multi-account = multiple rows per
// workspace+provider). Token columns are encrypted under the system key.
type ServiceConnection struct {
	ID, WorkspaceID, Provider                   string
	AccountLabel, AccountIdentity, Scopes       string
	EncryptedAccessToken, EncryptedRefreshToken string
	ExpiresAt, Status                           string
	Extra                                       string // JSON of per-connection resolved values (e.g. Jira cloudid)
	CreatedAt, UpdatedAt                        string
}

func (d *DB) UpsertServiceProviderConfig(ctx context.Context, c ServiceProviderConfig) error {
	_, err := d.ExecContext(ctx, `
INSERT INTO service_provider_configs (id, workspace_id, provider, encrypted_client_id, encrypted_client_secret)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(workspace_id, provider) DO UPDATE SET
  encrypted_client_id=excluded.encrypted_client_id,
  encrypted_client_secret=excluded.encrypted_client_secret,
  updated_at=datetime('now')`,
		c.ID, c.WorkspaceID, c.Provider, c.EncryptedClientID, c.EncryptedClientSecret)
	return err
}

func (d *DB) GetServiceProviderConfig(ctx context.Context, workspaceID, provider string) (*ServiceProviderConfig, error) {
	var c ServiceProviderConfig
	err := d.QueryRowContext(ctx, `
SELECT id, workspace_id, provider, encrypted_client_id, encrypted_client_secret, created_at, updated_at
FROM service_provider_configs WHERE workspace_id=? AND provider=?`, workspaceID, provider).
		Scan(&c.ID, &c.WorkspaceID, &c.Provider, &c.EncryptedClientID, &c.EncryptedClientSecret, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

const svcConnCols = `id, workspace_id, provider, account_label, account_identity, scopes,
	encrypted_access_token, encrypted_refresh_token, expires_at, status, extra, created_at, updated_at`

type rowScanner interface{ Scan(...any) error }

func scanConn(s rowScanner) (ServiceConnection, error) {
	var c ServiceConnection
	err := s.Scan(&c.ID, &c.WorkspaceID, &c.Provider, &c.AccountLabel, &c.AccountIdentity, &c.Scopes,
		&c.EncryptedAccessToken, &c.EncryptedRefreshToken, &c.ExpiresAt, &c.Status, &c.Extra, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

// InsertServiceConnection inserts a new connected account, or — when a row
// already exists for the same (workspace_id, provider, account_label), which
// is UNIQUE — UPSERTs onto it instead of erroring. Reconnecting under the
// same label (OAuth re-consent or re-pasting an API key) is a normal flow,
// not a conflict: it must refresh the stored tokens/extra/status but MUST
// KEEP THE EXISTING id, since `agent_connections` bindings reference
// connections by id — replacing the row/id on reconnect would silently
// unbind every agent that had this connection attached. `c.ID` is therefore
// only used for the initial insert; a conflicting reconnect ignores it.
//
// Refresh-token guard: some reconnect flows (e.g. a provider that doesn't
// re-issue a refresh token on every consent) legitimately pass an empty
// EncryptedRefreshToken. Overwriting the existing one with empty would brick
// the connection's background token refresh the moment the current access
// token expires — so the UPSERT only replaces encrypted_refresh_token when
// the incoming value is non-empty; otherwise it keeps whatever was already
// stored.
func (d *DB) InsertServiceConnection(ctx context.Context, c ServiceConnection) error {
	if c.Status == "" {
		c.Status = "ACTIVE"
	}
	_, err := d.ExecContext(ctx, `
INSERT INTO service_connections (`+svcConnCols+`)
VALUES (?,?,?,?,?,?,?,?,?,?,?,datetime('now'),datetime('now'))
ON CONFLICT(workspace_id, provider, account_label) DO UPDATE SET
  account_identity=excluded.account_identity,
  scopes=excluded.scopes,
  encrypted_access_token=excluded.encrypted_access_token,
  encrypted_refresh_token=CASE WHEN excluded.encrypted_refresh_token != '' THEN excluded.encrypted_refresh_token ELSE encrypted_refresh_token END,
  expires_at=excluded.expires_at,
  status=excluded.status,
  extra=excluded.extra,
  updated_at=datetime('now')`,
		c.ID, c.WorkspaceID, c.Provider, c.AccountLabel, c.AccountIdentity, c.Scopes,
		c.EncryptedAccessToken, c.EncryptedRefreshToken, c.ExpiresAt, c.Status, c.Extra)
	return err
}

func (d *DB) GetServiceConnection(ctx context.Context, id string) (*ServiceConnection, error) {
	c, err := scanConn(d.QueryRowContext(ctx, `SELECT `+svcConnCols+` FROM service_connections WHERE id=?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (d *DB) ListServiceConnections(ctx context.Context, workspaceID string) ([]ServiceConnection, error) {
	rows, err := d.QueryContext(ctx, `SELECT `+svcConnCols+` FROM service_connections WHERE workspace_id=? ORDER BY provider, account_label`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServiceConnection
	for rows.Next() {
		c, err := scanConn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (d *DB) UpdateConnectionTokens(ctx context.Context, id, encAccess, expiresAt, status string) error {
	_, err := d.ExecContext(ctx, `
UPDATE service_connections SET encrypted_access_token=?, expires_at=?, status=?, updated_at=datetime('now') WHERE id=?`,
		encAccess, expiresAt, status, id)
	return err
}

// UpdateConnectionTokensFull also persists a rotated refresh token (providers like
// Atlassian issue a NEW refresh token on every refresh and invalidate the old one).
func (d *DB) UpdateConnectionTokensFull(ctx context.Context, id, encAccess, encRefresh, expiresAt, status string) error {
	_, err := d.ExecContext(ctx, `
UPDATE service_connections SET encrypted_access_token=?, encrypted_refresh_token=?, expires_at=?, status=?, updated_at=datetime('now') WHERE id=?`,
		encAccess, encRefresh, expiresAt, status, id)
	return err
}

func (d *DB) UpdateConnectionStatus(ctx context.Context, id, status string) error {
	_, err := d.ExecContext(ctx, `UPDATE service_connections SET status=?, updated_at=datetime('now') WHERE id=?`, status, id)
	return err
}

func (d *DB) DeleteServiceConnection(ctx context.Context, id string) error {
	_, err := d.ExecContext(ctx, `DELETE FROM service_connections WHERE id=?`, id)
	return err
}

// ConnectionsNearExpiry returns ACTIVE connections whose access token expires at or
// before cutoff and that carry a refresh token (so the background loop can renew them).
func (d *DB) ConnectionsNearExpiry(ctx context.Context, cutoff string) ([]ServiceConnection, error) {
	rows, err := d.QueryContext(ctx, `
SELECT `+svcConnCols+` FROM service_connections
WHERE status='ACTIVE' AND expires_at <> '' AND expires_at <= ? AND encrypted_refresh_token <> ''`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServiceConnection
	for rows.Next() {
		c, err := scanConn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ApprovalModeAuto executes an action immediately; ApprovalModeApprove parks a
// public_write action for the owner to approve. These are the only two values the
// approval_mode column may hold.
const (
	ApprovalModeAuto    = "auto"
	ApprovalModeApprove = "approve"
)

// SetAgentConnections replaces an agent's bound connections with connIDs (replace-all).
//
// It PRESERVES each surviving binding's approval_mode across the replace. The designer's
// auto-bind and the agent page's checkbox card both call this on every save, so a naive
// delete-then-insert would silently reset a user's "require approval before posting to
// the company LinkedIn" back to auto — turning a deliberate safety setting off as a side
// effect of an unrelated edit.
func (d *DB) SetAgentConnections(ctx context.Context, agentID string, connIDs []string) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	modes := map[string]string{}
	rows, err := tx.QueryContext(ctx, `SELECT connection_id, approval_mode FROM agent_connections WHERE agent_id=?`, agentID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, mode string
		if err := rows.Scan(&id, &mode); err != nil {
			rows.Close()
			return err
		}
		modes[id] = mode
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM agent_connections WHERE agent_id=?`, agentID); err != nil {
		return err
	}
	for _, id := range connIDs {
		if strings.TrimSpace(id) == "" {
			continue
		}
		mode := modes[id]
		if mode == "" {
			mode = ApprovalModeAuto
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO agent_connections (agent_id, connection_id, approval_mode) VALUES (?, ?, ?)`, agentID, id, mode); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetAgentConnectionApprovalMode sets one binding's gate. mode must be ApprovalModeAuto
// or ApprovalModeApprove; anything else is rejected rather than stored, so an unknown
// value can never sit in the column and be read as "not approve" at execution time.
func (d *DB) SetAgentConnectionApprovalMode(ctx context.Context, agentID, connID, mode string) error {
	if mode != ApprovalModeAuto && mode != ApprovalModeApprove {
		return fmt.Errorf("invalid approval mode %q", mode)
	}
	_, err := d.ExecContext(ctx,
		`UPDATE agent_connections SET approval_mode=? WHERE agent_id=? AND connection_id=?`,
		mode, agentID, connID)
	return err
}

// AgentConnectionApprovalMode returns the gate for one binding, defaulting to
// ApprovalModeAuto when the agent is not bound to that connection at all.
func (d *DB) AgentConnectionApprovalMode(ctx context.Context, agentID, connID string) (string, error) {
	var mode string
	err := d.QueryRowContext(ctx,
		`SELECT approval_mode FROM agent_connections WHERE agent_id=? AND connection_id=?`,
		agentID, connID).Scan(&mode)
	if errors.Is(err, sql.ErrNoRows) {
		return ApprovalModeAuto, nil
	}
	if err != nil {
		return ApprovalModeAuto, err
	}
	if mode != ApprovalModeApprove {
		return ApprovalModeAuto, nil
	}
	return mode, nil
}

// ListAgentConnections returns the service connections an agent is bound to.
func (d *DB) ListAgentConnections(ctx context.Context, agentID string) ([]ServiceConnection, error) {
	rows, err := d.QueryContext(ctx, `
SELECT `+prefixCols("sc", svcConnCols)+`
FROM agent_connections ac JOIN service_connections sc ON sc.id = ac.connection_id
WHERE ac.agent_id=? ORDER BY sc.provider, sc.account_label`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServiceConnection
	for rows.Next() {
		c, err := scanConn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// prefixCols qualifies a comma-separated column list with a table alias.
func prefixCols(alias, cols string) string {
	parts := strings.Split(cols, ",")
	for i, p := range parts {
		parts[i] = alias + "." + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}
