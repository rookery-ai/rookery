package db

import (
	"database/sql"
	"errors"
	"fmt"
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

// ── Users ──────────────────────────────────────────────────────────────────

func (d *DB) CreateUser(u *User) error {
	_, err := d.Exec(`INSERT INTO users
		(id, username, password_hash, role, encrypted_master_password, secrets_salt,
		 needs_setup, must_change_password, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,datetime('now'),datetime('now'))`,
		u.ID, u.Username, u.PasswordHash, u.Role,
		u.EncryptedMasterPassword, u.SecretsSalt,
		boolToInt(u.NeedsSetup), boolToInt(u.MustChangePassword),
	)
	return err
}

func (d *DB) GetUserByID(id string) (*User, error) {
	row := d.QueryRow(`SELECT id,username,password_hash,role,encrypted_master_password,
		secrets_salt,needs_setup,must_change_password,created_at,updated_at,coder_id
		FROM users WHERE id=?`, id)
	return scanUser(row)
}

func (d *DB) GetUserByUsername(username string) (*User, error) {
	row := d.QueryRow(`SELECT id,username,password_hash,role,encrypted_master_password,
		secrets_salt,needs_setup,must_change_password,created_at,updated_at,coder_id
		FROM users WHERE username=?`, username)
	return scanUser(row)
}

func (d *DB) ListUsers() ([]*User, error) {
	rows, err := d.Query(`SELECT id,username,password_hash,role,encrypted_master_password,
		secrets_salt,needs_setup,must_change_password,created_at,updated_at,coder_id
		FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (d *DB) UpdateUserPassword(id, hash string) error {
	_, err := d.Exec(`UPDATE users SET password_hash=?, must_change_password=0, updated_at=datetime('now') WHERE id=?`, hash, id)
	return err
}

func (d *DB) UpdateUserSetup(id, encMasterPw, salt string) error {
	_, err := d.Exec(`UPDATE users SET encrypted_master_password=?, secrets_salt=?, needs_setup=0, updated_at=datetime('now') WHERE id=?`,
		encMasterPw, salt, id)
	return err
}

func (d *DB) UpdateUserMasterPassword(id, encMasterPw, salt string) error {
	_, err := d.Exec(`UPDATE users SET encrypted_master_password=?, secrets_salt=?, updated_at=datetime('now') WHERE id=?`,
		encMasterPw, salt, id)
	return err
}

func (d *DB) AdminExists() (bool, error) {
	var count int
	err := d.QueryRow(`SELECT COUNT(*) FROM users WHERE role='admin'`).Scan(&count)
	return count > 0, err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanUser(s scanner) (*User, error) {
	var u User
	var createdAt, updatedAt string
	var needsSetup, mustChange int
	err := s.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role,
		&u.EncryptedMasterPassword, &u.SecretsSalt,
		&needsSetup, &mustChange, &createdAt, &updatedAt, &u.CoderID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.NeedsSetup = needsSetup == 1
	u.MustChangePassword = mustChange == 1
	u.CreatedAt = scanTime(createdAt)
	u.UpdatedAt = scanTime(updatedAt)
	return &u, nil
}

// ── Secrets ────────────────────────────────────────────────────────────────

func (d *DB) UpsertSecret(s *Secret) error {
	_, err := d.Exec(`INSERT INTO secrets(id, user_id, name, ciphertext, nonce, created_at, updated_at)
		VALUES(?,?,?,?,?,datetime('now'),datetime('now'))
		ON CONFLICT(user_id, name) DO UPDATE SET
		  ciphertext=excluded.ciphertext,
		  nonce=excluded.nonce,
		  updated_at=datetime('now')`,
		s.ID, s.UserID, s.Name, s.Ciphertext, s.Nonce)
	return err
}

func (d *DB) GetSecret(userID, name string) (*Secret, error) {
	row := d.QueryRow(`SELECT id,user_id,name,ciphertext,nonce,created_at,updated_at FROM secrets WHERE user_id=? AND name=?`, userID, name)
	var s Secret
	var createdAt, updatedAt string
	err := row.Scan(&s.ID, &s.UserID, &s.Name, &s.Ciphertext, &s.Nonce, &createdAt, &updatedAt)
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

func (d *DB) ListSecretNames(userID string) ([]string, error) {
	rows, err := d.Query(`SELECT name FROM secrets WHERE user_id=? ORDER BY name`, userID)
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

func (d *DB) DeleteSecret(userID, name string) error {
	res, err := d.Exec(`DELETE FROM secrets WHERE user_id=? AND name=?`, userID, name)
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
	_, err := d.Exec(`INSERT INTO platform_connections(id, user_id, platform, encrypted_token, active, created_at, updated_at)
		VALUES(?,?,?,?,?,datetime('now'),datetime('now'))
		ON CONFLICT(user_id, platform) DO UPDATE SET
		  encrypted_token=excluded.encrypted_token,
		  active=excluded.active,
		  updated_at=datetime('now')`,
		c.ID, c.UserID, c.Platform, c.EncryptedToken, boolToInt(c.Active))
	return err
}

func (d *DB) GetPlatformConnection(userID, platform string) (*PlatformConnection, error) {
	row := d.QueryRow(`SELECT id,user_id,platform,encrypted_token,active,created_at,updated_at
		FROM platform_connections WHERE user_id=? AND platform=?`, userID, platform)
	return scanPlatformConnection(row)
}

func (d *DB) ListActivePlatformConnections() ([]*PlatformConnection, error) {
	rows, err := d.Query(`SELECT id,user_id,platform,encrypted_token,active,created_at,updated_at
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

func (d *DB) SetPlatformConnectionActive(userID, platform string, active bool) error {
	_, err := d.Exec(`UPDATE platform_connections SET active=?, updated_at=datetime('now') WHERE user_id=? AND platform=?`,
		boolToInt(active), userID, platform)
	return err
}

func (d *DB) DeletePlatformConnection(userID, platform string) error {
	_, err := d.Exec(`DELETE FROM platform_connections WHERE user_id=? AND platform=?`, userID, platform)
	return err
}

func (d *DB) ListUserPlatformConnections(userID string) ([]*PlatformConnection, error) {
	rows, err := d.Query(`SELECT id,user_id,platform,encrypted_token,active,created_at,updated_at
		FROM platform_connections WHERE user_id=?`, userID)
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
	err := s.Scan(&c.ID, &c.UserID, &c.Platform, &c.EncryptedToken, &active, &createdAt, &updatedAt)
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
	_, err := d.Exec(`INSERT INTO platform_identities(id, user_id, platform, platform_user_id, linked_at)
		VALUES(?,?,?,?,datetime('now'))
		ON CONFLICT(platform, platform_user_id) DO UPDATE SET
		  user_id=excluded.user_id`,
		i.ID, i.UserID, i.Platform, i.PlatformUserID)
	return err
}

func (d *DB) GetPlatformIdentity(platform, platformUserID string) (*PlatformIdentity, error) {
	row := d.QueryRow(`SELECT id,user_id,platform,platform_user_id,linked_at
		FROM platform_identities WHERE platform=? AND platform_user_id=?`, platform, platformUserID)
	var i PlatformIdentity
	var linkedAt string
	err := row.Scan(&i.ID, &i.UserID, &i.Platform, &i.PlatformUserID, &linkedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	i.LinkedAt = scanTime(linkedAt)
	return &i, nil
}

func (d *DB) ListPlatformIdentities(userID, platform string) ([]*PlatformIdentity, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if platform == "" {
		rows, err = d.Query(`SELECT id,user_id,platform,platform_user_id,linked_at
			FROM platform_identities WHERE user_id=?`, userID)
	} else {
		rows, err = d.Query(`SELECT id,user_id,platform,platform_user_id,linked_at
			FROM platform_identities WHERE user_id=? AND platform=?`, userID, platform)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*PlatformIdentity
	for rows.Next() {
		var i PlatformIdentity
		var linkedAt string
		if err := rows.Scan(&i.ID, &i.UserID, &i.Platform, &i.PlatformUserID, &linkedAt); err != nil {
			return nil, err
		}
		i.LinkedAt = scanTime(linkedAt)
		out = append(out, &i)
	}
	return out, rows.Err()
}

// HasPlatformIdentity returns true when the user has at least one linked
// platform (Telegram, etc.). Used to skip reminders and agent runs for
// users who have no way to receive output, preventing wasted API usage.
func (d *DB) HasPlatformIdentity(userID string) bool {
	var count int
	_ = d.QueryRow(`SELECT COUNT(*) FROM platform_identities WHERE user_id=?`, userID).Scan(&count)
	return count > 0
}

// ── Agents ─────────────────────────────────────────────────────────────────

func (d *DB) CreateAgent(a *Agent) error {
	_, err := d.Exec(`INSERT INTO agents(id,user_id,name,description,active,created_at,updated_at)
		VALUES(?,?,?,?,1,datetime('now'),datetime('now'))`,
		a.ID, a.UserID, a.Name, a.Description)
	return err
}

func (d *DB) GetAgent(id string) (*Agent, error) {
	row := d.QueryRow(`SELECT id,user_id,name,description,active,created_at,updated_at FROM agents WHERE id=?`, id)
	return scanAgent(row)
}

func (d *DB) GetAgentByName(userID, name string) (*Agent, error) {
	row := d.QueryRow(`SELECT id,user_id,name,description,active,created_at,updated_at FROM agents WHERE user_id=? AND name=?`, userID, name)
	return scanAgent(row)
}

func (d *DB) ListAgents(userID string) ([]*Agent, error) {
	rows, err := d.Query(`SELECT id,user_id,name,description,active,created_at,updated_at FROM agents WHERE user_id=? ORDER BY name`, userID)
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
	err := s.Scan(&a.ID, &a.UserID, &a.Name, &a.Description, &active, &createdAt, &updatedAt)
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
	_, err := d.Exec(`INSERT INTO agent_runs(id,agent_id,user_id,trigger,started_at)
		VALUES(?,?,?,?,datetime('now'))`,
		r.ID, r.AgentID, r.UserID, r.Trigger)
	return err
}

func (d *DB) FinishAgentRun(id string, exitCode int, stdout, stderr string) error {
	_, err := d.Exec(`UPDATE agent_runs SET exit_code=?, stdout=?, stderr=?, finished_at=datetime('now') WHERE id=?`,
		exitCode, stdout, stderr, id)
	return err
}

func (d *DB) ListAgentRuns(agentID string, limit int) ([]*AgentRun, error) {
	rows, err := d.Query(`SELECT id,agent_id,user_id,trigger,exit_code,stdout,stderr,started_at,finished_at
		FROM agent_runs WHERE agent_id=? ORDER BY started_at DESC LIMIT ?`, agentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []*AgentRun
	for rows.Next() {
		var r AgentRun
		var exitCode sql.NullInt64
		var startedAt string
		var finishedAt sql.NullString
		if err := rows.Scan(&r.ID, &r.AgentID, &r.UserID, &r.Trigger, &exitCode, &r.Stdout, &r.Stderr, &startedAt, &finishedAt); err != nil {
			return nil, err
		}
		if exitCode.Valid {
			v := int(exitCode.Int64)
			r.ExitCode = &v
		}
		r.StartedAt = scanTime(startedAt)
		if finishedAt.Valid {
			t := scanTime(finishedAt.String)
			r.FinishedAt = &t
		}
		runs = append(runs, &r)
	}
	return runs, rows.Err()
}

// ── Agent schedules ────────────────────────────────────────────────────────

func (d *DB) UpsertAgentSchedule(s *AgentSchedule) error {
	var nextRun *string
	if s.NextRunAt != nil {
		t := s.NextRunAt.UTC().Format("2006-01-02 15:04:05")
		nextRun = &t
	}
	_, err := d.Exec(`INSERT INTO agent_schedules(id,agent_id,user_id,cron_expr,next_run_at,enabled,created_at)
		VALUES(?,?,?,?,?,1,datetime('now'))
		ON CONFLICT(id) DO UPDATE SET cron_expr=excluded.cron_expr, next_run_at=excluded.next_run_at, enabled=excluded.enabled`,
		s.ID, s.AgentID, s.UserID, s.CronExpr, nextRun)
	return err
}

func (d *DB) ListDueSchedules(now time.Time) ([]*AgentSchedule, error) {
	rows, err := d.Query(`SELECT s.id,s.agent_id,s.user_id,s.cron_expr,s.next_run_at,s.last_run_at,s.enabled,s.created_at
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
		if err := rows.Scan(&s.ID, &s.AgentID, &s.UserID, &s.CronExpr, &nextRunAt, &lastRunAt, &enabled, &createdAt); err != nil {
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

// ── Chat sessions ──────────────────────────────────────────────────────────

func (d *DB) CreateChatSession(s *ChatSession) error {
	_, err := d.Exec(`INSERT INTO chat_sessions(id,user_id,agent_id,name,platform,active,created_at,last_seen)
		VALUES(?,?,?,?,?,1,datetime('now'),datetime('now'))`,
		s.ID, s.UserID, s.AgentID, s.Name, s.Platform)
	return err
}

func (d *DB) GetChatSession(id string) (*ChatSession, error) {
	row := d.QueryRow(`SELECT id,user_id,agent_id,name,platform,active,created_at,last_seen FROM chat_sessions WHERE id=?`, id)
	return scanChatSession(row)
}

func (d *DB) ListChatSessions(userID string) ([]*ChatSession, error) {
	rows, err := d.Query(`SELECT id,user_id,agent_id,name,platform,active,created_at,last_seen
		FROM chat_sessions WHERE user_id=? ORDER BY last_seen DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []*ChatSession
	for rows.Next() {
		s, err := scanChatSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

func (d *DB) TouchChatSession(id string) error {
	_, err := d.Exec(`UPDATE chat_sessions SET last_seen=datetime('now') WHERE id=?`, id)
	return err
}

func (d *DB) StopChatSession(id string) error {
	_, err := d.Exec(`UPDATE chat_sessions SET active=0 WHERE id=?`, id)
	return err
}

func (d *DB) DeleteChatSession(id string) error {
	_, err := d.Exec(`DELETE FROM chat_sessions WHERE id=?`, id)
	return err
}

func (d *DB) ListStaleSessions(before time.Time) ([]*ChatSession, error) {
	rows, err := d.Query(`SELECT id,user_id,agent_id,name,platform,active,created_at,last_seen
		FROM chat_sessions WHERE active=1 AND last_seen < ?`,
		before.UTC().Format("2006-01-02 15:04:05"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []*ChatSession
	for rows.Next() {
		s, err := scanChatSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

func scanChatSession(s scanner) (*ChatSession, error) {
	var cs ChatSession
	var agentID sql.NullString
	var active int
	var createdAt, lastSeen string
	err := s.Scan(&cs.ID, &cs.UserID, &agentID, &cs.Name, &cs.Platform, &active, &createdAt, &lastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if agentID.Valid {
		cs.AgentID = &agentID.String
	}
	cs.Active = active == 1
	cs.CreatedAt = scanTime(createdAt)
	cs.LastSeen = scanTime(lastSeen)
	return &cs, nil
}

// ── Chat messages ─────────────────────────────────────────────────────────

func (d *DB) AddChatMessage(sessionID, role, content string) error {
	_, err := d.Exec(`INSERT INTO chat_messages(session_id,role,content,created_at) VALUES(?,?,?,datetime('now'))`,
		sessionID, role, content)
	return err
}

func (d *DB) ListChatMessages(sessionID string) ([]ChatMessage, error) {
	rows, err := d.Query(`SELECT id,session_id,role,content,created_at FROM chat_messages WHERE session_id=? ORDER BY id ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChatMessage
	for rows.Next() {
		var m ChatMessage
		var createdAt string
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &createdAt); err != nil {
			return nil, err
		}
		m.CreatedAt = scanTime(createdAt)
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetActiveSessionForPlatform returns the most-recently-touched active session
// for the given user on the given platform, or nil if none exists.
func (d *DB) GetActiveSessionForPlatform(userID, platform string) (*ChatSession, error) {
	row := d.QueryRow(`SELECT id,user_id,agent_id,name,platform,active,created_at,last_seen
		FROM chat_sessions WHERE user_id=? AND platform=? AND active=1
		ORDER BY last_seen DESC LIMIT 1`, userID, platform)
	s, err := scanChatSession(row)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return s, err
}

// FindSessionByPrefix returns the first session whose ID starts with the given prefix.
func (d *DB) FindSessionByPrefix(userID, prefix string) (*ChatSession, error) {
	rows, err := d.Query(`SELECT id,user_id,agent_id,name,platform,active,created_at,last_seen
		FROM chat_sessions WHERE user_id=? AND id LIKE ? ORDER BY last_seen DESC LIMIT 1`,
		userID, prefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, ErrNotFound
	}
	return scanChatSession(rows)
}

// ResumeSession sets a session active again.
func (d *DB) ResumeSession(id string) error {
	_, err := d.Exec(`UPDATE chat_sessions SET active=1, last_seen=datetime('now') WHERE id=?`, id)
	return err
}

// ── Reminders ──────────────────────────────────────────────────────────────

func (d *DB) CreateReminder(r *Reminder) error {
	_, err := d.Exec(`INSERT INTO reminders(id,user_id,message,remind_at,recurrence,sent,created_at)
		VALUES(?,?,?,?,?,0,datetime('now'))`,
		r.ID, r.UserID, r.Message, r.RemindAt.UTC().Format("2006-01-02 15:04:05"), r.Recurrence)
	return err
}

func (d *DB) GetReminder(id string) (*Reminder, error) {
	row := d.QueryRow(`SELECT id,user_id,message,remind_at,recurrence,sent,created_at FROM reminders WHERE id=?`, id)
	return scanReminder(row)
}

func (d *DB) ListReminders(userID string) ([]*Reminder, error) {
	rows, err := d.Query(`SELECT id,user_id,message,remind_at,recurrence,sent,created_at
		FROM reminders WHERE user_id=? ORDER BY remind_at`, userID)
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
	rows, err := d.Query(`SELECT id,user_id,message,remind_at,recurrence,sent,created_at
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
	err := s.Scan(&r.ID, &r.UserID, &r.Message, &remindAt, &r.Recurrence, &sent, &createdAt)
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

// ── User permissions ───────────────────────────────────────────────────────

func (d *DB) GrantPermission(p *UserPermission) error {
	_, err := d.Exec(`INSERT OR IGNORE INTO user_permissions(id,user_id,permission,granted_by,granted_at)
		VALUES(?,?,?,?,datetime('now'))`,
		p.ID, p.UserID, p.Permission, p.GrantedBy)
	return err
}

func (d *DB) RevokePermission(userID, permission string) error {
	_, err := d.Exec(`DELETE FROM user_permissions WHERE user_id=? AND permission=?`, userID, permission)
	return err
}

func (d *DB) HasPermission(userID, permission string) (bool, error) {
	var count int
	err := d.QueryRow(`SELECT COUNT(*) FROM user_permissions WHERE user_id=? AND permission=?`, userID, permission).Scan(&count)
	return count > 0, err
}

func (d *DB) ListPermissions(userID string) ([]string, error) {
	rows, err := d.Query(`SELECT permission FROM user_permissions WHERE user_id=?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var perms []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		perms = append(perms, p)
	}
	return perms, rows.Err()
}

// ── Audit log ──────────────────────────────────────────────────────────────

func (d *DB) WriteAuditLog(a *AuditLog) error {
	_, err := d.Exec(`INSERT INTO audit_logs(id,user_id,action,target,detail,ip_address,created_at)
		VALUES(?,?,?,?,?,?,datetime('now'))`,
		a.ID, a.UserID, a.Action, a.Target, a.Detail, a.IPAddress)
	return err
}

func (d *DB) ListAuditLogs(limit int) ([]*AuditLog, error) {
	rows, err := d.Query(`SELECT id,user_id,action,target,detail,ip_address,created_at
		FROM audit_logs ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var logs []*AuditLog
	for rows.Next() {
		var a AuditLog
		var userID sql.NullString
		var createdAt string
		if err := rows.Scan(&a.ID, &userID, &a.Action, &a.Target, &a.Detail, &a.IPAddress, &createdAt); err != nil {
			return nil, err
		}
		if userID.Valid {
			a.UserID = &userID.String
		}
		a.CreatedAt = scanTime(createdAt)
		logs = append(logs, &a)
	}
	return logs, rows.Err()
}

// ── User settings ──────────────────────────────────────────────────────────

func (d *DB) GetSetting(userID, key string) (string, error) {
	var value string
	err := d.QueryRow(`SELECT value FROM user_settings WHERE user_id=? AND key=?`, userID, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return value, err
}

func (d *DB) SetSetting(userID, key, value string) error {
	_, err := d.Exec(`INSERT INTO user_settings(id,user_id,key,value,updated_at)
		VALUES(lower(hex(randomblob(16))),?,?,?,datetime('now'))
		ON CONFLICT(user_id,key) DO UPDATE SET value=excluded.value, updated_at=datetime('now')`,
		userID, key, value)
	return err
}

// SecretExists reports whether the user has a secret with the given name.
// It does NOT decrypt the value — safe to call without a master password.
func (d *DB) SecretExists(userID, name string) (bool, error) {
	var n int
	err := d.QueryRow(`SELECT COUNT(*) FROM secrets WHERE user_id=? AND name=?`, userID, name).Scan(&n)
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

// CountUsers returns total user count (used by admin dashboard).
func (d *DB) CountUsers() (int, error) {
	var count int
	err := d.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count)
	return count, err
}

// CountAgents returns total agent count optionally scoped to a user.
func (d *DB) CountAgents(userID string) (int, error) {
	var count int
	var err error
	if userID == "" {
		err = d.QueryRow(`SELECT COUNT(*) FROM agents`).Scan(&count)
	} else {
		err = d.QueryRow(`SELECT COUNT(*) FROM agents WHERE user_id=?`, userID).Scan(&count)
	}
	return count, err
}

// AgentRunWithName embeds AgentRun with the agent name for display purposes.
type AgentRunWithName struct {
	AgentRun
	AgentName string
}

// RecentAgentRunsWithNames returns the N most recent runs with the agent name joined in.
func (d *DB) RecentAgentRunsWithNames(userID string, limit int) ([]*AgentRunWithName, error) {
	rows, err := d.Query(`SELECT r.id,r.agent_id,r.user_id,r.trigger,r.exit_code,r.stdout,r.stderr,r.started_at,r.finished_at, COALESCE(a.name,'')
		FROM agent_runs r LEFT JOIN agents a ON a.id=r.agent_id
		WHERE r.user_id=? ORDER BY r.started_at DESC LIMIT ?`, userID, limit)
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
		if err := rows.Scan(&rn.ID, &rn.AgentID, &rn.UserID, &rn.Trigger, &exitCode, &rn.Stdout, &rn.Stderr, &startedAt, &finishedAt, &rn.AgentName); err != nil {
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
func (d *DB) RecentAgentRuns(userID string, limit int) ([]*AgentRun, error) {
	rows, err := d.Query(`SELECT r.id,r.agent_id,r.user_id,r.trigger,r.exit_code,r.stdout,r.stderr,r.started_at,r.finished_at
		FROM agent_runs r WHERE r.user_id=? ORDER BY r.started_at DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []*AgentRun
	for rows.Next() {
		var r AgentRun
		var exitCode sql.NullInt64
		var startedAt string
		var finishedAt sql.NullString
		if err := rows.Scan(&r.ID, &r.AgentID, &r.UserID, &r.Trigger, &exitCode, &r.Stdout, &r.Stderr, &startedAt, &finishedAt); err != nil {
			return nil, err
		}
		if exitCode.Valid {
			v := int(exitCode.Int64)
			r.ExitCode = &v
		}
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

	row := d.QueryRow(`SELECT id,agent_id,user_id,cron_expr,next_run_at,last_run_at,enabled,created_at
		FROM agent_schedules WHERE agent_id=?`, agentID)
	var s AgentSchedule
	var nextRunAt, lastRunAt sql.NullString
	var createdAt string
	var enabled int
	err = row.Scan(&s.ID, &s.AgentID, &s.UserID, &s.CronExpr, &nextRunAt, &lastRunAt, &enabled, &createdAt)
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
	row := d.QueryRow(`SELECT id,agent_id,user_id,cron_expr,next_run_at,last_run_at,enabled,created_at
		FROM agent_schedules WHERE agent_id=?`, agentID)
	var s AgentSchedule
	var nextRunAt, lastRunAt sql.NullString
	var createdAt string
	var enabled int
	err := row.Scan(&s.ID, &s.AgentID, &s.UserID, &s.CronExpr, &nextRunAt, &lastRunAt, &enabled, &createdAt)
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

// ── Coders ─────────────────────────────────────────────────────────────────

func (d *DB) CreateCoder(c *Coder) error {
	_, err := d.Exec(`INSERT INTO coders(id,name,description,claude_bin,timeout_s,backend_type,created_at,updated_at)
		VALUES(?,?,?,?,?,?,datetime('now'),datetime('now'))`,
		c.ID, c.Name, c.Description, c.ClaudeBin, c.TimeoutS, c.BackendType)
	return err
}

func (d *DB) GetCoder(id string) (*Coder, error) {
	row := d.QueryRow(`SELECT id,name,description,claude_bin,timeout_s,backend_type,created_at,updated_at
		FROM coders WHERE id=?`, id)
	return scanCoder(row)
}

func (d *DB) ListCoders() ([]*Coder, error) {
	rows, err := d.Query(`SELECT id,name,description,claude_bin,timeout_s,backend_type,created_at,updated_at
		FROM coders ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Coder
	for rows.Next() {
		c, err := scanCoder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (d *DB) UpdateCoder(c *Coder) error {
	_, err := d.Exec(`UPDATE coders SET name=?,description=?,claude_bin=?,timeout_s=?,backend_type=?,updated_at=datetime('now') WHERE id=?`,
		c.Name, c.Description, c.ClaudeBin, c.TimeoutS, c.BackendType, c.ID)
	return err
}

func (d *DB) DeleteCoder(id string) error {
	_, err := d.Exec(`DELETE FROM coders WHERE id=?`, id)
	return err
}

func (d *DB) AssignUserCoder(userID, coderID string) error {
	_, err := d.Exec(`UPDATE users SET coder_id=?,updated_at=datetime('now') WHERE id=?`, coderID, userID)
	return err
}

func (d *DB) UnassignUserCoder(userID string) error {
	_, err := d.Exec(`UPDATE users SET coder_id=NULL,updated_at=datetime('now') WHERE id=?`, userID)
	return err
}

// GetUserCoder returns the Coder assigned to a user, or (nil, nil) if none assigned.
func (d *DB) GetUserCoder(userID string) (*Coder, error) {
	row := d.QueryRow(`SELECT c.id,c.name,c.description,c.claude_bin,c.timeout_s,c.backend_type,c.created_at,c.updated_at
		FROM coders c JOIN users u ON u.coder_id=c.id WHERE u.id=?`, userID)
	c, err := scanCoder(row)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return c, err
}

func (d *DB) CountCoders() (int, error) {
	var n int
	err := d.QueryRow(`SELECT COUNT(*) FROM coders`).Scan(&n)
	return n, err
}

func scanCoder(s scanner) (*Coder, error) {
	var c Coder
	var createdAt, updatedAt string
	err := s.Scan(&c.ID, &c.Name, &c.Description, &c.ClaudeBin, &c.TimeoutS, &c.BackendType, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.CreatedAt = scanTime(createdAt)
	c.UpdatedAt = scanTime(updatedAt)
	return &c, nil
}

// ── MCP servers ────────────────────────────────────────────────────────────

// ── Skills ─────────────────────────────────────────────────────────────────

func (d *DB) CreateSkill(s *Skill) error {
	_, err := d.Exec(`INSERT INTO skills(id,user_id,name,description,installed_at)
		VALUES(?,?,?,?,datetime('now'))`,
		s.ID, s.UserID, s.Name, s.Description)
	return err
}

func (d *DB) GetSkillByName(userID, name string) (*Skill, error) {
	row := d.QueryRow(`SELECT id,user_id,name,description,installed_at FROM skills WHERE user_id=? AND name=?`, userID, name)
	return scanSkill(row)
}

func (d *DB) GetSkill(id string) (*Skill, error) {
	row := d.QueryRow(`SELECT id,user_id,name,description,installed_at FROM skills WHERE id=?`, id)
	return scanSkill(row)
}

func (d *DB) ListSkills(userID string) ([]*Skill, error) {
	rows, err := d.Query(`SELECT id,user_id,name,description,installed_at FROM skills WHERE user_id=? ORDER BY name`, userID)
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

// SetAgentSkills replaces all skill associations for an agent atomically.
func (d *DB) SetAgentSkills(agentID string, skillIDs []string) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM agent_skills WHERE agent_id=?`, agentID); err != nil {
		return err
	}
	for _, sid := range skillIDs {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO agent_skills(agent_id,skill_id) VALUES(?,?)`, agentID, sid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListSkillsForAgent returns skills declared by an agent.
func (d *DB) ListSkillsForAgent(agentID string) ([]*Skill, error) {
	rows, err := d.Query(`SELECT s.id,s.user_id,s.name,s.description,s.installed_at
		FROM skills s JOIN agent_skills ags ON ags.skill_id=s.id WHERE ags.agent_id=?`, agentID)
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

func scanSkill(s scanner) (*Skill, error) {
	var sk Skill
	var installedAt string
	err := s.Scan(&sk.ID, &sk.UserID, &sk.Name, &sk.Description, &installedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sk.InstalledAt = scanTime(installedAt)
	return &sk, nil
}

// ── MCP servers ────────────────────────────────────────────────────────────

func (d *DB) ListMCPServers(userID string) ([]*MCPServer, error) {
	rows, err := d.Query(`SELECT id,user_id,name,url,enabled,created_at,updated_at FROM mcp_servers WHERE user_id=? ORDER BY name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var servers []*MCPServer
	for rows.Next() {
		var s MCPServer
		var enabled int
		var createdAt, updatedAt string
		if err := rows.Scan(&s.ID, &s.UserID, &s.Name, &s.URL, &enabled, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		s.Enabled = enabled == 1
		s.CreatedAt = scanTime(createdAt)
		s.UpdatedAt = scanTime(updatedAt)
		servers = append(servers, &s)
	}
	return servers, rows.Err()
}

// ── Agent drafts ──────────────────────────────────────────────────────────────

// UpsertAgentDraft saves or overwrites the user's single in-progress design draft.
func (d *DB) UpsertAgentDraft(dr *AgentDraft) error {
	_, err := d.Exec(`INSERT OR REPLACE INTO agent_drafts
		(user_id, agent_id, agent_name, is_edit, state, history_json, pending_agent_md, pending_tools_json, updated_at, expires_at)
		VALUES (?,?,?,?,?,?,?,?,datetime('now'),?)`,
		dr.UserID, dr.AgentID, dr.AgentName, boolToInt(dr.IsEdit), dr.State,
		dr.HistoryJSON, dr.PendingAgentMD, dr.PendingToolsJSON,
		dr.ExpiresAt.UTC().Format("2006-01-02 15:04:05"))
	return err
}

// GetAgentDraft returns the user's draft if one exists and has not expired.
// Returns ErrNotFound when absent or expired.
func (d *DB) GetAgentDraft(userID string) (*AgentDraft, error) {
	var dr AgentDraft
	var isEdit int
	var updatedAt, expiresAt string
	err := d.QueryRow(`SELECT user_id, agent_id, agent_name, is_edit, state, history_json, pending_agent_md, pending_tools_json, updated_at, expires_at
		FROM agent_drafts WHERE user_id=? AND expires_at > datetime('now')`, userID).
		Scan(&dr.UserID, &dr.AgentID, &dr.AgentName, &isEdit, &dr.State, &dr.HistoryJSON,
			&dr.PendingAgentMD, &dr.PendingToolsJSON, &updatedAt, &expiresAt)
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
func (d *DB) DeleteAgentDraft(userID string) error {
	_, err := d.Exec(`DELETE FROM agent_drafts WHERE user_id=?`, userID)
	return err
}

// ListExpiredAgentDrafts returns all drafts past their expiry (for the nightly GC).
func (d *DB) ListExpiredAgentDrafts() ([]*AgentDraft, error) {
	rows, err := d.Query(`SELECT user_id, agent_id, agent_name, is_edit, state, history_json, pending_agent_md, pending_tools_json, updated_at, expires_at
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
		if err := rows.Scan(&dr.UserID, &dr.AgentID, &dr.AgentName, &isEdit, &dr.State, &dr.HistoryJSON,
			&dr.PendingAgentMD, &dr.PendingToolsJSON, &updatedAt, &expiresAt); err != nil {
			return nil, err
		}
		dr.IsEdit = isEdit == 1
		dr.UpdatedAt = scanTime(updatedAt)
		dr.ExpiresAt = scanTime(expiresAt)
		drafts = append(drafts, &dr)
	}
	return drafts, rows.Err()
}
