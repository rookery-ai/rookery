# Making the five CLI coders usable, and eleven more API providers

**Date:** 2026-08-11
**Status:** approved, ready for implementation
**Scope:** Spec B of four. See also: `2026-08-11-reconnect-and-workspace-images-design.md` (A),
`2026-08-11-sigv4-auth-kind-and-aws-design.md` (C),
`2026-08-11-connector-expansion-waves-design.md` (D).

The website advertises five CLI coders. All five are *detected*. Only two are *usable*,
and the gap is one missing form field. Separately, the direct-API coder catalog has 31
providers and is missing several that people actually run.

## 1. The local coder cannot be given a model

### The defect

`coder.DetectInstalled` finds all five (`internal/coder/detect.go:100-104`): Claude Code,
OpenCode, Codex, Gemini CLI, Cursor. Detection is not the problem.

The coder settings form collects a model only when `coder_kind == "api"`. The
`#coder_local` branch of `CoderSection.tsx` contains a binary picker and nothing else. The
save path is worse than merely omitting it — it writes an empty string over whatever was
there:

```go
// web/api_settings.go:707
s.db.UpdateWorkspaceCoder(w.ID, "local", bin, timeoutS, backend, "", "", "", "")
//                                                              ^^ model
```

So `workspaces.CoderModel` is actively wiped on every local-coder save, and cannot be set
through the UI at all.

The consequence is worst for OpenCode, which unlike Claude has no default model of its
own: it talks to many providers, and with no `-m` it targets a hardcoded default provider
(OpenRouter) and fails with `coder error: User not found. (status 401)`. That reads as
broken authentication and is actually a missing model, so re-running `opencode auth login`
never fixes it. **OpenCode cannot be configured through the UI today.**

Codex and Gemini have a second, separate problem — `cliModel` is never passed to them at
all, recorded in two NOTE comments:

```go
// internal/coder/coder.go:549-555
// NOTE: cliModel is not yet plumbed to codex (codex supports -m/--config model=);
//       a workspace CoderModel is currently a no-op for this backend.
// NOTE: cliModel is not yet plumbed to gemini (gemini supports -m); ...
```

### The fix

**A Model input in the `#coder_local` section**, in both `CoderSection.tsx` and
`SetupWizard.tsx`, read by `handleSaveWorkspaceCoder` and `handleSetupCoder` and persisted
through the existing `UpdateWorkspaceCoder` model parameter. No migration: the column
already exists and the runner already reads it.

The field is **optional**, with a placeholder that changes with the selected binary,
because the same field means different things per coder:

| Binary | Placeholder | Required in practice |
|---|---|---|
| `claude` | *(inherits your login)* | no |
| `opencode` | `ollama-cloud/glm-5.2` | **yes** — 401s without one |
| `codex` | `gpt-5.5-codex` | no |
| `gemini` | `gemini-2.5-pro` | no |
| `cursor` | `sonnet-4.5` | no |

Placeholders are illustrative, not validated. The server must not reject an unknown model
string: the set of valid models changes weekly and is the provider's business, not ours.

OpenCode's requirement is surfaced as **inline help under the field**, not as a validation
error — a user who genuinely wants to rely on a host-level default should not be blocked,
and a blocking validation would be wrong the moment OpenCode ships a default.

**Plumb `cliModel` to `codexBackend` and `geminiBackend`.** Both accept `-m`. Follow
`opencodeBackend.buildArgs`, which adds the flag only when the model is non-empty, so an
empty model remains exactly today's command line.

### Tests

- Saving a local coder with a model persists it; saving again without touching the field
  does not blank it. This is the actual regression and it needs a direct test.
- `codexBackend.buildArgs` and `geminiBackend.buildArgs` include `-m <model>` when set and
  omit the flag entirely when empty.
- The setup wizard's local branch round-trips a model.

### Recorded limit

Only `claude` and `opencode` are installed on the development host. **Codex, Gemini and
Cursor remain authored-but-unverified**, exactly as they are today. This spec fixes their
model plumbing, which is code and is unit-testable; it does not and cannot claim a live
end-to-end round-trip for those three. Closing that requires a host with the binaries
installed and accounts behind them, and it stays an open gap.

## 2. Eleven more direct-API providers

All OpenAI-schema, so each is a catalog row plus a base URL plus a logo, with no framework
change. Azure OpenAI and Google Vertex AI stay deferred — see the exclusions below.

**Hosted tier:**

| Name | Label | Why it earns a row |
|---|---|---|
| `cohere` | Cohere | Enterprise RAG incumbent; ships an OpenAI compatibility endpoint |
| `nvidia` | NVIDIA NIM | build.nvidia.com; the standard on-prem GPU inference path |
| `vercel_ai` | Vercel AI Gateway | Fast-growing router; already how many Next.js shops reach models |
| `minimax` | MiniMax | M-series models are widely used for coding agents |
| `baseten` | Baseten | Dedicated model deployments, OpenAI-compatible |
| `novita` | Novita AI | Low-cost open-weight inference |
| `hyperbolic` | Hyperbolic | Low-cost open-weight inference |
| `chutes` | Chutes | Very widely used cheap inference in the agent community |
| `venice` | Venice AI | Privacy-positioned, no-logging inference |
| `ai21` | AI21 (Jamba) | Long-context Jamba family |

**Local tier:**

| Name | Label | Why |
|---|---|---|
| `litellm` | LiteLLM Proxy | The most common self-hosted OpenAI-compatible gateway; slots beside Ollama/vLLM |

### Constraints each row must satisfy

- **A resolved base URL in `llm.defaultBases`, never a template.** `llm.New` assigns
  `cfg.BaseURL` straight into the HTTP client with no validation, so a `{region}`
  placeholder passes every test and then fails at request time with an opaque DNS error.
  This is why Bedrock bakes `us-east-1` and leaves region variation to the per-workspace
  override. `TestAPIProviders_BaseURLsAreDialable` pins the property.
- **Every base URL is confirmed against the provider's own current documentation during
  implementation.** They are not carried forward from memory. A wrong base URL produces a
  provider that appears in the picker and cannot answer.
- **`RequiresKey` is an iff against `Group == GroupLocal`**, enforced by
  `TestAPIProviders_KeylessIsLocalTier`. The ten hosted providers require a key; LiteLLM
  does not and gets `placeholderLocalKey` via `coder.PlanKeySecret`, because `llm.New`
  rejects an empty key.
- **`generic` stays last in the catalog** (`TestAPIProviders_CustomIsGenericAndLast`).
- **A `scripts/vendor-brand-logos.sh` manifest line per provider.** lobehub is
  authoritative for AI/LLM marks. Verify each rendered mark is visible on the white tile —
  a white-on-transparent variant passes every structural test and renders as an empty
  square.

### Deliberate exclusions

- **Azure OpenAI** — needs an `api-key` header instead of Bearer, the deployment name in
  the URL path, and a mandatory `api-version` query parameter. That is a new
  `llm.Provider` implementation plus new fields on the coder form, not a catalog row.
  It is the largest remaining enterprise gap and is a candidate for its own spec.
- **Google Vertex AI** — mints short-lived OAuth tokens from a service-account JSON key,
  which `llm.Config.APIKey` (a plain string `llm.New` rejects when empty) cannot express.
- **Anyscale** — the endpoint is discontinued.

## Documentation obligation

The provider count appears in `README.md`, `CLAUDE.md` and the website, and
`make docs-sync-check` measures it against source. Run the `docs-sync` skill before
opening the pull request; the coder-provider count moves from 31 to 42.
