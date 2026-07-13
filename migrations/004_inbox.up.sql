-- Inbox: cross-agent notification feed. Each row is one delivered notification
-- (an agent run's actual output/error message, or a fired reminder), captured at
-- the delivery hook so the body is exactly what the user was sent. The vault
-- inbox/ folder mirrors these as markdown notes (Reflector).
CREATE TABLE IF NOT EXISTS inbox_messages (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    source       TEXT NOT NULL,                 -- 'agent_run' | 'reminder'
    agent_id     TEXT REFERENCES agents(id) ON DELETE SET NULL, -- nullable (reminders)
    agent_name   TEXT NOT NULL DEFAULT '',      -- denormalized; survives agent delete
    ref_id       TEXT NOT NULL DEFAULT '',      -- run_id or reminder_id
    trigger      TEXT NOT NULL DEFAULT '',      -- cron|manual|chat ('' for reminders)
    body         TEXT NOT NULL,                 -- the actual delivered notification
    status       TEXT NOT NULL DEFAULT 'ok',    -- 'ok' | 'error'
    read_at      TEXT,                          -- NULL = unread
    created_at   TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_inbox_ws_created ON inbox_messages(workspace_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_inbox_ws_unread  ON inbox_messages(workspace_id, read_at);