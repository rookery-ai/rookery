-- Remove the type column from agents (was used before unified conversational creation).
-- SQLite 3.35+ (bundled: 3.49) supports DROP COLUMN.
ALTER TABLE agents DROP COLUMN type;
