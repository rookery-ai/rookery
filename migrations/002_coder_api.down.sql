-- SQLite cannot drop a column without a table rebuild; this migration is
-- intentionally a no-op on the way down. The coder_base_url column is harmless
-- to leave in place if the api coder kind is rolled back.
SELECT 1;