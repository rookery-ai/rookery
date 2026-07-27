-- Parked connector calls awaiting the owner's approval.
--
-- When a public_write action fires under approval_mode='approve', Execute does NOT
-- call the provider. It writes the resolved call here and hands the coder a queue
-- ticket; the run finishes normally. The owner approves via /approve <id> in chat or
-- the web inbox, and a worker performs the real call then.
--
-- Storing ARGS (not a rendered HTTP request) is deliberate: a rendered request pins a
-- bearer token and an expiry at park time, and approval can arrive hours later. Re-
-- rendering at approval time goes through the same Execute path with a freshly
-- refreshed token.
CREATE TABLE IF NOT EXISTS pending_actions (
    id            TEXT PRIMARY KEY,
    workspace_id  TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    -- Nullable: chat has no agent, and an agent deleted while a call is parked must
    -- not cascade the parked row away — the owner still deserves to resolve it.
    agent_id      TEXT REFERENCES agents(id) ON DELETE SET NULL,
    agent_name    TEXT NOT NULL DEFAULT '',   -- denormalized; survives agent delete
    connection_id TEXT NOT NULL REFERENCES service_connections(id) ON DELETE CASCADE,
    provider      TEXT NOT NULL,
    action        TEXT NOT NULL,
    args_json     TEXT NOT NULL,              -- the typed args, re-rendered on approval
    summary       TEXT NOT NULL DEFAULT '',   -- human-readable preview for the prompt
    status        TEXT NOT NULL DEFAULT 'pending', -- pending|approved|rejected|failed|expired
    result_json   TEXT NOT NULL DEFAULT '',   -- provider response after a successful send
    error         TEXT NOT NULL DEFAULT '',   -- failure detail when status='failed'
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    resolved_at   TEXT
);
CREATE INDEX IF NOT EXISTS idx_pending_ws_status ON pending_actions(workspace_id, status, created_at DESC);
