-- Per-binding approval mode for irreversible public writes.
--
-- agent_connections already means exactly "this agent, this account", which is the
-- right granularity: the same agent can post autonomously to a personal Bluesky while
-- requiring approval on a company LinkedIn Page. A per-agent flag would be simpler and
-- could not express that.
--
-- 'auto' is the default so every existing binding keeps today's behaviour — the gate is
-- opt-in and no agent starts waiting on approval because of this migration.
ALTER TABLE agent_connections ADD COLUMN approval_mode TEXT NOT NULL DEFAULT 'auto';
