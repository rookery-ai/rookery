-- Per-agent spending limits, the budget pause, and the "limited" run outcome.
-- Spec: docs/superpowers/specs/2026-11-09-agent-budgets-design.md
--
-- Limits live in their own table rather than as columns on agents: most agents
-- have none, and a row's absence is the "unlimited" default. 0 in any column
-- means that limit is unset.
CREATE TABLE agent_limits (
    agent_id     TEXT PRIMARY KEY REFERENCES agents(id) ON DELETE CASCADE,
    run_tokens   INTEGER NOT NULL DEFAULT 0,
    run_turns    INTEGER NOT NULL DEFAULT 0,
    run_usd      REAL    NOT NULL DEFAULT 0,
    day_tokens   INTEGER NOT NULL DEFAULT 0,
    day_usd      REAL    NOT NULL DEFAULT 0,
    month_tokens INTEGER NOT NULL DEFAULT 0,
    month_usd    REAL    NOT NULL DEFAULT 0,
    updated_at   TEXT    NOT NULL DEFAULT (datetime('now'))
);

-- A budget pause is deliberately NOT `active` and NOT the schedule. `active` is
-- the owner's own switch and the schedule is the owner's cron; a pause that
-- reused either would overwrite a choice the owner made, and could never lift
-- itself when the day or month rolls over. The pause expires on its own at
-- budget_paused_until (UTC, 'YYYY-MM-DD HH:MM:SS' like every other timestamp
-- here; '' = not paused). limit_notice_day makes the "limit reached" notice
-- fire once per day rather than once per skipped run.
ALTER TABLE agents ADD COLUMN budget_paused_until TEXT NOT NULL DEFAULT '';
ALTER TABLE agents ADD COLUMN budget_pause_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE agents ADD COLUMN limit_notice_day    TEXT NOT NULL DEFAULT '';

-- Why a run was ended by a limit; '' = not limited. A column, not an inference
-- from exit_code, for the same reason as `stopped`: the run list renders a label
-- from it. Not backfilled: older runs read '' (no limit applied).
ALTER TABLE agent_runs ADD COLUMN limited TEXT NOT NULL DEFAULT '';
