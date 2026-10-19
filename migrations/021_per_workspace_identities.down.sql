-- Restores the global UNIQUE(platform, platform_user_id). An account linked to
-- several workspaces keeps only its EARLIEST link; the rest are dropped, because
-- the old schema cannot represent them.
CREATE TABLE platform_identities_old (
    id               TEXT PRIMARY KEY,
    workspace_id     TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    platform         TEXT NOT NULL,
    platform_user_id TEXT NOT NULL,
    linked_at        TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(workspace_id, platform),
    UNIQUE(platform, platform_user_id)
);

INSERT OR IGNORE INTO platform_identities_old (id, workspace_id, platform, platform_user_id, linked_at)
    SELECT id, workspace_id, platform, platform_user_id, linked_at FROM platform_identities
    ORDER BY linked_at, id;

DROP TABLE platform_identities;

ALTER TABLE platform_identities_old RENAME TO platform_identities;

CREATE INDEX IF NOT EXISTS idx_platform_identities_lookup ON platform_identities(platform, platform_user_id);
