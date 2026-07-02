PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;

-- Single-owner, multi-workspace model.
--
-- The person who installs the platform is the sole OWNER (one row in `owner`).
-- The owner logs in, then enters a WORKSPACE. Each workspace is a fully isolated
-- tenant (own vault, home, secrets, agents, connector, coder config). Workspaces
-- have no login of their own — the owner enters them with the workspace master
-- password. All tenant-scoped tables key off workspace_id.

CREATE TABLE IF NOT EXISTS owner (
    id                   TEXT PRIMARY KEY,
    username             TEXT UNIQUE NOT NULL,
    password_hash        TEXT NOT NULL,
    must_change_password INTEGER NOT NULL DEFAULT 1,
    created_at           TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at           TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS workspaces (
    id                        TEXT PRIMARY KEY,
    name                      TEXT UNIQUE NOT NULL,
    about                     TEXT NOT NULL DEFAULT '',
    encrypted_master_password TEXT NOT NULL DEFAULT '',
    secrets_salt              TEXT NOT NULL DEFAULT '',
    -- Inlined coder config (moved from the old admin-level coders pool).
    -- coder_kind: 'local' = a coder CLI binary installed on the host (now);
    --             'api'   = direct LLM provider API call (reserved for future work).
    coder_kind                TEXT NOT NULL DEFAULT 'local' CHECK (coder_kind IN ('local','api')),
    coder_bin                 TEXT NOT NULL DEFAULT '',
    coder_timeout_s           INTEGER NOT NULL DEFAULT 0,
    coder_backend_type        TEXT NOT NULL DEFAULT '',
    -- Reserved for the future direct-API coder path (not yet implemented).
    coder_provider            TEXT NOT NULL DEFAULT '',
    coder_model               TEXT NOT NULL DEFAULT '',
    coder_api_key_secret      TEXT NOT NULL DEFAULT '',
    needs_setup               INTEGER NOT NULL DEFAULT 1,
    created_at                TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at                TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS workspace_permissions (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    permission   TEXT NOT NULL,
    granted_by   TEXT NOT NULL DEFAULT '',
    granted_at   TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(workspace_id, permission)
);

CREATE TABLE IF NOT EXISTS platform_connections (
    id              TEXT PRIMARY KEY,
    workspace_id    TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    platform        TEXT NOT NULL,
    encrypted_token TEXT NOT NULL,
    active          INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(workspace_id, platform)
);

CREATE TABLE IF NOT EXISTS platform_identities (
    id               TEXT PRIMARY KEY,
    workspace_id     TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    platform         TEXT NOT NULL,
    platform_user_id TEXT NOT NULL,
    linked_at        TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(workspace_id, platform),
    UNIQUE(platform, platform_user_id)
);

CREATE TABLE IF NOT EXISTS agents (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    active       INTEGER NOT NULL DEFAULT 1,
    created_at   TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at   TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(workspace_id, name)
);

CREATE TABLE IF NOT EXISTS agent_schedules (
    id           TEXT PRIMARY KEY,
    agent_id     TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    cron_expr    TEXT NOT NULL,
    next_run_at  TEXT,
    last_run_at  TEXT,
    enabled      INTEGER NOT NULL DEFAULT 1,
    created_at   TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS agent_runs (
    id           TEXT PRIMARY KEY,
    agent_id     TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    trigger      TEXT NOT NULL,
    exit_code    INTEGER,
    stdout       TEXT,
    stderr       TEXT,
    started_at   TEXT NOT NULL DEFAULT (datetime('now')),
    finished_at  TEXT
);

CREATE TABLE IF NOT EXISTS secrets (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    ciphertext   TEXT NOT NULL,
    nonce        TEXT NOT NULL,
    created_at   TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at   TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(workspace_id, name)
);

CREATE TABLE IF NOT EXISTS chats (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    agent_id     TEXT REFERENCES agents(id) ON DELETE SET NULL,
    name         TEXT NOT NULL DEFAULT '',
    platform     TEXT NOT NULL DEFAULT 'web',
    active       INTEGER NOT NULL DEFAULT 1,
    created_at   TEXT NOT NULL DEFAULT (datetime('now')),
    last_seen    TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS chat_messages (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id    TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    role       TEXT NOT NULL,
    content    TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS reminders (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    message      TEXT NOT NULL,
    remind_at    TEXT NOT NULL,
    recurrence   TEXT NOT NULL DEFAULT '',
    sent         INTEGER NOT NULL DEFAULT 0,
    created_at   TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS workspace_settings (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    key          TEXT NOT NULL,
    value        TEXT NOT NULL,
    updated_at   TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(workspace_id, key)
);

CREATE TABLE IF NOT EXISTS mcp_servers (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    url          TEXT NOT NULL,
    enabled      INTEGER NOT NULL DEFAULT 1,
    created_at   TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at   TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(workspace_id, name)
);

CREATE TABLE IF NOT EXISTS skills (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    installed_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(workspace_id, name)
);

CREATE TABLE IF NOT EXISTS agent_skills (
    agent_id   TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    skill_name TEXT NOT NULL,
    PRIMARY KEY (agent_id, skill_name)
);

CREATE TABLE IF NOT EXISTS agent_drafts (
    workspace_id       TEXT PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
    agent_id           TEXT,
    agent_name         TEXT NOT NULL,
    is_edit            INTEGER NOT NULL DEFAULT 0,
    state              TEXT NOT NULL,
    history_json       TEXT NOT NULL DEFAULT '[]',
    pending_agent_md   TEXT NOT NULL DEFAULT '',
    pending_tools_json TEXT NOT NULL DEFAULT '{}',
    updated_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at         DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS skill_drafts (
    workspace_id         TEXT PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
    skill_name           TEXT NOT NULL,
    state                TEXT NOT NULL,
    history_json         TEXT NOT NULL DEFAULT '[]',
    pending_skill_md     TEXT NOT NULL DEFAULT '',
    pending_scripts_json TEXT NOT NULL DEFAULT '{}',
    vetting_report       TEXT NOT NULL DEFAULT '',
    updated_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at           DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_logs (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT REFERENCES workspaces(id) ON DELETE SET NULL,
    action       TEXT NOT NULL,
    target       TEXT NOT NULL DEFAULT '',
    detail       TEXT NOT NULL DEFAULT '',
    ip_address   TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Owner/system-level settings (not tenant-scoped). Keyed by name; no workspace FK.
CREATE TABLE IF NOT EXISTS system_settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS schema_migrations (
    name       TEXT PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Indexes for common query patterns
CREATE INDEX IF NOT EXISTS idx_platform_connections_ws ON platform_connections(workspace_id);
CREATE INDEX IF NOT EXISTS idx_platform_identities_lookup ON platform_identities(platform, platform_user_id);
CREATE INDEX IF NOT EXISTS idx_agents_ws ON agents(workspace_id);
CREATE INDEX IF NOT EXISTS idx_agent_schedules_next_run ON agent_schedules(next_run_at, enabled);
CREATE INDEX IF NOT EXISTS idx_agent_runs_agent ON agent_runs(agent_id);
CREATE INDEX IF NOT EXISTS idx_secrets_ws ON secrets(workspace_id);
CREATE INDEX IF NOT EXISTS idx_chats_ws ON chats(workspace_id, active);
CREATE INDEX IF NOT EXISTS idx_reminders_fire ON reminders(remind_at, sent);
CREATE INDEX IF NOT EXISTS idx_skills_ws ON skills(workspace_id);
CREATE INDEX IF NOT EXISTS idx_agent_skills_agent ON agent_skills(agent_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_ws ON audit_logs(workspace_id, created_at);
