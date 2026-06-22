-- Agent design drafts: persist in-progress agent creation/edit sessions so a
-- page reload, browser close, or server restart doesn't lose the conversation
-- (and, for StateVerifying, the generated agent content). One draft per user.
CREATE TABLE IF NOT EXISTS agent_drafts (
    user_id            TEXT PRIMARY KEY,
    agent_id           TEXT,
    agent_name         TEXT NOT NULL,
    is_edit            INTEGER NOT NULL DEFAULT 0,
    state              TEXT NOT NULL,               -- "designing" or "verifying"
    history_json       TEXT NOT NULL DEFAULT '[]',
    pending_agent_md   TEXT NOT NULL DEFAULT '',
    pending_tools_json TEXT NOT NULL DEFAULT '{}',
    updated_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at         DATETIME NOT NULL,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);