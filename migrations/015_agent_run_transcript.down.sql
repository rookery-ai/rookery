-- Drop the run transcript and the silent flag.
--
-- Both columns only ever ADD information, so reversing returns run history to
-- what it showed before: the [CHAT] output and nothing else. What is lost is
-- the captured tool calls and coder turns for runs recorded while the column
-- existed, which cannot be recomputed — a run is not re-executable after the
-- fact. That is a real loss of diagnostic history rather than of application
-- state, and the vault run note still holds each run's raw output.
ALTER TABLE agent_runs DROP COLUMN silent;
ALTER TABLE agent_runs DROP COLUMN transcript;
