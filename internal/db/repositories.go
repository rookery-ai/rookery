package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrNotFound = errors.New("not found")

// scanTime parses SQLite's datetime() string to time.Time.
func scanTime(s string) time.Time {
	t, _ := time.Parse("2006-01-02 15:04:05", s)
	return t
}

func scanTimePtr(s *string) *time.Time {
	if s == nil || *s == "" {
		return nil
	}
	t, _ := time.Parse("2006-01-02 15:04:05", *s)
	return &t
}

type scanner interface {
	Scan(dest ...any) error
}

// ── Owner ──────────────────────────────────────────────────────────────────

func (d *DB) CreateOwner(o *Owner) error {
	_, err := d.Exec(`INSERT INTO owner
		(id, username, password_hash, must_change_password, created_at, updated_at)
		VALUES (?,?,?,?,datetime('now'),datetime('now'))`,
		o.ID, o.Username, o.PasswordHash, boolToInt(o.MustChangePassword))
	return err
}

func (d *DB) GetOwner() (*Owner, error) {
	row := d.QueryRow(`SELECT id,username,password_hash,must_change_password,created_at,updated_at
		FROM owner LIMIT 1`)
	return scanOwner(row)
}

func (d *DB) GetOwnerByUsername(username string) (*Owner, error) {
	row := d.QueryRow(`SELECT id,username,password_hash,must_change_password,created_at,updated_at
		FROM owner WHERE username=?`, username)
	return scanOwner(row)
}

func (d *DB) UpdateOwnerPassword(id, hash string) error {
	_, err := d.Exec(`UPDATE owner SET password_hash=?, must_change_password=0, updated_at=datetime('now') WHERE id=?`, hash, id)
	return err
}

func (d *DB) OwnerExists() (bool, error) {
	var count int
	err := d.QueryRow(`SELECT COUNT(*) FROM owner`).Scan(&count)
	return count > 0, err
}

