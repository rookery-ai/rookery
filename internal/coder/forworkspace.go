package coder

import (
	"time"

	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/vault"
)

// ForWorkspace builds a Coder from a workspace's inlined coder config, falling
// back to the provided system defaults when a field is unset.
//
// Two kinds are supported:
//   - "local": a host CLI binary (claude-code, opencode, codex, cursor). bin/timeout/
//     backendType are taken from the workspace row, falling back to the system default.
//   - "api": a direct LLM provider API coder (OpenAI, OpenRouter, Anthropic, any
//     OpenAI-compatible endpoint). provider/model/base_url/api_key_secret_name are
//     taken from the workspace row; the provider API key is resolved lazily at run
//     time via the SecretsLookup wired by the caller (main.go), so it works on every
//     call site regardless of whether that path injects secrets via env.
//
// vlt is the per-user vault used by the API coder's host tools for path-safe file
// access. It is unused by the local coder path.
func ForWorkspace(w *db.Workspace, homesDir, dataDir string, vlt *vault.Vault, defaultBin string, defaultTimeout time.Duration, enableSandbox bool) *Coder {
	if w != nil && w.CoderKind == "api" {
		c := New(defaultBin, defaultTimeout, homesDir, dataDir).
			WithBackendType("api").
			WithSandbox(enableSandbox).
			WithVault(vlt).
			WithAPIConfig(w.CoderProvider, w.CoderModel, w.CoderBaseURL, w.CoderAPIKeySecret)
		if w.CoderTimeoutS > 0 {
			c.timeout = time.Duration(w.CoderTimeoutS) * time.Second
		}
		return c
	}

	bin := defaultBin
	timeout := defaultTimeout
	backendType := ""

	if w != nil {
		if w.CoderBin != "" {
			bin = w.CoderBin
		}
		if w.CoderTimeoutS > 0 {
			timeout = time.Duration(w.CoderTimeoutS) * time.Second
		}
		backendType = w.CoderBackendType
	}

	return New(bin, timeout, homesDir, dataDir).
		WithBackendType(backendType).
		WithSandbox(enableSandbox).
		WithVault(vlt)
}
