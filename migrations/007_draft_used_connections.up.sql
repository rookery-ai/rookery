-- Persist the agent designer's build-used connection IDs on the draft row so
-- auto-bind survives a server restart / resumed "keep-as-is" draft (previously
-- only held in-memory on DesignSession.PendingUsedConnections).
ALTER TABLE agent_drafts ADD COLUMN pending_used_connections TEXT NOT NULL DEFAULT '';
