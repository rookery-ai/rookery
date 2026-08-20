-- Sweep rows orphaned by the per-connection foreign-key bug fixed in #214
-- (10926d1, 2026-08-17). busy_timeout and foreign_keys are per-CONNECTION
-- settings and database/sql is a connection POOL, so before that fix a
-- `DELETE FROM agents` cascaded only when it happened to run on a connection
-- that had foreign_keys on. Every orphan observed predates the fix; the
-- cascade is correct now, which is why this is a one-time sweep and NOT a
-- change to the delete path.
--
-- Two policies, chosen per table by what its foreign key already declares.

-- CASCADE tables: the schema already says these rows die with their agent, so
-- deleting them is finishing a job the database meant to do. agent_connections
-- is the one with a security dimension — a binding grants an agent live
-- credentials, and a binding to an agent that no longer exists is a grant
-- nobody can see or revoke through the UI.
DELETE FROM agent_runs        WHERE agent_id NOT IN (SELECT id FROM agents);
DELETE FROM agent_skills      WHERE agent_id NOT IN (SELECT id FROM agents);
DELETE FROM agent_connections WHERE agent_id NOT IN (SELECT id FROM agents);
DELETE FROM agent_schedules   WHERE agent_id NOT IN (SELECT id FROM agents);
DELETE FROM agent_mcp_servers WHERE agent_id NOT IN (SELECT id FROM agents);

-- SET NULL tables: the ROW is preserved and only the dangling id goes, which is
-- exactly what the foreign key would have done had it been enforced.
--
-- inbox_messages carries a denormalized agent_name whose own schema comment
-- reads "survives agent delete", and it renders correctly today, so the row is
-- real notification history — a delivery record, not a projection of the agent.
-- Deleting it to tidy up a dangling id would destroy working history to fix a
-- problem it does not have. Nulling the id is not cosmetic either: Home
-- deep-links each inbox card to its source agent, so a dangling id is a link to
-- a deleted agent's page.
UPDATE inbox_messages  SET agent_id = NULL WHERE agent_id IS NOT NULL AND agent_id NOT IN (SELECT id FROM agents);
UPDATE pending_actions SET agent_id = NULL WHERE agent_id IS NOT NULL AND agent_id NOT IN (SELECT id FROM agents);
UPDATE chats           SET agent_id = NULL WHERE agent_id IS NOT NULL AND agent_id NOT IN (SELECT id FROM agents);
