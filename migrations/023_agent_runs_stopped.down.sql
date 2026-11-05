-- Drop the stopped flag. Runs recorded as stopped then read as ordinary failures
-- (exit_code=-1), and the vault run note still says they were stopped.
ALTER TABLE agent_runs DROP COLUMN stopped;
