-- Record WHY a chat stopped: '' (active, or stopped before this column
-- existed), 'idle' (30 minutes of inactivity), 'user' (stopped on purpose).
--
-- Chat apps now open a real chat for every conversation, and when one ends
-- from inactivity the next reply tells the owner so. That notice must not fire
-- for a chat the owner stopped themselves with /chat stop, and nothing else
-- records the difference: inferring it from last_seen gets "stopped, came back
-- two hours later" wrong. Not backfilled — a historical stopped chat reads ''
-- and is simply never announced, which is the safe direction.
ALTER TABLE chats ADD COLUMN ended_reason TEXT NOT NULL DEFAULT '';
