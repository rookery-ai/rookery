# Directly-configurable coder providers

**Date:** 2026-07-15
**Status:** Approved (design)
**Author:** brainstorming session

## Problem

Configuring an API coder today is URL-first and secret-indirect:

1. The provider picker offers only four entries — `OpenAI`, `OpenRouter`, `Anthropic`,
   `Generic OpenAI-compatible` (`internal/coder/detect.go`, `apiProviders`).
2. Any provider that isn't one of the first three forces the user onto "Generic" and makes
   them hand-type a base URL they have to look up.
3. The API key can't be entered on the coder form. The user must first visit **Secrets**,
   create a secret, then come back and reference it by name.

The popular provider landscape (OpenAI, Anthropic, Z.AI, Ollama, OpenRouter, DeepSeek, Groq,
xAI, Mistral, Gemini, OpenCode Zen/Go, Perplexity, Moonshot, …) is almost entirely
**OpenAI-compatible** — so nearly every one already works through the existing
`openaiProvider` factory in `internal/llm`. "Supporting a provider" is therefore a matter of
**catalog metadata + UX**, not new transport code. Only Anthropic uses a different wire schema,
and it is already implemented.

## Goal

Let the user pick a named provider from a curated catalog, paste an API key inline (stored
transparently as a secret), and type a model. The base URL is resolved automatically from the
provider and is editable under an "Advanced" disclosure for anyone who needs a non-default
endpoint. A "Custom (OpenAI-compatible)" escape hatch preserves the arbitrary-URL path.

## Non-goals

- Live `/v1/models` fetching or model dropdowns. Model is **plain free text** with a
  provider-aware placeholder.
- A YAML/data-file catalog format. The catalog is a small Go slice (see Approach).
- Dual-endpoint handling for Z.AI (OpenAI-compat vs Anthropic-compat vs Coding-Plan). The
  single default plus the Advanced base-URL override covers all variants.
- Any change to the Telegram/Discord surfaces. This is a **web settings + setup** feature.
- Changing the `internal/llm` transport, the Anthropic schema path, or the API engine loop.

## Approach (chosen)

**A — Go slice catalog in `internal/coder`** (chosen over B — an embedded YAML data file).

The catalog is ~16 static entries with display-only fields. A Go slice matches how the coder
package already models this (`apiProviders`), needs no embed/parse/validate machinery, and
keeps "add a provider" to one struct literal plus one `internal/llm` registration. The
connectors package uses YAML because a connector carries auth flows, action manifests, and
request templates; a coder provider carries none of that. YAML here would be heavier than the
data warrants.

## Design

### 1. Single source of truth for base URLs — `internal/llm`

Base URLs live **only** in `internal/llm`, never duplicated in the coder catalog (prevents drift).

- Register every new provider **name** against the existing shared `openaiProvider` factory
  (the same factory already backs `openai`/`openrouter`/`generic`). Anthropic keeps its own
  factory. New names: `zai`, `ollama`, `ollama_local`, `deepseek`, `groq`, `xai`, `mistral`,
  `gemini`, `opencode_zen`, `opencode_go`, `perplexity`, `moonshot`.
- Add each provider's default endpoint to `defaultBases`.
- Add one accessor: `func DefaultBaseURL(name string) string` — returns the registered default
  (or `""`). The web layer uses it to prefill the Advanced base-URL field.

Because `llm.New` already falls back to `defaultBases[provider]` when `cfg.BaseURL == ""`, a
workspace storing `coder_provider="zai"` with an **empty** `coder_base_url` resolves the endpoint
automatically at call time. Base URL is persisted only when the user overrides it (or for Custom).

**Provider catalog (verified where noted; re-verify the unmarked ones against live docs at
implementation time — the Advanced override covers any drift):**

| Registry name  | Label                       | Default base URL                         | Schema    | Key? | Verified |
|----------------|-----------------------------|------------------------------------------|-----------|------|----------|
| `openai`       | OpenAI                      | `https://api.openai.com/v1`              | openai    | yes  | existing |
| `anthropic`    | Anthropic                   | `https://api.anthropic.com`              | anthropic | yes  | existing |
| `openrouter`   | OpenRouter                  | `https://openrouter.ai/api/v1`           | openai    | yes  | existing |
| `zai`          | Z.AI (GLM)                  | `https://api.z.ai/api/openai/v1`         | openai    | yes  | ✅ 07-15 |
| `ollama`       | Ollama Cloud                | `https://ollama.com/v1`                  | openai    | yes  | ✅ 07-15 |
| `ollama_local` | Ollama (Local)              | `http://localhost:11434/v1`              | openai    | **no** | ✅ 07-15 |
| `deepseek`     | DeepSeek                    | `https://api.deepseek.com`               | openai    | yes  | re-check |
| `groq`         | Groq                        | `https://api.groq.com/openai/v1`         | openai    | yes  | re-check |
| `xai`          | xAI (Grok)                  | `https://api.x.ai/v1`                     | openai    | yes  | re-check |
| `mistral`      | Mistral                     | `https://api.mistral.ai/v1`              | openai    | yes  | re-check |
| `gemini`       | Google Gemini               | `https://generativelanguage.googleapis.com/v1beta/openai/` | openai | yes | re-check |
| `opencode_zen` | OpenCode Zen                | `https://opencode.ai/zen/v1`             | openai    | yes  | ✅ 07-15 |
| `opencode_go`  | OpenCode Go                 | `https://opencode.ai/zen/go/v1`          | openai    | yes  | ✅ 07-15 |
| `perplexity`   | Perplexity                  | `https://api.perplexity.ai`              | openai    | yes  | ✅ 07-15 |
| `moonshot`     | Moonshot (Kimi)             | `https://api.moonshot.ai/v1`             | openai    | yes  | ✅ 07-15 |
| `generic`      | Custom (OpenAI-compatible)  | — (user-typed, required)                 | openai    | yes  | existing |

