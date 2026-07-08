-- Adds coder_base_url for the "api" coder kind (direct LLM provider APIs).
-- Used by the "generic" OpenAI-compatible provider and as an override for any
-- provider (OpenAI/OpenRouter/Anthropic). Empty by default — the registry
-- applies the provider's canonical base URL when this is blank.
ALTER TABLE workspaces ADD COLUMN coder_base_url TEXT NOT NULL DEFAULT '';