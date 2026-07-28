package coder

import (
	"errors"
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
// ErrLocalCoderDisabled is returned by every entry point of a coder built for a
// "local" workspace in a build that ships no CLI coder (SA_CODER_MODE=slim).
// It is returned at USE time rather than construction time because ForWorkspace
// has no error return and is called from hot paths; failing loudly here beats
// spawning a binary that does not exist and surfacing "executable file not found".
var ErrLocalCoderDisabled = errors.New(
	"this build has no CLI coder (SA_CODER_MODE=slim) — switch this workspace to the API engine in Settings → Coder")

func ForWorkspace(w *db.Workspace, homesDir, dataDir string, vlt *vault.Vault, defaultBin string, defaultTimeout time.Duration, enableSandbox, allowLocal bool) *Coder {
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

	// Everything below builds a CLI-coder subprocess. A slim build has no such
	// binary, so hand back a coder that fails with a message naming the fix
	// instead of one that will fail with "executable file not found".
	if !allowLocal {
		return New(defaultBin, defaultTimeout, homesDir, dataDir).withDisabled(ErrLocalCoderDisabled)
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

	cd := New(bin, timeout, homesDir, dataDir).
		WithBackendType(backendType).
		WithSandbox(enableSandbox).
		WithVault(vlt)
	if w != nil {
		cd.cliModel = w.CoderModel
	}
	return cd
}