Notes:
- **Z.AI**: `…/api/openai/v1` is the general pay-per-token endpoint. The $10 Coding Plan uses
  `…/api/coding/paas/v4` — reachable via the Advanced override, not a separate catalog entry.
- **OpenCode Zen / Go** are two subscription tiers of the same service sharing one upstream API
  key (`OPENCODE_API_KEY`) but distinct endpoints. They are two catalog entries; the auto-secret
  scheme stores the key under each independently (harmless duplication, no special-casing).

### 2. Display-only catalog — `internal/coder`

Replace the flat `apiProviders []APIProvider` with a richer slice. `APIProviderInfo` carries
**no base URL** (that lives in `llm`):

```go
type APIProviderInfo struct {
    Name             string // registry id, e.g. "zai" — must be registered in internal/llm
    Label            string // human label, e.g. "Z.AI (GLM)"
    Schema           string // "openai" | "anthropic" (display/grouping only)
    ModelPlaceholder string // example model for the free-text hint, e.g. "glm-4.7"
    DocsURL          string // provider API-key/docs page
    RequiresKey      bool   // false only for ollama_local
    Custom           bool   // true only for generic → UI reveals the base-URL field
}
```

`APIProviders() []APIProviderInfo` returns the catalog. The `generic` entry is labelled
"Custom (OpenAI-compatible)" and sorts last.

### 3. Advanced base-URL override for **every** provider

The base-URL input is present for all providers (not just Custom):

- Prefilled from `llm.DefaultBaseURL(selectedProvider)`.
- Collapsed under an **Advanced** disclosure by default.
- Editable; **required** only when the provider is Custom (`generic`).
- For Custom, the field is shown expanded (not hidden behind Advanced).

This is what makes Z.AI Coding Plan, Azure OpenAI, and regional endpoints work without a
catalog entry each.

### 4. Inline key paste → transparent auto-secret

On the coder form the API key becomes a `password` field (not a secret-name reference).

Handler behavior (`handleSaveWorkspaceCoder`, and the setup equivalent) when `kind == "api"`:

1. If a key value was pasted:
   - Decrypt the workspace master password headlessly via
     `secrets.DecryptMasterPassword(w.EncryptedMasterPassword, s.systemKey)` — the same pattern
     `handleCreateSecret` already uses (`web/handlers_secrets.go:55`), so **no re-prompt**.
   - `svc.Set(ctx, secretName, key)` where `secretName = "CODER_KEY_" + upper(provider)` (e.g.
     `CODER_KEY_ZAI`, `CODER_KEY_OPENCODE_GO`). Reserved prefix; overwrites that provider's prior
     entry, leaves other providers' keys intact (reusable when switching back).
   - Persist `coder_api_key_secret = secretName`.
2. If **no** key was pasted **and** `coder_api_key_secret` is already set (edit case): keep the
   existing secret — do **not** clear it or force a re-paste.
3. If the provider `RequiresKey == false` (Ollama Local) and no key present: auto-store a dummy
   value (`svc.Set("CODER_KEY_OLLAMA_LOCAL", "ollama")`) so `llm.New`'s key-required check passes
   with no special-casing in the transport layer. The UI hides the key field for this provider.
4. Base URL: leave empty for catalog providers unless the user overrode it; for Custom, require a
   non-empty typed URL (existing validation).

Reuses the existing `workspaces` columns (`coder_provider`, `coder_model`, `coder_api_key_secret`,
`coder_base_url`) via `db.UpdateWorkspaceCoder`. **No DB migration.**

### 5. UI — settings and setup, both web

**Settings** (`web/templates/dashboard/settings.html`, `#coder_api` block):
- Provider `<select>` rendered from `APIProviders()`.
- Model: free-text `<input>` whose placeholder updates to the selected provider's
  `ModelPlaceholder` via JS.
- API key: `password` input, shown unless the selected provider has `RequiresKey == false`.
  Help text notes it is stored as a secret automatically.
