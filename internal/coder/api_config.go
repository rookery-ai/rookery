package coder

import (
	"context"

	"github.com/rookery-ai/rookery/internal/vault"
)

// SecretsLookup resolves a single secret value by name for a workspace, at run
// time. The API engine uses it to fetch its own provider API key (named by
// apiConfig.apiKeySecretName) without depending on caller-injected env, which is
// inconsistent across the chat / design-conversation / Telegram paths.
//
// The closure is wired in main.go onto both the shared coder and the per-workspace
// factory; it decrypts the workspace master password and reads the secret via the
// secrets service, exactly as the scheduler and runner already do.
type SecretsLookup func(ctx context.Context, workspaceID, name string) (string, error)

// apiConfig configures a direct-LLM-API coder (workspace coder_kind == "api").
// When set on a Coder, Generate/Ping dispatch to the in-process tool-calling
// engine instead of spawning a CLI subprocess.
type apiConfig struct {
	provider         string // llm registry name: "openai", "openrouter", "anthropic", "generic"
	model            string // model id, e.g. "gpt-4o", "anthropic/claude-3.5-sonnet"
	baseURL          string // optional override; provider default when empty
	apiKeySecretName string // name of the secret holding the API key
}

// WithAPIConfig returns a shallow copy of the Coder configured as a direct
// LLM-API coder. Once set, Generate runs an in-process tool-calling loop against
// the provider (see api_engine.go) instead of spawning a CLI subprocess.
func (c *Coder) WithAPIConfig(provider, model, baseURL, apiKeySecretName string) *Coder {
	c2 := *c
	c2.api = &apiConfig{
		provider:         provider,
		model:            model,
		baseURL:          baseURL,
		apiKeySecretName: apiKeySecretName,
	}
	// The prompts-level backend capability for an API coder is "tool-calling".
	if c2.backendType == "" {
		c2.backendType = "api"
	}
	return &c2
}

// WithSecretsLookup attaches the lazy secret resolver used by the API engine to
// obtain its provider API key. Wired in main.go onto both the shared coder and
// the per-workspace factory so every call site (runs, generation, chat, Telegram)
// can resolve the key regardless of whether that path injects secrets via env.
func (c *Coder) WithSecretsLookup(f SecretsLookup) *Coder {
	c2 := *c
	c2.secretsLookup = f
	return &c2
}

// WithVault attaches the per-user vault used by the API engine's host tools
// (read_file/write_file/edit_file/list_dir/run_script) for path-safe file access.
func (c *Coder) WithVault(v *vault.Vault) *Coder {
	c2 := *c
	c2.vlt = v
	return &c2
}

// IsAPI reports whether this coder is a direct-LLM-API coder (vs a CLI subprocess).
func (c *Coder) IsAPI() bool { return c.api != nil }

// WithProgress attaches an optional live-progress sink. The API engine calls it
// once per host-tool execution with a short milestone line (e.g. "🔧 read_file(notes.md)")
// so the web run-progress SSE stream shows tool activity as it happens. CLI coders
// ignore it. Set by the runner from RunInput.OnProgress; nil on all other call sites
// (chat, design, reminders) → no milestones, no behavior change.
func (c *Coder) WithProgress(f func(string)) *Coder {
	c2 := *c
	c2.progress = f
	return &c2
}
