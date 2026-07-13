package db

import (
	"context"
	"database/sql"
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

func (d *DB) InsertServiceConnection(ctx context.Context, c ServiceConnection) error {
	if c.Status == "" {
		c.Status = "ACTIVE"
	}
	_, err := d.ExecContext(ctx, `
INSERT INTO service_connections (`+svcConnCols+`)
VALUES (?,?,?,?,?,?,?,?,?,?,?,datetime('now'),datetime('now'))`,
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

// SetAgentConnections replaces an agent's bound connections with connIDs (replace-all).
func (d *DB) SetAgentConnections(ctx context.Context, agentID string, connIDs []string) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM agent_connections WHERE agent_id=?`, agentID); err != nil {
		return err
	}
	for _, id := range connIDs {
		if strings.TrimSpace(id) == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO agent_connections (agent_id, connection_id) VALUES (?, ?)`, agentID, id); err != nil {
			return err
		}
	}
	return tx.Commit()
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
