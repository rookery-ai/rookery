package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// MCP server status values. The two failure states are deliberately distinct: see
// MCPServer.Status and internal/mcp's classification of a refresh failure.
const (
	MCPStatusActive      = "ACTIVE"
	MCPStatusNeedsAuth   = "NEEDS_AUTH"
	MCPStatusUnreachable = "UNREACHABLE"
)

const mcpServerCols = `id,workspace_id,name,slug,url,transport,auth_kind,header_name,
	encrypted_token,enabled,status,last_error,tools_synced_at,tools_ttl_ms,server_info,
	created_at,updated_at`

func scanMCPServer(sc interface {
	Scan(dest ...any) error
}) (*MCPServer, error) {
	var s MCPServer
	var enabled int
	var createdAt, updatedAt string
	if err := sc.Scan(&s.ID, &s.WorkspaceID, &s.Name, &s.Slug, &s.URL, &s.Transport,
		&s.AuthKind, &s.HeaderName, &s.EncryptedToken, &enabled, &s.Status, &s.LastError,
		&s.ToolsSyncedAt, &s.ToolsTTLMs, &s.ServerInfo, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	s.Enabled = enabled == 1
	s.CreatedAt = scanTime(createdAt)
	s.UpdatedAt = scanTime(updatedAt)
	return &s, nil
}

// ListMCPServers returns every MCP server in the workspace, enabled or not.
func (d *DB) ListMCPServers(workspaceID string) ([]*MCPServer, error) {
	rows, err := d.Query(`SELECT `+mcpServerCols+` FROM mcp_servers WHERE workspace_id=? ORDER BY name`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	servers := []*MCPServer{}
	for rows.Next() {
		s, err := scanMCPServer(rows)
		if err != nil {
			return nil, err
		}
		servers = append(servers, s)
	}
	return servers, rows.Err()
}

// GetMCPServer returns one server by id, scoped to the workspace so a handler cannot
// reach another tenant's row by guessing an id.
func (d *DB) GetMCPServer(ctx context.Context, workspaceID, id string) (*MCPServer, error) {
	row := d.QueryRowContext(ctx, `SELECT `+mcpServerCols+` FROM mcp_servers WHERE id=? AND workspace_id=?`, id, workspaceID)
	s, err := scanMCPServer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return s, err
}

// CreateMCPServer inserts a server row. The caller supplies an already-unique slug.
func (d *DB) CreateMCPServer(ctx context.Context, s *MCPServer) error {
	_, err := d.ExecContext(ctx, `INSERT INTO mcp_servers
		(id,workspace_id,name,slug,url,transport,auth_kind,header_name,encrypted_token,enabled,status)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		s.ID, s.WorkspaceID, s.Name, s.Slug, s.URL, s.Transport, s.AuthKind,
		s.HeaderName, s.EncryptedToken, boolToInt(s.Enabled), s.Status)
	return err
}

// UpdateMCPServer saves the owner-editable fields. The token is only overwritten when
// a non-empty one is supplied, so an edit that does not retype the credential keeps it.
func (d *DB) UpdateMCPServer(ctx context.Context, s *MCPServer) error {
	if s.EncryptedToken != "" {
		_, err := d.ExecContext(ctx, `UPDATE mcp_servers SET name=?,url=?,auth_kind=?,
			header_name=?,encrypted_token=?,enabled=?,updated_at=datetime('now')
			WHERE id=? AND workspace_id=?`,
			s.Name, s.URL, s.AuthKind, s.HeaderName, s.EncryptedToken,
			boolToInt(s.Enabled), s.ID, s.WorkspaceID)
		return err
	}
	_, err := d.ExecContext(ctx, `UPDATE mcp_servers SET name=?,url=?,auth_kind=?,
		header_name=?,enabled=?,updated_at=datetime('now')
		WHERE id=? AND workspace_id=?`,
		s.Name, s.URL, s.AuthKind, s.HeaderName, boolToInt(s.Enabled), s.ID, s.WorkspaceID)
	return err
}

func (d *DB) DeleteMCPServer(ctx context.Context, workspaceID, id string) error {
	_, err := d.ExecContext(ctx, `DELETE FROM mcp_servers WHERE id=? AND workspace_id=?`, id, workspaceID)
	return err
}

// SetMCPServerStatus records the outcome of a connection attempt.
func (d *DB) SetMCPServerStatus(ctx context.Context, id, status, lastErr string) error {
	_, err := d.ExecContext(ctx, `UPDATE mcp_servers SET status=?,last_error=?,updated_at=datetime('now') WHERE id=?`,
		status, lastErr, id)
	return err
}

// SetMCPServerSync records a successful catalog sync.
func (d *DB) SetMCPServerSync(ctx context.Context, id string, ttlMs int, serverInfo string) error {
	_, err := d.ExecContext(ctx, `UPDATE mcp_servers SET tools_synced_at=datetime('now'),
		tools_ttl_ms=?,server_info=?,status=?,last_error='',updated_at=datetime('now') WHERE id=?`,
		ttlMs, serverInfo, MCPStatusActive, id)
	return err
}

// MCPSlugTaken reports whether a slug is already used in the workspace, optionally
// ignoring one server id (so renaming a server does not collide with itself).
func (d *DB) MCPSlugTaken(ctx context.Context, workspaceID, slug, exceptID string) (bool, error) {
	var n int
	err := d.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM mcp_servers WHERE workspace_id=? AND slug=? AND id<>?`,
		workspaceID, slug, exceptID).Scan(&n)
	return n > 0, err
}

// ── Tools ─────────────────────────────────────────────────────────────────────

const mcpToolCols = `id,server_id,name,tool_name,title,description,input_schema,
	read_only,approval_mode,enabled,missing,created_at,updated_at`

func scanMCPTool(sc interface {
	Scan(dest ...any) error
}) (*MCPTool, error) {
	var t MCPTool
	var readOnly, enabled, missing int
	var createdAt, updatedAt string
	if err := sc.Scan(&t.ID, &t.ServerID, &t.Name, &t.ToolName, &t.Title, &t.Description,
		&t.InputSchema, &readOnly, &t.ApprovalMode, &enabled, &missing, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	t.ReadOnly = readOnly == 1
	t.Enabled = enabled == 1
	t.Missing = missing == 1
	t.CreatedAt = scanTime(createdAt)
	t.UpdatedAt = scanTime(updatedAt)
	return &t, nil
}

// ListMCPTools returns every cached tool for a server, including missing ones (the UI
// shows those so the owner can see what disappeared rather than being confused by a
// silently shorter list).
func (d *DB) ListMCPTools(ctx context.Context, serverID string) ([]*MCPTool, error) {
	rows, err := d.QueryContext(ctx, `SELECT `+mcpToolCols+` FROM mcp_tools WHERE server_id=? ORDER BY name`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tools := []*MCPTool{}
	for rows.Next() {
		t, err := scanMCPTool(rows)
		if err != nil {
			return nil, err
		}
		tools = append(tools, t)
	}
	return tools, rows.Err()
}

// ListEnabledMCPTools returns the tools actually offered to a model: enabled and not
// missing. A missing tool is never offered — calling it would fail at the server.
func (d *DB) ListEnabledMCPTools(ctx context.Context, serverID string) ([]*MCPTool, error) {
	rows, err := d.QueryContext(ctx, `SELECT `+mcpToolCols+`
		FROM mcp_tools WHERE server_id=? AND enabled=1 AND missing=0 ORDER BY name`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tools := []*MCPTool{}
	for rows.Next() {
		t, err := scanMCPTool(rows)
		if err != nil {
			return nil, err
		}
		tools = append(tools, t)
	}
	return tools, rows.Err()
}

// GetMCPTool loads one tool by id, verifying it belongs to the given server.
func (d *DB) GetMCPTool(ctx context.Context, serverID, id string) (*MCPTool, error) {
	row := d.QueryRowContext(ctx, `SELECT `+mcpToolCols+` FROM mcp_tools WHERE id=? AND server_id=?`, id, serverID)
	t, err := scanMCPTool(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

// UpsertMCPTool writes a discovered tool, PRESERVING the three owner-authored columns
// (read_only, approval_mode, enabled) on an existing row.
//
// This is the whole reason reconcile is an upsert keyed on (server_id, name) rather
// than a replace: a re-sync must not reset the owner's "this tool needs approval" to
// auto as a side effect of the server restating its catalog. It also clears `missing`,
// since a tool present in this response is by definition back.
func (d *DB) UpsertMCPTool(ctx context.Context, t *MCPTool) error {
	_, err := d.ExecContext(ctx, `INSERT INTO mcp_tools
		(id,server_id,name,tool_name,title,description,input_schema,read_only,approval_mode,enabled,missing)
		VALUES (?,?,?,?,?,?,?,?,?,?,0)
		ON CONFLICT(server_id,name) DO UPDATE SET
			tool_name=excluded.tool_name,
			title=excluded.title,
			description=excluded.description,
			input_schema=excluded.input_schema,
			missing=0,
			updated_at=datetime('now')`,
		t.ID, t.ServerID, t.Name, t.ToolName, t.Title, t.Description, t.InputSchema,
		boolToInt(t.ReadOnly), t.ApprovalMode, boolToInt(t.Enabled))
	return err
}

// MarkMCPToolsMissing flags every tool of a server whose name is not in keep.
//
// Marking rather than deleting is what lets the owner's overrides survive a server
// restart that briefly serves a shorter catalog.
func (d *DB) MarkMCPToolsMissing(ctx context.Context, serverID string, keep []string) error {
	if len(keep) == 0 {
		_, err := d.ExecContext(ctx, `UPDATE mcp_tools SET missing=1,updated_at=datetime('now') WHERE server_id=?`, serverID)
		return err
	}
	args := []any{serverID}
	ph := make([]string, 0, len(keep))
	for _, n := range keep {
		ph = append(ph, "?")
		args = append(args, n)
	}
	_, err := d.ExecContext(ctx, `UPDATE mcp_tools SET missing=1,updated_at=datetime('now')
		WHERE server_id=? AND name NOT IN (`+strings.Join(ph, ",")+`)`, args...)
	return err
}

// UpdateMCPToolSettings saves the owner's three ticks for one tool.
func (d *DB) UpdateMCPToolSettings(ctx context.Context, serverID, id string, enabled, readOnly bool, approvalMode string) error {
	if approvalMode != ApprovalModeApprove {
		approvalMode = ApprovalModeAuto
	}
	_, err := d.ExecContext(ctx, `UPDATE mcp_tools SET enabled=?,read_only=?,approval_mode=?,
		updated_at=datetime('now') WHERE id=? AND server_id=?`,
		boolToInt(enabled), boolToInt(readOnly), approvalMode, id, serverID)
	return err
}

// CountEnabledMCPTools counts the tools a server currently offers, used to enforce the
// per-server cap at sync time.
func (d *DB) CountEnabledMCPTools(ctx context.Context, serverID string) (int, error) {
	var n int
	err := d.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM mcp_tools WHERE server_id=? AND enabled=1 AND missing=0`, serverID).Scan(&n)
	return n, err
}

// ── Agent binding ─────────────────────────────────────────────────────────────

// SetAgentMCPServers replaces an agent's bound MCP servers (replace-all), preserving
// each surviving binding's approval_mode for exactly the reason SetAgentConnections
// does: the designer's auto-bind and the agent page's card both call this on every
// save, and a naive delete-then-insert would quietly turn a deliberate approval
// requirement back to auto.
func (d *DB) SetAgentMCPServers(ctx context.Context, agentID string, serverIDs []string) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	modes := map[string]string{}
	rows, err := tx.QueryContext(ctx, `SELECT server_id, approval_mode FROM agent_mcp_servers WHERE agent_id=?`, agentID)
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

	if _, err := tx.ExecContext(ctx, `DELETE FROM agent_mcp_servers WHERE agent_id=?`, agentID); err != nil {
		return err
	}
	for _, id := range serverIDs {
		if strings.TrimSpace(id) == "" {
			continue
		}
		mode := modes[id]
		if mode == "" {
			mode = ApprovalModeAuto
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO agent_mcp_servers (agent_id, server_id, approval_mode) VALUES (?,?,?)`,
			agentID, id, mode); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListAgentMCPServers returns the enabled MCP servers an agent is bound to.
//
// Disabled servers are filtered here rather than at the call site: a run must not be
// handed tools for a server the owner has switched off.
func (d *DB) ListAgentMCPServers(ctx context.Context, agentID string) ([]*MCPServer, error) {
	rows, err := d.QueryContext(ctx, `SELECT `+prefixCols("ms", mcpServerCols)+`
		FROM agent_mcp_servers ams JOIN mcp_servers ms ON ms.id = ams.server_id
		WHERE ams.agent_id=? AND ms.enabled=1 ORDER BY ms.name`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	servers := []*MCPServer{}
	for rows.Next() {
		s, err := scanMCPServer(rows)
		if err != nil {
			return nil, err
		}
		servers = append(servers, s)
	}
	return servers, rows.Err()
}

// ListAgentMCPServerIDs returns just the bound ids, for the agent page's checkbox card.
func (d *DB) ListAgentMCPServerIDs(ctx context.Context, agentID string) ([]string, error) {
	rows, err := d.QueryContext(ctx, `SELECT server_id FROM agent_mcp_servers WHERE agent_id=?`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// MCPServerApprovalMode returns the binding's approval mode, defaulting to auto when
// the agent has no binding for that server.
func (d *DB) MCPServerApprovalMode(ctx context.Context, agentID, serverID string) (string, error) {
	var mode string
	err := d.QueryRowContext(ctx,
		`SELECT approval_mode FROM agent_mcp_servers WHERE agent_id=? AND server_id=?`,
		agentID, serverID).Scan(&mode)
	if errors.Is(err, sql.ErrNoRows) {
		return ApprovalModeAuto, nil
	}
	return mode, err
}

// SetMCPServerApprovalMode toggles one binding's approval gate.
func (d *DB) SetMCPServerApprovalMode(ctx context.Context, agentID, serverID, mode string) error {
	if mode != ApprovalModeApprove {
		mode = ApprovalModeAuto
	}
	_, err := d.ExecContext(ctx,
		`UPDATE agent_mcp_servers SET approval_mode=? WHERE agent_id=? AND server_id=?`,
		mode, agentID, serverID)
	return err
}

// AgentHasGatedMCPServer reports whether the agent has ANY MCP binding on 'approve',
// so an ungated agent installs no Parker and pays nothing for the feature.
func (d *DB) AgentHasGatedMCPServer(ctx context.Context, agentID string) (bool, error) {
	var n int
	err := d.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM agent_mcp_servers WHERE agent_id=? AND approval_mode=?`,
		agentID, ApprovalModeApprove).Scan(&n)
	return n > 0, err
}
