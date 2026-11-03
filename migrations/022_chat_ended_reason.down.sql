-- Drops the stop reason. Chat apps keep working; they simply cannot tell an
-- idle-ended chat from a stopped one, so the "previous chat ended" notice
-- never fires.
ALTER TABLE chats DROP COLUMN ended_reason;
