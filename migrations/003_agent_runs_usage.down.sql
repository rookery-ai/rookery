-- No-op downgrade (SQLite cannot DROP COLUMN without a table rebuild).
-- The usage columns are harmless to leave in place on rollback.
SELECT 1;