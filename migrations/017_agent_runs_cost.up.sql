-- Record what a run cost, not only how many tokens it spent.
--
-- agent_runs has carried prompt/completion/total tokens since 003, but a token
-- count is not a price: the same 200k tokens cost different amounts on different
-- models, and the part served from a prompt cache is billed at a fraction. So
-- "what is this agent costing me?" could not be answered from the run history at
-- all, only estimated from outside.
--
-- cached_tokens is the part of prompt_tokens the provider served from its cache.
-- cost_usd is what the provider said the run cost, summed over its turns —
-- taken from the provider rather than computed from a price table, because a
-- table is a second copy of someone else's pricing and goes stale in silence.
--
-- Both carry a _reported flag rather than relying on 0 as a sentinel. A provider
-- that reports zero cache hits and a provider that reports nothing are opposite
-- findings, and a CLI coder reports neither — rendering its runs as "$0.00"
-- would read as free rather than as unmeasured.
ALTER TABLE agent_runs ADD COLUMN cached_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE agent_runs ADD COLUMN cache_reported INTEGER NOT NULL DEFAULT 0;
ALTER TABLE agent_runs ADD COLUMN cost_usd REAL NOT NULL DEFAULT 0;
ALTER TABLE agent_runs ADD COLUMN cost_reported INTEGER NOT NULL DEFAULT 0;
