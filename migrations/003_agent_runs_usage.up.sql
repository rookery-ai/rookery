-- Token-usage accounting for the "api" coder kind (direct LLM providers).
-- CLI coders leave these at 0; the API engine accumulates provider-reported
-- usage across the tool-calling loop and persists it here per run.
ALTER TABLE agent_runs ADD COLUMN prompt_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE agent_runs ADD COLUMN completion_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE agent_runs ADD COLUMN total_tokens INTEGER NOT NULL DEFAULT 0;