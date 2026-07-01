-- 010: agent_skills keyed by skill NAME (not skill_id).
--
-- Core (embedded) skills have no row in the `skills` table, so the old
-- skill_id FK could never represent them. Switching to skill_name lets a single
-- table hold both core and user skill attachments, and makes the DB — not
-- AGENT.md / agent.json — the source of truth for an agent's skills. The
-- designer now persists the coder's declared skills here directly; the runner
-- and the agent page read them from here.
--
-- Backfill existing user-skill attachments by resolving the old skill_id → name,
-- keeping only rows whose agent still exists (some legacy rows reference
-- already-deleted agents and would otherwise violate the new FK).

CREATE TABLE IF NOT EXISTS agent_skills_new (
    agent_id   TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    skill_name TEXT NOT NULL,
    PRIMARY KEY (agent_id, skill_name)
);

INSERT OR IGNORE INTO agent_skills_new (agent_id, skill_name)
    SELECT ags.agent_id, s.name
    FROM agent_skills ags
    JOIN skills s ON s.id = ags.skill_id
    WHERE ags.agent_id IN (SELECT id FROM agents);

DROP TABLE IF EXISTS agent_skills;

ALTER TABLE agent_skills_new RENAME TO agent_skills;

CREATE INDEX IF NOT EXISTS idx_agent_skills_agent ON agent_skills(agent_id);