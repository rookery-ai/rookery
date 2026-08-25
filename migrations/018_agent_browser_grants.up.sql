-- Browser acting grants, per agent.
--
-- Two columns rather than one because they are two different decisions. "Let
-- this agent log in and read my bill" and "let this agent pay it" are not the
-- same permission, and collapsing them would mean an owner who wants the first
-- necessarily grants the second.
--
-- Both default to 0. An agent can render and read a page without either, which
-- is the capability most agents actually need; acting is opt-in per agent, made
-- deliberately, once. That default is the primary protection — the accessible
-- name heuristic behind browser_irreversible is only a second layer, and it is
-- explicitly treated as fallible.
--
-- Deliberately NOT scoped per site. An agent granted acting may act on whatever
-- page it opens. A domain allowlist was considered and left out: a real flow
-- redirects across hosts (an identity provider, a payment processor), so a list
-- would either break ordinary logins or be widened until it meant nothing. The
-- limitation is recorded in the spec rather than papered over.
ALTER TABLE agents ADD COLUMN browser_acting INTEGER NOT NULL DEFAULT 0;
ALTER TABLE agents ADD COLUMN browser_irreversible INTEGER NOT NULL DEFAULT 0;