func scanOwner(s scanner) (*Owner, error) {
	var o Owner
	var createdAt, updatedAt string
	var mustChange int
	err := s.Scan(&o.ID, &o.Username, &o.PasswordHash, &mustChange, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	o.MustChangePassword = mustChange == 1
	o.CreatedAt = scanTime(createdAt)
	o.UpdatedAt = scanTime(updatedAt)
	return &o, nil
}

// ── Workspaces ───────────────────────────────────────────────────────────────

const workspaceCols = `id,name,about,icon,encrypted_master_password,secrets_salt,
	coder_kind,coder_bin,coder_timeout_s,coder_backend_type,
	coder_provider,coder_model,coder_api_key_secret,coder_base_url,
	needs_setup,created_at,updated_at`

func (d *DB) CreateWorkspace(w *Workspace) error {
	_, err := d.Exec(`INSERT INTO workspaces
		(id, name, about, encrypted_master_password, secrets_salt,
		 coder_kind, coder_bin, coder_timeout_s, coder_backend_type,
		 coder_provider, coder_model, coder_api_key_secret, coder_base_url,
		 needs_setup, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,datetime('now'),datetime('now'))`,
		w.ID, w.Name, w.About, w.EncryptedMasterPassword, w.SecretsSalt,
		coderKindOrDefault(w.CoderKind), w.CoderBin, w.CoderTimeoutS, w.CoderBackendType,
		w.CoderProvider, w.CoderModel, w.CoderAPIKeySecret, w.CoderBaseURL,
		boolToInt(w.NeedsSetup))
	return err
}

func (d *DB) GetWorkspaceByID(id string) (*Workspace, error) {
	row := d.QueryRow(`SELECT `+workspaceCols+` FROM workspaces WHERE id=?`, id)
	return scanWorkspace(row)
}

func (d *DB) GetWorkspaceByName(name string) (*Workspace, error) {
	row := d.QueryRow(`SELECT `+workspaceCols+` FROM workspaces WHERE name=?`, name)
	return scanWorkspace(row)
}

func (d *DB) ListWorkspaces() ([]*Workspace, error) {
	rows, err := d.Query(`SELECT ` + workspaceCols + ` FROM workspaces ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var workspaces []*Workspace
	for rows.Next() {
		w, err := scanWorkspace(rows)
		if err != nil {
			return nil, err
		}
		workspaces = append(workspaces, w)
	}
	return workspaces, rows.Err()
}

func (d *DB) UpdateWorkspaceMasterPassword(id, encMasterPw, salt string) error {
	_, err := d.Exec(`UPDATE workspaces SET encrypted_master_password=?, secrets_salt=?, updated_at=datetime('now') WHERE id=?`,
		encMasterPw, salt, id)
	return err
}

// UpdateWorkspaceMeta updates the workspace name and about fields.
func (d *DB) UpdateWorkspaceMeta(id, name, about string) error {
	_, err := d.Exec(`UPDATE workspaces SET name=?, about=?, updated_at=datetime('now') WHERE id=?`, name, about, id)
	return err
}

// UpdateWorkspaceIcon sets the workspace's icon slug. An empty slug is valid
// and means "no icon" — the UI falls back to the name's initial.
func (d *DB) UpdateWorkspaceIcon(id, icon string) error {
	_, err := d.Exec(`UPDATE workspaces SET icon=?, updated_at=datetime('now') WHERE id=?`, icon, id)
	return err
}

// UpdateWorkspaceCoder updates the inlined coder config for a workspace.
func (d *DB) UpdateWorkspaceCoder(id, kind, bin string, timeoutS int, backendType, provider, model, apiKeySecret, baseURL string) error {
	_, err := d.Exec(`UPDATE workspaces SET
		coder_kind=?, coder_bin=?, coder_timeout_s=?, coder_backend_type=?,
		coder_provider=?, coder_model=?, coder_api_key_secret=?, coder_base_url=?, updated_at=datetime('now')
		WHERE id=?`,
		coderKindOrDefault(kind), bin, timeoutS, backendType, provider, model, apiKeySecret, baseURL, id)
	return err
}

func (d *DB) MarkWorkspaceSetupComplete(id string) error {
	_, err := d.Exec(`UPDATE workspaces SET needs_setup=0, updated_at=datetime('now') WHERE id=?`, id)
	return err
}

func (d *DB) DeleteWorkspace(id string) error {
	_, err := d.Exec(`DELETE FROM workspaces WHERE id=?`, id)
	return err
}

func coderKindOrDefault(k string) string {
	if k == "" {
		return "local"
	}
	return k
}

func scanWorkspace(s scanner) (*Workspace, error) {
	var w Workspace
	var createdAt, updatedAt string
	var needsSetup int
	err := s.Scan(&w.ID, &w.Name, &w.About, &w.Icon, &w.EncryptedMasterPassword, &w.SecretsSalt,
		&w.CoderKind, &w.CoderBin, &w.CoderTimeoutS, &w.CoderBackendType,
		&w.CoderProvider, &w.CoderModel, &w.CoderAPIKeySecret, &w.CoderBaseURL,
		&needsSetup, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	w.NeedsSetup = needsSetup == 1
	w.CreatedAt = scanTime(createdAt)
	w.UpdatedAt = scanTime(updatedAt)
	return &w, nil
}

// ── Secrets ────────────────────────────────────────────────────────────────

func (d *DB) UpsertSecret(s *Secret) error {
	_, err := d.Exec(`INSERT INTO secrets(id, workspace_id, name, ciphertext, nonce, created_at, updated_at)
		VALUES(?,?,?,?,?,datetime('now'),datetime('now'))
		ON CONFLICT(workspace_id, name) DO UPDATE SET
		  ciphertext=excluded.ciphertext,
		  nonce=excluded.nonce,
		  updated_at=datetime('now')`,
		s.ID, s.WorkspaceID, s.Name, s.Ciphertext, s.Nonce)
	return err
}

func (d *DB) GetSecret(workspaceID, name string) (*Secret, error) {
	row := d.QueryRow(`SELECT id,workspace_id,name,ciphertext,nonce,created_at,updated_at FROM secrets WHERE workspace_id=? AND name=?`, workspaceID, name)
	var s Secret
	var createdAt, updatedAt string
	err := row.Scan(&s.ID, &s.WorkspaceID, &s.Name, &s.Ciphertext, &s.Nonce, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.CreatedAt = scanTime(createdAt)
	s.UpdatedAt = scanTime(updatedAt)
	return &s, nil
}

func (d *DB) ListSecretNames(workspaceID string) ([]string, error) {
	rows, err := d.Query(`SELECT name FROM secrets WHERE workspace_id=? ORDER BY name`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

func (d *DB) DeleteSecret(workspaceID, name string) error {
	res, err := d.Exec(`DELETE FROM secrets WHERE workspace_id=? AND name=?`, workspaceID, name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ── Platform connections ───────────────────────────────────────────────────

func (d *DB) UpsertPlatformConnection(c *PlatformConnection) error {
	_, err := d.Exec(`INSERT INTO platform_connections(id, workspace_id, platform, encrypted_token, encrypted_config, active, created_at, updated_at)
		VALUES(?,?,?,?,?,?,datetime('now'),datetime('now'))
		ON CONFLICT(workspace_id, platform) DO UPDATE SET
		  encrypted_token=excluded.encrypted_token,
		  encrypted_config=excluded.encrypted_config,
		  active=excluded.active,
		  updated_at=datetime('now')`,
		c.ID, c.WorkspaceID, c.Platform, c.EncryptedToken, c.EncryptedConfig, boolToInt(c.Active))
	return err
}

func (d *DB) GetPlatformConnection(workspaceID, platform string) (*PlatformConnection, error) {
	row := d.QueryRow(`SELECT id,workspace_id,platform,encrypted_token,encrypted_config,active,created_at,updated_at
		FROM platform_connections WHERE workspace_id=? AND platform=?`, workspaceID, platform)
	return scanPlatformConnection(row)
}

func (d *DB) ListActivePlatformConnections() ([]*PlatformConnection, error) {
	rows, err := d.Query(`SELECT id,workspace_id,platform,encrypted_token,encrypted_config,active,created_at,updated_at
		FROM platform_connections WHERE active=1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*PlatformConnection
	for rows.Next() {
		c, err := scanPlatformConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (d *DB) SetPlatformConnectionActive(workspaceID, platform string, active bool) error {
	_, err := d.Exec(`UPDATE platform_connections SET active=?, updated_at=datetime('now') WHERE workspace_id=? AND platform=?`,
		boolToInt(active), workspaceID, platform)
	return err
}

func (d *DB) DeletePlatformConnection(workspaceID, platform string) error {
	_, err := d.Exec(`DELETE FROM platform_connections WHERE workspace_id=? AND platform=?`, workspaceID, platform)
	return err
}

// WorkspaceBotIdentity pairs a workspace with the bot identity setting stored
// for one platform.
type WorkspaceBotIdentity struct {
	WorkspaceID   string
	WorkspaceName string
	IdentityJSON  string
}

// ListPlatformBotIdentities returns the stored bot identity of every workspace
// that currently has a connection for this platform, excluding one workspace
// (the one being saved, so re-saving a rotated token for the SAME workspace is
// not mistaken for a collision).
//
// It JOINS platform_connections deliberately rather than reading the settings
// table alone: disconnecting DELETEs the connection row but leaves the
// bot_identity.<platform> setting behind, so a settings-only query would report
// a workspace that no longer uses the bot at all and block a legitimate
// reconnect forever.
func (d *DB) ListPlatformBotIdentities(platform, excludeWorkspaceID, settingKey string) ([]WorkspaceBotIdentity, error) {
	rows, err := d.Query(`SELECT pc.workspace_id, w.name, s.value
		FROM platform_connections pc
		JOIN workspaces w ON w.id = pc.workspace_id
		JOIN workspace_settings s ON s.workspace_id = pc.workspace_id AND s.key = ?
		WHERE pc.platform = ? AND pc.workspace_id <> ?`, settingKey, platform, excludeWorkspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []WorkspaceBotIdentity
	for rows.Next() {
		var e WorkspaceBotIdentity
		if err := rows.Scan(&e.WorkspaceID, &e.WorkspaceName, &e.IdentityJSON); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (d *DB) ListWorkspacePlatformConnections(workspaceID string) ([]*PlatformConnection, error) {
	rows, err := d.Query(`SELECT id,workspace_id,platform,encrypted_token,encrypted_config,active,created_at,updated_at
		FROM platform_connections WHERE workspace_id=?`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*PlatformConnection
	for rows.Next() {
		c, err := scanPlatformConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func scanPlatformConnection(s scanner) (*PlatformConnection, error) {
	var c PlatformConnection
	var createdAt, updatedAt string
	var active int
	err := s.Scan(&c.ID, &c.WorkspaceID, &c.Platform, &c.EncryptedToken, &c.EncryptedConfig, &active, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.Active = active == 1
	c.CreatedAt = scanTime(createdAt)
	c.UpdatedAt = scanTime(updatedAt)
	return &c, nil
}

// ── Platform identities ────────────────────────────────────────────────────

func (d *DB) UpsertPlatformIdentity(i *PlatformIdentity) error {
	_, err := d.Exec(`INSERT INTO platform_identities(id, workspace_id, platform, platform_user_id, linked_at)
		VALUES(?,?,?,?,datetime('now'))
		ON CONFLICT(platform, platform_user_id) DO UPDATE SET
		  workspace_id=excluded.workspace_id`,
		i.ID, i.WorkspaceID, i.Platform, i.PlatformUserID)
	return err
}

func (d *DB) GetPlatformIdentity(platform, platformUserID string) (*PlatformIdentity, error) {
	row := d.QueryRow(`SELECT id,workspace_id,platform,platform_user_id,linked_at
		FROM platform_identities WHERE platform=? AND platform_user_id=?`, platform, platformUserID)
	var i PlatformIdentity
	var linkedAt string
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.Platform, &i.PlatformUserID, &linkedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	i.LinkedAt = scanTime(linkedAt)
	return &i, nil
}

func (d *DB) ListPlatformIdentities(workspaceID, platform string) ([]*PlatformIdentity, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if platform == "" {
		rows, err = d.Query(`SELECT id,workspace_id,platform,platform_user_id,linked_at
			FROM platform_identities WHERE workspace_id=?
			ORDER BY linked_at, platform, id`, workspaceID)
	} else {
		rows, err = d.Query(`SELECT id,workspace_id,platform,platform_user_id,linked_at
			FROM platform_identities WHERE workspace_id=? AND platform=?
			ORDER BY linked_at, platform, id`, workspaceID, platform)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*PlatformIdentity
	for rows.Next() {
		var i PlatformIdentity
		var linkedAt string
		if err := rows.Scan(&i.ID, &i.WorkspaceID, &i.Platform, &i.PlatformUserID, &linkedAt); err != nil {
			return nil, err
		}
		i.LinkedAt = scanTime(linkedAt)
		out = append(out, &i)
	}
	return out, rows.Err()
}

// DeletePlatformIdentity unlinks a workspace from one chat platform. Deleting an
// identity that is not there is a no-op: the Unlink action can race a link that
// was already removed, and reporting that as an error would be noise.
func (d *DB) DeletePlatformIdentity(workspaceID, platform string) error {
	_, err := d.Exec(`DELETE FROM platform_identities WHERE workspace_id=? AND platform=?`,
		workspaceID, platform)
	return err
}

// HasPlatformIdentity returns true when the user has at least one linked
// platform (Telegram, etc.). Used to skip reminders and agent runs for
// workspaces who have no way to receive output, preventing wasted API usage.
func (d *DB) HasPlatformIdentity(workspaceID string) bool {
	var count int
	_ = d.QueryRow(`SELECT COUNT(*) FROM platform_identities WHERE workspace_id=?`, workspaceID).Scan(&count)
	return count > 0
}

// ── Agents ─────────────────────────────────────────────────────────────────

func (d *DB) CreateAgent(a *Agent) error {
	_, err := d.Exec(`INSERT INTO agents(id,workspace_id,name,description,active,created_at,updated_at)
		VALUES(?,?,?,?,1,datetime('now'),datetime('now'))`,
		a.ID, a.WorkspaceID, a.Name, a.Description)
	return err
}

func (d *DB) GetAgent(id string) (*Agent, error) {
	row := d.QueryRow(`SELECT id,workspace_id,name,description,active,created_at,updated_at FROM agents WHERE id=?`, id)
	return scanAgent(row)
}

func (d *DB) GetAgentByName(workspaceID, name string) (*Agent, error) {
	row := d.QueryRow(`SELECT id,workspace_id,name,description,active,created_at,updated_at FROM agents WHERE workspace_id=? AND name=?`, workspaceID, name)
	return scanAgent(row)
}

func (d *DB) ListAgents(workspaceID string) ([]*Agent, error) {
	rows, err := d.Query(`SELECT id,workspace_id,name,description,active,created_at,updated_at FROM agents WHERE workspace_id=? ORDER BY name`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var agents []*Agent
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

// UpdateAgentDescription updates an agent's description (used when an edit session
// regenerates AGENT.md). Name/ID are immutable in the edit flow.
func (d *DB) UpdateAgentDescription(id, description string) error {
	_, err := d.Exec(`UPDATE agents SET description=?, updated_at=datetime('now') WHERE id=?`, description, id)
	return err
}

func (d *DB) SetAgentActive(id string, active bool) error {
	_, err := d.Exec(`UPDATE agents SET active=?, updated_at=datetime('now') WHERE id=?`, boolToInt(active), id)
	return err
}

func (d *DB) DeleteAgent(id string) error {
	_, err := d.Exec(`DELETE FROM agents WHERE id=?`, id)
	return err
}

func scanAgent(s scanner) (*Agent, error) {
	var a Agent
	var createdAt, updatedAt string
	var active int
	err := s.Scan(&a.ID, &a.WorkspaceID, &a.Name, &a.Description, &active, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.Active = active == 1
	a.CreatedAt = scanTime(createdAt)
	a.UpdatedAt = scanTime(updatedAt)
	return &a, nil
}

// ── Agent runs ─────────────────────────────────────────────────────────────

func (d *DB) CreateAgentRun(r *AgentRun) error {
	_, err := d.Exec(`INSERT INTO agent_runs(id,agent_id,workspace_id,trigger,started_at)
		VALUES(?,?,?,?,datetime('now'))`,
		r.ID, r.AgentID, r.WorkspaceID, r.Trigger)
	return err
}

// RunOutcome is everything recorded about a run when it finishes.
//
// A struct rather than more positional parameters: the argument list was
// already seven long, and `transcript` and `stderr` are both strings whose
// meanings are easy to swap without the compiler noticing.
type RunOutcome struct {
	ExitCode int
	Stdout   string
	Stderr   string
	// Transcript is the run's captured tool calls and coder turns. Empty for a
	// run recorded before the column existed, and for one that produced none.
	Transcript string
	// Silent records that the run emitted [SILENT] — it chose to say nothing,
	// as distinct from having nothing to say because it broke.
	Silent           bool
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	// CachedTokens is the part of PromptTokens the provider served from its
	// prompt cache; CostUSD is what the provider said the run cost, summed over
	// its turns. Each carries a *Reported flag rather than using 0 as a
	// sentinel: a provider reporting zero and a provider reporting nothing are
	// opposite findings, and a CLI coder reports neither — showing its runs as
	// "$0.00" would read as free rather than as unmeasured.
	CachedTokens  int
	CacheReported bool
	CostUSD       float64
	CostReported  bool
}

func (d *DB) FinishAgentRun(id string, out RunOutcome) error {
	_, err := d.Exec(`UPDATE agent_runs SET exit_code=?, stdout=?, stderr=?, transcript=?, silent=?, prompt_tokens=?, completion_tokens=?, total_tokens=?, cached_tokens=?, cache_reported=?, cost_usd=?, cost_reported=?, finished_at=datetime('now') WHERE id=?`,
		out.ExitCode, out.Stdout, out.Stderr, out.Transcript, out.Silent,
		out.PromptTokens, out.CompletionTokens, out.TotalTokens,
		out.CachedTokens, out.CacheReported, out.CostUSD, out.CostReported, id)
	return err
}

// GetAgentRun returns one run INCLUDING its transcript.
//
// Separate from ListAgentRuns, which deliberately omits the transcript: the
// agent detail page lists every recent run, and shipping each one's full
// transcript on every page load would pay for a panel that is collapsed by
// default. This is the lazy read behind expanding a single row.
func (d *DB) GetAgentRun(runID string) (*AgentRun, error) {
	row := d.QueryRow(`SELECT id,agent_id,workspace_id,trigger,exit_code,stdout,stderr,transcript,silent,prompt_tokens,completion_tokens,total_tokens,cached_tokens,cache_reported,cost_usd,cost_reported,started_at,finished_at
		FROM agent_runs WHERE id=?`, runID)
	var r AgentRun
	var exitCode sql.NullInt64
	var stdout, stderr, transcript sql.NullString
	var startedAt string
	var finishedAt sql.NullString
	if err := row.Scan(&r.ID, &r.AgentID, &r.WorkspaceID, &r.Trigger, &exitCode, &stdout, &stderr,
		&transcript, &r.Silent, &r.PromptTokens, &r.CompletionTokens, &r.TotalTokens,
		&r.CachedTokens, &r.CacheReported, &r.CostUSD, &r.CostReported, &startedAt, &finishedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if exitCode.Valid {
		v := int(exitCode.Int64)
		r.ExitCode = &v
	}
	r.Stdout = stdout.String
	r.Stderr = stderr.String
	r.Transcript = transcript.String
	r.StartedAt = scanTime(startedAt)
	if finishedAt.Valid {
		t := scanTime(finishedAt.String)
		r.FinishedAt = &t
	}
	return &r, nil
}

// ListAgentRuns omits `transcript` on purpose — see GetAgentRun. `silent` IS
// selected, because the list renders a chip for it and a per-row transcript
// fetch just to decide whether to draw a chip would defeat the split.
func (d *DB) ListAgentRuns(agentID string, limit int) ([]*AgentRun, error) {
	rows, err := d.Query(`SELECT id,agent_id,workspace_id,trigger,exit_code,stdout,stderr,silent,prompt_tokens,completion_tokens,total_tokens,cached_tokens,cache_reported,cost_usd,cost_reported,started_at,finished_at
		FROM agent_runs WHERE agent_id=? ORDER BY started_at DESC LIMIT ?`, agentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []*AgentRun
	for rows.Next() {
		var r AgentRun
		var exitCode sql.NullInt64
		var stdout, stderr sql.NullString
		var startedAt string
		var finishedAt sql.NullString
		// stdout/stderr are NULL until FinishAgentRun runs; an in-progress (async)
		// run row is listed on the detail page, so scan through NullString.
		if err := rows.Scan(&r.ID, &r.AgentID, &r.WorkspaceID, &r.Trigger, &exitCode, &stdout, &stderr, &r.Silent,
			&r.PromptTokens, &r.CompletionTokens, &r.TotalTokens,
			&r.CachedTokens, &r.CacheReported, &r.CostUSD, &r.CostReported, &startedAt, &finishedAt); err != nil {
			return nil, err
		}
		if exitCode.Valid {
			v := int(exitCode.Int64)
			r.ExitCode = &v
		}
		r.Stdout = stdout.String
		r.Stderr = stderr.String
		r.StartedAt = scanTime(startedAt)
		if finishedAt.Valid {
			t := scanTime(finishedAt.String)
			r.FinishedAt = &t
		}
		runs = append(runs, &r)
	}
	return runs, rows.Err()
}

// GetUnfinishedAgentRun returns the most recent run for an agent that has not yet
// finished (finished_at IS NULL), or (nil, nil) if there is none. Used to show a
// durable "Running…" badge that survives a page reload (the in-memory run tracker
// drives the live SSE stream; this DB row drives the badge).
func (d *DB) GetUnfinishedAgentRun(agentID string) (*AgentRun, error) {
	row := d.QueryRow(`SELECT id,agent_id,workspace_id,trigger,started_at
		FROM agent_runs WHERE agent_id=? AND finished_at IS NULL
		ORDER BY started_at DESC LIMIT 1`, agentID)
	var r AgentRun
	var startedAt string
	if err := row.Scan(&r.ID, &r.AgentID, &r.WorkspaceID, &r.Trigger, &startedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	r.StartedAt = scanTime(startedAt)
	return &r, nil
}

// InterruptedRun identifies a scheduled run that was cut off mid-flight, so the
// scheduler can retry it once on the next boot.
type InterruptedRun struct {
	RunID       string
	AgentID     string
	WorkspaceID string
}

// ReconcileStaleRuns marks every run still flagged in-progress (finished_at IS NULL)
// as finished with exit code -1. Called once on server startup: a crash or shutdown
// mid-run otherwise leaves the row open forever, showing a permanently stuck
// "Running…" badge. Returns the number of rows reconciled, plus the subset that were
// interrupted CRON runs — the ones the scheduler retries once (see scheduler.Recover).
//
// The SELECT has to happen HERE rather than in a second exported call, because
// `finished_at IS NULL` is the only unambiguous signal that a run was interrupted and
// this very UPDATE destroys it. `exit_code=-1` cannot stand in: FinishAgentRun writes
// the same -1 for a run that failed honestly. Reading it afterwards would need a
// caller to remember an ordering the compiler cannot enforce, so the two steps are one
// transaction and one function.
//
// Only trigger='cron' is reported. A manual run has a human in front of it who can
// press the button again; a chat run's requester is long gone; and 'cron-retry' is the
// trigger the retry itself carries, which is precisely what stops an agent that kills
// the server from being retried forever, once per boot.
func (d *DB) ReconcileStaleRuns() (int64, []InterruptedRun, error) {
	tx, err := d.Begin()
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback()

	rows, err := tx.Query(`SELECT id,agent_id,workspace_id FROM agent_runs
		WHERE finished_at IS NULL AND trigger='cron'`)
	if err != nil {
		return 0, nil, err
	}
	var interrupted []InterruptedRun
	for rows.Next() {
		var r InterruptedRun
		if err := rows.Scan(&r.RunID, &r.AgentID, &r.WorkspaceID); err != nil {
			rows.Close()
			return 0, nil, err
		}
		interrupted = append(interrupted, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, nil, err
	}
	rows.Close()

	res, err := tx.Exec(`UPDATE agent_runs
		SET finished_at=datetime('now'), exit_code=-1,
		    stderr=CASE WHEN stderr IS NULL OR stderr='' THEN 'run interrupted by server restart' ELSE stderr END
		WHERE finished_at IS NULL`)
	if err != nil {
		return 0, nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil, err
	}
	if err := tx.Commit(); err != nil {
		return 0, nil, err
	}
	return n, interrupted, nil
}

// ── Agent schedules ────────────────────────────────────────────────────────

func (d *DB) UpsertAgentSchedule(s *AgentSchedule) error {
	var nextRun *string
	if s.NextRunAt != nil {
		t := s.NextRunAt.UTC().Format("2006-01-02 15:04:05")
		nextRun = &t
	}
	_, err := d.Exec(`INSERT INTO agent_schedules(id,agent_id,workspace_id,cron_expr,next_run_at,enabled,timezone,created_at)
		VALUES(?,?,?,?,?,1,?,datetime('now'))
		ON CONFLICT(id) DO UPDATE SET cron_expr=excluded.cron_expr, next_run_at=excluded.next_run_at, enabled=excluded.enabled, timezone=excluded.timezone`,
		s.ID, s.AgentID, s.WorkspaceID, s.CronExpr, nextRun, s.Timezone)
	return err
}

func (d *DB) ListDueSchedules(now time.Time) ([]*AgentSchedule, error) {
	rows, err := d.Query(`SELECT s.id,s.agent_id,s.workspace_id,s.cron_expr,s.next_run_at,s.last_run_at,s.enabled,s.created_at,s.timezone
		FROM agent_schedules s
		JOIN agents a ON a.id = s.agent_id AND a.active = 1
		WHERE s.enabled=1 AND (s.next_run_at IS NULL OR s.next_run_at <= ?)`,
		now.UTC().Format("2006-01-02 15:04:05"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var schedules []*AgentSchedule
	for rows.Next() {
		var s AgentSchedule
		var nextRunAt, lastRunAt sql.NullString
		var createdAt string
		var enabled int
		if err := rows.Scan(&s.ID, &s.AgentID, &s.WorkspaceID, &s.CronExpr, &nextRunAt, &lastRunAt, &enabled, &createdAt, &s.Timezone); err != nil {
			return nil, err
		}
		s.NextRunAt = scanTimePtr(nullToPtr(nextRunAt))
		s.LastRunAt = scanTimePtr(nullToPtr(lastRunAt))
		s.Enabled = enabled == 1
		s.CreatedAt = scanTime(createdAt)
		schedules = append(schedules, &s)
	}
	return schedules, rows.Err()
}

func (d *DB) UpdateScheduleRunTimes(id string, last, next time.Time) error {
	_, err := d.Exec(`UPDATE agent_schedules SET last_run_at=?, next_run_at=? WHERE id=?`,
		last.UTC().Format("2006-01-02 15:04:05"),
		next.UTC().Format("2006-01-02 15:04:05"),
		id)
	return err
}

func (d *DB) DeleteAgentSchedule(agentID string) error {
	_, err := d.Exec(`DELETE FROM agent_schedules WHERE agent_id=?`, agentID)
	return err
}

// ScheduleWithName embeds AgentSchedule with the agent name joined in, for
// the "upcoming" list on the home dashboard.
type ScheduleWithName struct {
	AgentSchedule
	AgentName string
}

// ListWorkspaceSchedulesWithNames returns every ENABLED schedule for active
// agents in the workspace, agent name joined in, ordered by next_run_at
// ascending (soonest first; schedules with no next_run_at yet sort last).
// Mirrors ListDueSchedules' `s.enabled=1 JOIN agents a ON ... AND a.active=1`
// semantics — a paused agent's schedule will never fire, so it isn't
// "upcoming".
func (d *DB) ListWorkspaceSchedulesWithNames(workspaceID string) ([]*ScheduleWithName, error) {
	rows, err := d.Query(`SELECT s.id,s.agent_id,s.workspace_id,s.cron_expr,s.next_run_at,s.last_run_at,s.enabled,s.created_at,a.name
		FROM agent_schedules s
		JOIN agents a ON a.id = s.agent_id AND a.active = 1
		WHERE s.workspace_id=? AND s.enabled=1
		ORDER BY (s.next_run_at IS NULL), s.next_run_at ASC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ScheduleWithName{}
	for rows.Next() {
		var s ScheduleWithName
		var nextRunAt, lastRunAt sql.NullString
		var createdAt string
		var enabled int
		if err := rows.Scan(&s.ID, &s.AgentID, &s.WorkspaceID, &s.CronExpr, &nextRunAt, &lastRunAt, &enabled, &createdAt, &s.AgentName); err != nil {
			return nil, err
		}
		s.NextRunAt = scanTimePtr(nullToPtr(nextRunAt))
		s.LastRunAt = scanTimePtr(nullToPtr(lastRunAt))
		s.Enabled = enabled == 1
		s.CreatedAt = scanTime(createdAt)
		out = append(out, &s)
	}
	return out, rows.Err()
}

// ── Chats ──────────────────────────────────────────────────────────────────

func (d *DB) CreateChat(c *Chat) error {
	_, err := d.Exec(`INSERT INTO chats(id,workspace_id,agent_id,name,platform,active,created_at,last_seen)
		VALUES(?,?,?,?,?,1,datetime('now'),datetime('now'))`,
		c.ID, c.WorkspaceID, c.AgentID, c.Name, c.Platform)
	return err
}

func (d *DB) GetChat(id string) (*Chat, error) {
	row := d.QueryRow(`SELECT id,workspace_id,agent_id,name,platform,active,created_at,last_seen FROM chats WHERE id=?`, id)
	return scanChat(row)
}

func (d *DB) ListChats(workspaceID string) ([]*Chat, error) {
	rows, err := d.Query(`SELECT id,workspace_id,agent_id,name,platform,active,created_at,last_seen
		FROM chats WHERE workspace_id=? ORDER BY last_seen DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var chats []*Chat
	for rows.Next() {
		c, err := scanChat(rows)
		if err != nil {
			return nil, err
		}
		chats = append(chats, c)
	}
	return chats, rows.Err()
}

func (d *DB) TouchChat(id string) error {
	_, err := d.Exec(`UPDATE chats SET last_seen=datetime('now') WHERE id=?`, id)
	return err
}

func (d *DB) StopChat(id string) error {
	_, err := d.Exec(`UPDATE chats SET active=0 WHERE id=?`, id)
	return err
}

func (d *DB) DeleteChat(id string) error {
	_, err := d.Exec(`DELETE FROM chats WHERE id=?`, id)
	return err
}

func (d *DB) ListStaleChats(before time.Time) ([]*Chat, error) {
	rows, err := d.Query(`SELECT id,workspace_id,agent_id,name,platform,active,created_at,last_seen
		FROM chats WHERE active=1 AND last_seen < ?`,
		before.UTC().Format("2006-01-02 15:04:05"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var chats []*Chat
	for rows.Next() {
		c, err := scanChat(rows)
		if err != nil {
			return nil, err
		}
		chats = append(chats, c)
	}
	return chats, rows.Err()
}

func scanChat(s scanner) (*Chat, error) {
	var c Chat
	var agentID sql.NullString
	var active int
	var createdAt, lastSeen string
	err := s.Scan(&c.ID, &c.WorkspaceID, &agentID, &c.Name, &c.Platform, &active, &createdAt, &lastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if agentID.Valid {
		c.AgentID = &agentID.String
	}
	c.Active = active == 1
	c.CreatedAt = scanTime(createdAt)
	c.LastSeen = scanTime(lastSeen)
	return &c, nil
}

// ── Chat messages ─────────────────────────────────────────────────────────

func (d *DB) AddChatMessage(chatID, role, content string) error {
	_, err := d.Exec(`INSERT INTO chat_messages(chat_id,role,content,created_at) VALUES(?,?,?,datetime('now'))`,
		chatID, role, content)
	return err
}

// UpdateChatName renames a chat. Used by the auto-title flow to replace the
// default "Chat <timestamp>" name with a content-derived topic.
func (d *DB) UpdateChatName(chatID, name string) error {
	_, err := d.Exec(`UPDATE chats SET name=? WHERE id=?`, name, chatID)
	return err
}

func (d *DB) ListChatMessages(chatID string) ([]ChatMessage, error) {
	rows, err := d.Query(`SELECT id,chat_id,role,content,created_at FROM chat_messages WHERE chat_id=? ORDER BY id ASC`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChatMessage
	for rows.Next() {
		var m ChatMessage
		var createdAt string
		if err := rows.Scan(&m.ID, &m.ChatID, &m.Role, &m.Content, &createdAt); err != nil {
			return nil, err
		}
		m.CreatedAt = scanTime(createdAt)
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetActiveChatForPlatform returns the most-recently-touched active chat
// for the given user on the given platform, or nil if none exists.
func (d *DB) GetActiveChatForPlatform(workspaceID, platform string) (*Chat, error) {
	row := d.QueryRow(`SELECT id,workspace_id,agent_id,name,platform,active,created_at,last_seen
		FROM chats WHERE workspace_id=? AND platform=? AND active=1
		ORDER BY last_seen DESC LIMIT 1`, workspaceID, platform)
	c, err := scanChat(row)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return c, err
}

// FindChatByPrefix returns the first chat whose ID starts with the given prefix.
func (d *DB) FindChatByPrefix(workspaceID, prefix string) (*Chat, error) {
	rows, err := d.Query(`SELECT id,workspace_id,agent_id,name,platform,active,created_at,last_seen
		FROM chats WHERE workspace_id=? AND id LIKE ? ORDER BY last_seen DESC LIMIT 1`,
		workspaceID, prefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, ErrNotFound
	}
	return scanChat(rows)
}

// ResumeChat sets a chat active again.
func (d *DB) ResumeChat(id string) error {
	_, err := d.Exec(`UPDATE chats SET active=1, last_seen=datetime('now') WHERE id=?`, id)
	return err
}

// ── Reminders ──────────────────────────────────────────────────────────────

func (d *DB) CreateReminder(r *Reminder) error {
	_, err := d.Exec(`INSERT INTO reminders(id,workspace_id,message,remind_at,recurrence,sent,created_at)
		VALUES(?,?,?,?,?,0,datetime('now'))`,
		r.ID, r.WorkspaceID, r.Message, r.RemindAt.UTC().Format("2006-01-02 15:04:05"), r.Recurrence)
	return err
}

func (d *DB) GetReminder(id string) (*Reminder, error) {
	row := d.QueryRow(`SELECT id,workspace_id,message,remind_at,recurrence,sent,created_at FROM reminders WHERE id=?`, id)
	return scanReminder(row)
}

func (d *DB) ListReminders(workspaceID string) ([]*Reminder, error) {
	rows, err := d.Query(`SELECT id,workspace_id,message,remind_at,recurrence,sent,created_at
		FROM reminders WHERE workspace_id=? ORDER BY remind_at`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Reminder
	for rows.Next() {
		r, err := scanReminder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (d *DB) ListDueReminders(now time.Time) ([]*Reminder, error) {
	rows, err := d.Query(`SELECT id,workspace_id,message,remind_at,recurrence,sent,created_at
		FROM reminders WHERE sent=0 AND remind_at <= ?`,
		now.UTC().Format("2006-01-02 15:04:05"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Reminder
	for rows.Next() {
		r, err := scanReminder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (d *DB) MarkReminderSent(id string) error {
	_, err := d.Exec(`UPDATE reminders SET sent=1 WHERE id=?`, id)
	return err
}

func (d *DB) DeleteReminder(id string) error {
	res, err := d.Exec(`DELETE FROM reminders WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanReminder(s scanner) (*Reminder, error) {
	var r Reminder
	var remindAt, createdAt string
	var sent int
	err := s.Scan(&r.ID, &r.WorkspaceID, &r.Message, &remindAt, &r.Recurrence, &sent, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.Sent = sent == 1
	r.RemindAt = scanTime(remindAt)
	r.CreatedAt = scanTime(createdAt)
	return &r, nil
}

// ── Inbox ───────────────────────────────────────────────────────────────────

func (d *DB) CreateInboxMessage(m *InboxMessage) error {
	// agent_id is a nullable FK: insert NULL (not "") so reminders don't trip
	// foreign-key enforcement.
	var agentID any
	if m.AgentID != "" {
		agentID = m.AgentID
	}
	_, err := d.Exec(`INSERT INTO inbox_messages(id,workspace_id,source,agent_id,agent_name,ref_id,trigger,body,status,read_at,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,NULL,datetime('now'))`,
		m.ID, m.WorkspaceID, m.Source, agentID, m.AgentName, m.RefID, m.Trigger, m.Body, m.Status)
	return err
}

func (d *DB) ListInboxMessages(workspaceID string, limit, offset int) ([]*InboxMessage, error) {
	rows, err := d.Query(`SELECT id,workspace_id,source,agent_id,agent_name,ref_id,trigger,body,status,read_at,created_at
		FROM inbox_messages WHERE workspace_id=? ORDER BY created_at DESC, rowid DESC LIMIT ? OFFSET ?`,
		workspaceID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*InboxMessage
	for rows.Next() {
		m, err := scanInboxMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (d *DB) UnreadInboxCount(workspaceID string) (int, error) {
	var n int
	err := d.QueryRow(`SELECT COUNT(*) FROM inbox_messages WHERE workspace_id=? AND read_at IS NULL`, workspaceID).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (d *DB) MarkInboxRead(id, workspaceID string) error {
	res, err := d.Exec(`UPDATE inbox_messages SET read_at=datetime('now') WHERE id=? AND workspace_id=? AND read_at IS NULL`,
		id, workspaceID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (d *DB) MarkAllInboxRead(workspaceID string) error {
	_, err := d.Exec(`UPDATE inbox_messages SET read_at=datetime('now') WHERE workspace_id=? AND read_at IS NULL`, workspaceID)
	return err
}

func (d *DB) DeleteInboxMessage(id, workspaceID string) error {
	res, err := d.Exec(`DELETE FROM inbox_messages WHERE id=? AND workspace_id=?`, id, workspaceID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanInboxMessage(s scanner) (*InboxMessage, error) {
	var m InboxMessage
	var agentID sql.NullString
	var readAt sql.NullString
	var createdAt string
	err := s.Scan(&m.ID, &m.WorkspaceID, &m.Source, &agentID, &m.AgentName, &m.RefID, &m.Trigger, &m.Body, &m.Status, &readAt, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	m.AgentID = agentID.String
	if readAt.Valid && readAt.String != "" {
		t := scanTime(readAt.String)
		if !t.IsZero() {
			m.ReadAt = &t
		}
	}
	m.CreatedAt = scanTime(createdAt)
	return &m, nil
}

func (d *DB) WriteAuditLog(a *AuditLog) error {
	_, err := d.Exec(`INSERT INTO audit_logs(id,workspace_id,action,target,detail,ip_address,created_at)
		VALUES(?,?,?,?,?,?,datetime('now'))`,
		a.ID, a.WorkspaceID, a.Action, a.Target, a.Detail, a.IPAddress)
	return err
}

// AuditLogFilter narrows a ListAuditLogs query. A zero value means "no
// filtering" and behaves exactly like the old unfiltered call.
//
// Filtering is done in SQL rather than over an already-truncated page: a
// filter applied to the last 100 rows client-side would report "no matches"
// for an action that simply happened 101 events ago, which is worse than no
// filter at all because it looks like an answer.
type AuditLogFilter struct {
	WorkspaceID string    // exact match; "" means any
	Action      string    // exact match; "" means any
	Query       string    // substring over target/detail/ip; "" means any
	Since       time.Time // only entries at or after this instant; zero means any
	Limit       int       // <= 0 falls back to 100
}

// DistinctAuditActions returns every action value present in the log, sorted,
// so the UI can offer a picker instead of asking the operator to recall exact
// action strings.
func (d *DB) DistinctAuditActions() ([]string, error) {
	rows, err := d.Query(`SELECT DISTINCT action FROM audit_logs ORDER BY action`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListAuditLogsFiltered is ListAuditLogs with an optional filter.
func (d *DB) ListAuditLogsFiltered(f AuditLogFilter) ([]*AuditLog, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	q := `SELECT id,workspace_id,action,target,detail,ip_address,created_at
		FROM audit_logs WHERE 1=1`
	args := []any{}
	if f.WorkspaceID != "" {
		q += ` AND workspace_id = ?`
		args = append(args, f.WorkspaceID)
	}
	if f.Action != "" {
		q += ` AND action = ?`
		args = append(args, f.Action)
	}
	if f.Query != "" {
		// LIKE with escaped wildcards — a user searching for a literal "%"
		// should not silently match everything.
		like := "%" + escapeLike(f.Query) + "%"
		q += ` AND (target LIKE ? ESCAPE '\' OR detail LIKE ? ESCAPE '\' OR ip_address LIKE ? ESCAPE '\')`
		args = append(args, like, like, like)
	}
	if !f.Since.IsZero() {
		// created_at is stored by SQLite's datetime('now'), which is UTC.
		q += ` AND created_at >= ?`
		args = append(args, f.Since.UTC().Format("2006-01-02 15:04:05"))
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := d.Query(q, args...)
	if err != nil {
		return nil, err
	}
	return scanAuditLogs(rows)
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func (d *DB) ListAuditLogs(limit int) ([]*AuditLog, error) {
	return d.ListAuditLogsFiltered(AuditLogFilter{Limit: limit})
}

// scanAuditLogs consumes and closes rows shaped like the audit_logs SELECT
// above. Shared so the filtered and unfiltered paths cannot decode differently.
func scanAuditLogs(rows *sql.Rows) ([]*AuditLog, error) {
	defer rows.Close()
	var logs []*AuditLog
	for rows.Next() {
		var a AuditLog
		var workspaceID sql.NullString
		var createdAt string
		if err := rows.Scan(&a.ID, &workspaceID, &a.Action, &a.Target, &a.Detail, &a.IPAddress, &createdAt); err != nil {
			return nil, err
		}
		if workspaceID.Valid {
			a.WorkspaceID = &workspaceID.String
		}
		a.CreatedAt = scanTime(createdAt)
		logs = append(logs, &a)
	}
	return logs, rows.Err()
}

// ── System settings (owner/system-level, not tenant-scoped) ─────────────────

func (d *DB) GetSystemSetting(key string) (string, error) {
	var value string
	err := d.QueryRow(`SELECT value FROM system_settings WHERE key=?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return value, err
}

func (d *DB) SetSystemSetting(key, value string) error {
	_, err := d.Exec(`INSERT INTO system_settings(key,value,updated_at)
		VALUES(?,?,datetime('now'))
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=datetime('now')`,
		key, value)
	return err
}

// ── Workspace settings ───────────────────────────────────────────────────────

func (d *DB) GetSetting(workspaceID, key string) (string, error) {
	var value string
	err := d.QueryRow(`SELECT value FROM workspace_settings WHERE workspace_id=? AND key=?`, workspaceID, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return value, err
}

func (d *DB) SetSetting(workspaceID, key, value string) error {
	_, err := d.Exec(`INSERT INTO workspace_settings(id,workspace_id,key,value,updated_at)
		VALUES(lower(hex(randomblob(16))),?,?,?,datetime('now'))
		ON CONFLICT(workspace_id,key) DO UPDATE SET value=excluded.value, updated_at=datetime('now')`,
		workspaceID, key, value)
	return err
}

// SecretExists reports whether the user has a secret with the given name.
// It does NOT decrypt the value — safe to call without a master password.
func (d *DB) SecretExists(workspaceID, name string) (bool, error) {
	var n int
	err := d.QueryRow(`SELECT COUNT(*) FROM secrets WHERE workspace_id=? AND name=?`, workspaceID, name).Scan(&n)
	return n > 0, err
}

// ── Helpers ────────────────────────────────────────────────────────────────

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullToPtr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	return &n.String
}

// CountWorkspaces returns total user count (used by admin dashboard).
func (d *DB) CountWorkspaces() (int, error) {
	var count int
	err := d.QueryRow(`SELECT COUNT(*) FROM workspaces`).Scan(&count)
	return count, err
}

// CountAgents returns total agent count optionally scoped to a user.
func (d *DB) CountAgents(workspaceID string) (int, error) {
	var count int
	var err error
	if workspaceID == "" {
		err = d.QueryRow(`SELECT COUNT(*) FROM agents`).Scan(&count)
	} else {
		err = d.QueryRow(`SELECT COUNT(*) FROM agents WHERE workspace_id=?`, workspaceID).Scan(&count)
	}
	return count, err
}

// AgentRunWithName embeds AgentRun with the agent name for display purposes.
type AgentRunWithName struct {
	AgentRun
	AgentName string
}

// RecentAgentRunsWithNames returns the N most recent runs with the agent name joined in.
func (d *DB) RecentAgentRunsWithNames(workspaceID string, limit int) ([]*AgentRunWithName, error) {
	rows, err := d.Query(`SELECT r.id,r.agent_id,r.workspace_id,r.trigger,r.exit_code,r.stdout,r.stderr,r.prompt_tokens,r.completion_tokens,r.total_tokens,r.started_at,r.finished_at, COALESCE(a.name,'')
		FROM agent_runs r LEFT JOIN agents a ON a.id=r.agent_id
		WHERE r.workspace_id=? ORDER BY r.started_at DESC LIMIT ?`, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []*AgentRunWithName
	for rows.Next() {
		var rn AgentRunWithName
		var exitCode sql.NullInt64
		var startedAt string
		var finishedAt sql.NullString
		if err := rows.Scan(&rn.ID, &rn.AgentID, &rn.WorkspaceID, &rn.Trigger, &exitCode, &rn.Stdout, &rn.Stderr, &rn.PromptTokens, &rn.CompletionTokens, &rn.TotalTokens, &startedAt, &finishedAt, &rn.AgentName); err != nil {
			return nil, err
		}
		if exitCode.Valid {
			v := int(exitCode.Int64)
			rn.ExitCode = &v
		}
		rn.StartedAt = scanTime(startedAt)
		if finishedAt.Valid {
			t := scanTime(finishedAt.String)
			rn.FinishedAt = &t
		}
		runs = append(runs, &rn)
	}
	return runs, rows.Err()
}

// RecentAgentRuns returns the N most recent runs across all agents for a user.
func (d *DB) RecentAgentRuns(workspaceID string, limit int) ([]*AgentRun, error) {
	rows, err := d.Query(`SELECT r.id,r.agent_id,r.workspace_id,r.trigger,r.exit_code,r.stdout,r.stderr,r.prompt_tokens,r.completion_tokens,r.total_tokens,r.started_at,r.finished_at
		FROM agent_runs r WHERE r.workspace_id=? ORDER BY r.started_at DESC LIMIT ?`, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []*AgentRun
	for rows.Next() {
		var r AgentRun
		var exitCode sql.NullInt64
		var stdout, stderr sql.NullString
		var startedAt string
		var finishedAt sql.NullString
		// stdout/stderr are NULL until FinishAgentRun runs; an in-progress (async)
		// run row is listed on the detail page, so scan through NullString.
		if err := rows.Scan(&r.ID, &r.AgentID, &r.WorkspaceID, &r.Trigger, &exitCode, &stdout, &stderr, &r.PromptTokens, &r.CompletionTokens, &r.TotalTokens, &startedAt, &finishedAt); err != nil {
			return nil, err
		}
		if exitCode.Valid {
			v := int(exitCode.Int64)
			r.ExitCode = &v
		}
		r.Stdout = stdout.String
		r.Stderr = stderr.String
		r.StartedAt = scanTime(startedAt)
		if finishedAt.Valid {
			t := scanTime(finishedAt.String)
			r.FinishedAt = &t
		}
		runs = append(runs, &r)
	}
	return runs, rows.Err()
}

// GetAgentWithSchedule returns agent + schedule info as a convenience join.
type AgentWithSchedule struct {
	Agent    *Agent
	Schedule *AgentSchedule // nil if no schedule
}

func (d *DB) GetAgentWithSchedule(agentID string) (*AgentWithSchedule, error) {
	a, err := d.GetAgent(agentID)
	if err != nil {
		return nil, err
	}
	result := &AgentWithSchedule{Agent: a}

	row := d.QueryRow(`SELECT id,agent_id,workspace_id,cron_expr,next_run_at,last_run_at,enabled,created_at
		FROM agent_schedules WHERE agent_id=?`, agentID)
	var s AgentSchedule
	var nextRunAt, lastRunAt sql.NullString
	var createdAt string
	var enabled int
	err = row.Scan(&s.ID, &s.AgentID, &s.WorkspaceID, &s.CronExpr, &nextRunAt, &lastRunAt, &enabled, &createdAt)
	if err == nil {
		s.NextRunAt = scanTimePtr(nullToPtr(nextRunAt))
		s.LastRunAt = scanTimePtr(nullToPtr(lastRunAt))
		s.Enabled = enabled == 1
		s.CreatedAt = scanTime(createdAt)
		result.Schedule = &s
	}
	return result, nil
}

// GetScheduleForAgent returns the schedule for an agent, or nil if none.
func (d *DB) GetScheduleForAgent(agentID string) (*AgentSchedule, error) {
	row := d.QueryRow(`SELECT id,agent_id,workspace_id,cron_expr,next_run_at,last_run_at,enabled,created_at
		FROM agent_schedules WHERE agent_id=?`, agentID)
	var s AgentSchedule
	var nextRunAt, lastRunAt sql.NullString
	var createdAt string
	var enabled int
	err := row.Scan(&s.ID, &s.AgentID, &s.WorkspaceID, &s.CronExpr, &nextRunAt, &lastRunAt, &enabled, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get schedule: %w", err)
	}
	s.NextRunAt = scanTimePtr(nullToPtr(nextRunAt))
	s.LastRunAt = scanTimePtr(nullToPtr(lastRunAt))
	s.Enabled = enabled == 1
	s.CreatedAt = scanTime(createdAt)
	return &s, nil
}

// ── Skills ─────────────────────────────────────────────────────────────────

const skillCols = `id,workspace_id,name,description,installed_at`

func (d *DB) CreateSkill(s *Skill) error {
	_, err := d.Exec(`INSERT INTO skills(id,workspace_id,name,description,installed_at)
		VALUES(?,?,?,?,datetime('now'))`,
		s.ID, s.WorkspaceID, s.Name, s.Description)
	return err
}

func (d *DB) GetSkillByName(workspaceID, name string) (*Skill, error) {
	row := d.QueryRow(`SELECT `+skillCols+` FROM skills WHERE workspace_id=? AND name=?`, workspaceID, name)
	return scanSkill(row)
}

func (d *DB) GetSkill(id string) (*Skill, error) {
	row := d.QueryRow(`SELECT `+skillCols+` FROM skills WHERE id=?`, id)
	return scanSkill(row)
}

func (d *DB) ListSkills(workspaceID string) ([]*Skill, error) {
	rows, err := d.Query(`SELECT `+skillCols+` FROM skills WHERE workspace_id=? ORDER BY name`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var skills []*Skill
	for rows.Next() {
		s, err := scanSkill(rows)
		if err != nil {
			return nil, err
		}
		skills = append(skills, s)
	}
	return skills, rows.Err()
}

func (d *DB) DeleteSkill(id string) error {
	res, err := d.Exec(`DELETE FROM skills WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (d *DB) UpdateSkillDescription(id, description string) error {
	_, err := d.Exec(`UPDATE skills SET description=? WHERE id=?`, description, id)
	return err
}

// SetAgentSkills replaces all skill attachments for an agent atomically.
// skillNames are skill NAMES (not IDs) — core (embedded) skills are attached by
// name since they have no skills-table row. The DB is the single source of truth
// for an agent's skills; AGENT.md's "# Skills:" line is only the coder's
// declaration channel, parsed once at generation time.
func (d *DB) SetAgentSkills(agentID string, skillNames []string) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM agent_skills WHERE agent_id=?`, agentID); err != nil {
		return err
	}
	for _, name := range skillNames {
		if name == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO agent_skills(agent_id,skill_name) VALUES(?,?)`, agentID, name); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListAgentSkillNames returns the skill names attached to an agent (core + user),
// in insertion order. This is the authoritative skill list for the runner and the
// agent page.
func (d *DB) ListAgentSkillNames(agentID string) ([]string, error) {
	rows, err := d.Query(`SELECT skill_name FROM agent_skills WHERE agent_id=?`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

// DeleteAgentSkillsByName removes agent_skills rows matching a skill name for
// every agent owned by workspaceID — used when a user skill is deleted so attachments
// don't dangle. Core skills can't be deleted by the user.
func (d *DB) DeleteAgentSkillsByName(workspaceID, skillName string) error {
	_, err := d.Exec(`DELETE FROM agent_skills WHERE skill_name=? AND agent_id IN
		(SELECT id FROM agents WHERE workspace_id=?)`, skillName, workspaceID)
	return err
}

func scanSkill(s scanner) (*Skill, error) {
	var sk Skill
	var installedAt string
	err := s.Scan(&sk.ID, &sk.WorkspaceID, &sk.Name, &sk.Description, &installedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sk.InstalledAt = scanTime(installedAt)
	return &sk, nil
}

// ── Skill drafts ─────────────────────────────────────────────────────────────

// UpsertSkillDraft saves or overwrites the user's single in-progress skill-creator draft.
func (d *DB) UpsertSkillDraft(dr *SkillDraft) error {
	_, err := d.Exec(`INSERT OR REPLACE INTO skill_drafts
		(workspace_id, skill_name, state, history_json, pending_skill_md, pending_scripts_json, vetting_report, updated_at, expires_at)
		VALUES (?,?,?,?,?,?,?,datetime('now'),?)`,
		dr.WorkspaceID, dr.SkillName, dr.State, dr.HistoryJSON,
		dr.PendingSkillMD, dr.PendingScriptsJSON, dr.VettingReport,
		dr.ExpiresAt.UTC().Format("2006-01-02 15:04:05"))
	return err
}

// GetSkillDraft returns the user's draft if one exists and has not expired.
// Returns ErrNotFound when absent or expired.
func (d *DB) GetSkillDraft(workspaceID string) (*SkillDraft, error) {
	var dr SkillDraft
	var updatedAt, expiresAt string
	err := d.QueryRow(`SELECT workspace_id, skill_name, state, history_json, pending_skill_md, pending_scripts_json, vetting_report, updated_at, expires_at
		FROM skill_drafts WHERE workspace_id=? AND expires_at > datetime('now')`, workspaceID).
		Scan(&dr.WorkspaceID, &dr.SkillName, &dr.State, &dr.HistoryJSON,
			&dr.PendingSkillMD, &dr.PendingScriptsJSON, &dr.VettingReport, &updatedAt, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	dr.UpdatedAt = scanTime(updatedAt)
	dr.ExpiresAt = scanTime(expiresAt)
	return &dr, nil
}

// DeleteSkillDraft removes the user's draft.
func (d *DB) DeleteSkillDraft(workspaceID string) error {
	_, err := d.Exec(`DELETE FROM skill_drafts WHERE workspace_id=?`, workspaceID)
	return err
}

// ListExpiredSkillDrafts returns all skill drafts past their expiry (for the nightly GC).
func (d *DB) ListExpiredSkillDrafts() ([]*SkillDraft, error) {
	rows, err := d.Query(`SELECT workspace_id, skill_name, state, history_json, pending_skill_md, pending_scripts_json, vetting_report, updated_at, expires_at
		FROM skill_drafts WHERE expires_at <= datetime('now')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var drafts []*SkillDraft
	for rows.Next() {
		var dr SkillDraft
		var updatedAt, expiresAt string
		if err := rows.Scan(&dr.WorkspaceID, &dr.SkillName, &dr.State, &dr.HistoryJSON,
			&dr.PendingSkillMD, &dr.PendingScriptsJSON, &dr.VettingReport, &updatedAt, &expiresAt); err != nil {
			return nil, err
		}
		dr.UpdatedAt = scanTime(updatedAt)
		dr.ExpiresAt = scanTime(expiresAt)
		drafts = append(drafts, &dr)
	}
	return drafts, rows.Err()
}

// MCP server + tool helpers live in internal/db/mcp.go.

// ── Agent drafts ──────────────────────────────────────────────────────────────

// UpsertAgentDraft saves or overwrites the user's single in-progress design draft.
func (d *DB) UpsertAgentDraft(dr *AgentDraft) error {
	_, err := d.Exec(`INSERT OR REPLACE INTO agent_drafts
		(workspace_id, agent_id, agent_name, is_edit, state, history_json, pending_agent_md, pending_tools_json, pending_used_connections, pending_used_mcp_servers, updated_at, expires_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,datetime('now'),?)`,
		dr.WorkspaceID, dr.AgentID, dr.AgentName, boolToInt(dr.IsEdit), dr.State,
		dr.HistoryJSON, dr.PendingAgentMD, dr.PendingToolsJSON, dr.PendingUsedConnectionsJSON,
		dr.PendingUsedMCPServersJSON,
		dr.ExpiresAt.UTC().Format("2006-01-02 15:04:05"))
	return err
}

// GetAgentDraft returns the user's draft if one exists and has not expired.
// Returns ErrNotFound when absent or expired.
func (d *DB) GetAgentDraft(workspaceID string) (*AgentDraft, error) {
	var dr AgentDraft
	var isEdit int
	var updatedAt, expiresAt string
	err := d.QueryRow(`SELECT workspace_id, agent_id, agent_name, is_edit, state, history_json, pending_agent_md, pending_tools_json, pending_used_connections, pending_used_mcp_servers, updated_at, expires_at
		FROM agent_drafts WHERE workspace_id=? AND expires_at > datetime('now')`, workspaceID).
		Scan(&dr.WorkspaceID, &dr.AgentID, &dr.AgentName, &isEdit, &dr.State, &dr.HistoryJSON,
			&dr.PendingAgentMD, &dr.PendingToolsJSON, &dr.PendingUsedConnectionsJSON,
			&dr.PendingUsedMCPServersJSON, &updatedAt, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	dr.IsEdit = isEdit == 1
	dr.UpdatedAt = scanTime(updatedAt)
	dr.ExpiresAt = scanTime(expiresAt)
	return &dr, nil
}

// DeleteAgentDraft removes the user's draft.
func (d *DB) DeleteAgentDraft(workspaceID string) error {
	_, err := d.Exec(`DELETE FROM agent_drafts WHERE workspace_id=?`, workspaceID)
	return err
}

// ListExpiredAgentDrafts returns all drafts past their expiry (for the nightly GC).
func (d *DB) ListExpiredAgentDrafts() ([]*AgentDraft, error) {
	rows, err := d.Query(`SELECT workspace_id, agent_id, agent_name, is_edit, state, history_json, pending_agent_md, pending_tools_json, pending_used_connections, pending_used_mcp_servers, updated_at, expires_at
		FROM agent_drafts WHERE expires_at <= datetime('now')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var drafts []*AgentDraft
	for rows.Next() {
		var dr AgentDraft
		var isEdit int
		var updatedAt, expiresAt string
		if err := rows.Scan(&dr.WorkspaceID, &dr.AgentID, &dr.AgentName, &isEdit, &dr.State, &dr.HistoryJSON,
			&dr.PendingAgentMD, &dr.PendingToolsJSON, &dr.PendingUsedConnectionsJSON,
			&dr.PendingUsedMCPServersJSON, &updatedAt, &expiresAt); err != nil {
			return nil, err
		}
		dr.IsEdit = isEdit == 1
		dr.UpdatedAt = scanTime(updatedAt)
		dr.ExpiresAt = scanTime(expiresAt)
		drafts = append(drafts, &dr)
	}
	return drafts, rows.Err()
}
