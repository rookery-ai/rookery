-- Drop the per-schedule timezone.
--
-- Safe to reverse, unlike 013: the column only ever ADDS information. Every row
-- that carries an empty string was already being evaluated in the host's local
-- zone, so dropping it returns those schedules to precisely the behaviour they
-- had. A row that carries an explicit zone reverts to host-local, which is the
-- pre-migration behaviour for that row too — it loses a correction, not data
-- that cannot be recomputed, since the workspace profile still holds the zone
-- the column was populated from.
ALTER TABLE agent_schedules DROP COLUMN timezone;
