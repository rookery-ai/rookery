-- SQLite before 3.35 cannot DROP COLUMN, and modernc.org/sqlite is new enough that it
-- can — but dropping loses every user's gate setting silently. Rebuilding the table is
-- the honest inverse.
CREATE TABLE agent_connections_old (
    agent_id      TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    connection_id TEXT NOT NULL REFERENCES service_connections(id) ON DELETE CASCADE,
    PRIMARY KEY (agent_id, connection_id)
);
INSERT INTO agent_connections_old (agent_id, connection_id)
    SELECT agent_id, connection_id FROM agent_connections;
DROP TABLE agent_connections;
ALTER TABLE agent_connections_old RENAME TO agent_connections;
