-- Self-managed OAuth connector layer. Per-workspace OAuth app credentials and one
-- row per connected account (multi-account = multiple rows). All secret columns are
-- AES-256-GCM encrypted under the system key so the headless refresh loop and cron
-- runs decrypt without a master password.
CREATE TABLE IF NOT EXISTS service_provider_configs (
    id                      TEXT PRIMARY KEY,
    workspace_id            TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    provider                TEXT NOT NULL,
    encrypted_client_id     TEXT NOT NULL,
    encrypted_client_secret TEXT NOT NULL,
    created_at              TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at              TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(workspace_id, provider)
);

CREATE TABLE IF NOT EXISTS service_connections (
    id                       TEXT PRIMARY KEY,
    workspace_id             TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    provider                 TEXT NOT NULL,
    account_label            TEXT NOT NULL,
    account_identity         TEXT NOT NULL DEFAULT '',
    scopes                   TEXT NOT NULL DEFAULT '',
    encrypted_access_token   TEXT NOT NULL DEFAULT '',
    encrypted_refresh_token  TEXT NOT NULL DEFAULT '',
    expires_at               TEXT NOT NULL DEFAULT '',
    status                   TEXT NOT NULL DEFAULT 'ACTIVE',
    created_at               TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at               TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(workspace_id, provider, account_label)
);
CREATE INDEX IF NOT EXISTS idx_svc_conn_ws ON service_connections(workspace_id);
CREATE INDEX IF NOT EXISTS idx_svc_conn_expiry ON service_connections(status, expires_at);

CREATE TABLE IF NOT EXISTS agent_connections (
    agent_id      TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    connection_id TEXT NOT NULL REFERENCES service_connections(id) ON DELETE CASCADE,
    PRIMARY KEY (agent_id, connection_id)
);
