-- Deliberately a no-op, stated rather than left blank so the next reader knows
-- the emptiness is a decision and not an oversight.
--
-- The up migration deletes rows whose parent agent no longer exists. Nothing on
-- disk can reconstruct them, so a down migration that appeared to reverse it
-- would be lying about what it restored. The nulled agent_id columns are
-- equally unrecoverable: the id pointed at an agent row that is already gone.
SELECT 1;
