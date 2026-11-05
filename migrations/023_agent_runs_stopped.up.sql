-- Record that the owner stopped a run.
--
-- A column rather than an inference from exit_code, because exit_code=-1 also means
-- failed and interrupted, and the interface must show a stopped run differently from
-- both. A column rather than a transcript field because the run list renders a label
-- from it and must not fetch every transcript to do so, the same reasoning as `silent`.
--
-- Not backfilled: an older run reads 0, meaning "not known to have been stopped",
-- which is the truth about a row written before the flag existed.
ALTER TABLE agent_runs ADD COLUMN stopped INTEGER NOT NULL DEFAULT 0;
