-- Coder profiles: each profile defines a claude CLI binary + timeout.
-- Admin creates profiles; users are assigned one. NULL coder_id = system default.

CREATE TABLE IF NOT EXISTS coders (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    claude_bin  TEXT NOT NULL DEFAULT 'claude',
    timeout_s   INTEGER NOT NULL DEFAULT 120,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

ALTER TABLE users ADD COLUMN coder_id TEXT REFERENCES coders(id) ON DELETE SET NULL;
