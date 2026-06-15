ALTER TABLE agents ADD COLUMN type TEXT NOT NULL DEFAULT 'python';

CREATE TABLE IF NOT EXISTS skills (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    installed_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(user_id, name)
);

CREATE TABLE IF NOT EXISTS agent_skills (
    agent_id  TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    skill_id  TEXT NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    PRIMARY KEY (agent_id, skill_id)
);

CREATE INDEX IF NOT EXISTS idx_skills_user ON skills(user_id);
CREATE INDEX IF NOT EXISTS idx_agent_skills_agent ON agent_skills(agent_id);