- Docs link rendered from `DocsURL` next to the key field.
- Advanced disclosure containing the base-URL input, prefilled from the provider default
  (a JS map `provider → defaultBaseURL`, sourced from `llm.DefaultBaseURL` and passed to the
  template); auto-expanded and required for Custom.
- JS `onchange` handler updates: model placeholder, key-field visibility, base-URL prefill,
  Custom expansion, docs link.

**Setup wizard** (`web/handlers_setup.go handleSetupCoder`, `web/templates/auth/setup.html` step 3):
The coder step is **currently local-only** (`handleSetupCoder` hardcodes `"local"`; the template
offers only a binary). This adds the API branch so a new workspace can pick a provider during
onboarding — mirroring the settings picker (same catalog, same auto-secret flow). This is net-new
markup + handler logic, sequenced as its own plan phase after the settings surface works.

## Data flow

```
User picks provider "zai", pastes key, types "glm-4.7"
  → POST /dashboard/settings/coder
    → handleSaveWorkspaceCoder:
        decrypt master pw (system key) → svc.Set("CODER_KEY_ZAI", key)
        UpdateWorkspaceCoder(kind=api, provider=zai, model=glm-4.7,
                             api_key_secret=CODER_KEY_ZAI, base_url="")
  → run/chat time: coder.ForWorkspace(w) builds the api engine
    → secretsLookup resolves CODER_KEY_ZAI → key
    → llm.New(provider=zai, base_url="") → defaultBases["zai"] →
       https://api.z.ai/api/openai/v1 via openaiProvider
```

## Backward compatibility

- Existing workspaces on `openai` / `openrouter` / `anthropic` / `generic` keep working
  untouched — those names stay registered with the same defaults.
- "Custom" persists as provider `generic`, so a workspace already on `generic` with a stored
  base URL is unchanged and renders as Custom.
- The old secret-reference flow still functions for any workspace whose `coder_api_key_secret`
  points at a manually-created secret; the edit-flow rule (§4.2) preserves it.

## Edge cases

- **Switching providers**: writes a new `CODER_KEY_<PROVIDER>` secret; old per-provider secrets
  linger (reusable, harmless). Not garbage-collected in this change.
- **Ollama Local**: "localhost" is the **server** host running simple-agents, not the user's
  laptop. Help text must say so. Dummy key satisfies `llm.New`.
- **Secret-name sanitization**: provider ids are already `[a-z_]`; `CODER_KEY_<UPPER>` is a valid
  secret name. Assert this in a test.
- **Blank master password / incomplete setup**: reuse `handleCreateSecret`'s existing guard
  (`SecretsSalt`/`EncryptedMasterPassword` empty → user-facing error).

## Testing

- **`internal/llm`**: table test — every catalog provider name resolves a non-empty base URL
  (via `DefaultBaseURL` or, for `generic`, requires an explicit one) and `New` builds without
  error given a dummy key. `DefaultBaseURL` returns `""` for unknown names.
- **`internal/coder`**: catalog-integrity test — every `APIProviderInfo.Name` (except is-Custom)
  is registered in `internal/llm`; every entry has a non-empty `Label` and `Schema ∈ {openai,
  anthropic}`; exactly one entry has `Custom == true`; `RequiresKey == false` only for
  `ollama_local`.
- **`web`** (handler tests):
  - Paste key for `zai` → secret `CODER_KEY_ZAI` created with the pasted value; coder fields set
    (`provider=zai`, `api_key_secret=CODER_KEY_ZAI`, `base_url=""`).
  - `ollama_local` with no key → save succeeds; dummy secret stored; no "key required" error.
  - Custom with empty base URL → validation error.
  - Edit: save with blank key while `coder_api_key_secret` already set → existing secret retained
    (unchanged), no new secret written.
  - Advanced override: non-default base URL typed for `zai` → persisted to `coder_base_url`.

## Files touched

- `internal/llm/openai.go` — register new provider names on the shared factory.
- `internal/llm/provider.go` — add default base URLs; add `DefaultBaseURL(name)` accessor.
- `internal/coder/detect.go` — replace `apiProviders` with the `APIProviderInfo` catalog +
  `APIProviders()`.
- `web/handlers_misc.go` — `handleSaveWorkspaceCoder`: inline-key auto-secret + edit retention +
  no-key providers; pass base-URL-defaults map + catalog to the settings template.
- `web/templates/dashboard/settings.html` — catalog-driven picker, password key field, Advanced
  base-URL disclosure, JS onchange.
- `web/handlers_setup.go` + `web/templates/auth/setup.html` — API branch in the setup coder step
  (phase 2).
- Tests: `internal/llm/*_test.go`, `internal/coder/*_test.go`, `web/*_test.go`.

## Implementation phases

1. **`llm` + `coder` catalog** — provider registrations, `defaultBases`, `DefaultBaseURL`,
   `APIProviderInfo`, catalog + integrity tests. (No UI yet.)
2. **Settings UI + handler** — picker, inline-key auto-secret, Advanced override, handler tests.
3. **Setup wizard** — add the API branch to the onboarding coder step.
