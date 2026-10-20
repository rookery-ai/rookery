-- A workspace is bound to its BOT, not to the person's chat account.
--
-- platform_identities carried UNIQUE(platform, platform_user_id), so one Telegram
-- account could be linked to exactly one workspace: messaging a second workspace's
-- bot was routed into the first workspace and answered "You're already linked!"
-- out of the FIRST bot. Dropping that constraint lets one account link to several
-- workspaces, each through its own bot. UNIQUE(workspace_id, platform) stays: a
-- workspace's bot still serves one chat (a DM, or one Telegram group).
--
-- SQLite cannot drop a table constraint in place, so the table is rebuilt.
-- Nothing references platform_identities, and statements run one at a time on a
-- pooled connection, so there is deliberately no BEGIN/COMMIT here.
--
-- One-way in practice: pre-021 code upserts with ON CONFLICT(platform,
-- platform_user_id), which SQLite rejects against this schema. Roll back by
-- restoring a database snapshot, not by running an older binary.
CREATE TABLE platform_identities_new (
    id               TEXT PRIMARY KEY,
    workspace_id     TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    platform         TEXT NOT NULL,
    platform_user_id TEXT NOT NULL,
    linked_at        TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(workspace_id, platform)
);

INSERT INTO platform_identities_new (id, workspace_id, platform, platform_user_id, linked_at)
    SELECT id, workspace_id, platform, platform_user_id, linked_at FROM platform_identities;

DROP TABLE platform_identities;

ALTER TABLE platform_identities_new RENAME TO platform_identities;

-- Inbound lookups are now (workspace_id, platform, platform_user_id), which the
-- UNIQUE(workspace_id, platform) index already serves. This one serves the
-- supergroup re-key, which knows only the platform and the old chat id.
CREATE INDEX IF NOT EXISTS idx_platform_identities_lookup ON platform_identities(platform, platform_user_id);
