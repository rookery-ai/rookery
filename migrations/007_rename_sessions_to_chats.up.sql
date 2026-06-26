-- Rename the chat_sessions table to chats (the concept is "chats", not "sessions").
-- Rename the chat_messages.session_id FK column to chat_id to match.
ALTER TABLE chat_sessions RENAME TO chats;

-- SQLite >= 3.25 supports RENAME COLUMN. The FK reference to the (now renamed)
-- chats table is rewritten automatically by SQLite during the parent-table rename
-- above, so only the column name needs updating here.
ALTER TABLE chat_messages RENAME COLUMN session_id TO chat_id;

-- Indexes cannot be renamed; drop the old name and recreate under the new one.
DROP INDEX IF EXISTS idx_chat_sessions_user;
CREATE INDEX IF NOT EXISTS idx_chats_user ON chats(user_id, active);