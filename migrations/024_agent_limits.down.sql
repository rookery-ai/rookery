-- Drop the limits table. The added columns on agents and agent_runs are left in
-- place: they default to '' (not paused, not limited), so older code ignores them,
-- and SQLite column drops would rebuild two hot tables for no benefit.
DROP TABLE agent_limits;
