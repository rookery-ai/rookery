# LLM provider expansion — fifteen drop-in providers, and a discoverable base URL

**Date:** 2026-08-04
**Status:** approved

## Problem

The API coder engine ships sixteen providers. Three gaps matter:

1. **Enterprise clouds are absent.** AWS Bedrock and Alibaba Cloud (Qwen) are the
   two most-asked-for missing names, and neither is reachable today except
   through the `generic` escape hatch, which requires the user to know the base
   URL themselves.
2. **The inference-cloud tier is thin.** Together, Fireworks, Cerebras,
   SambaNova, Nebius and DeepInfra are where open-weight models are actually
   served; Hugging Face and GitHub Models each put hundreds of models behind one
   key.
3. **The local tier is one entry.** `ollama_local` is the only self-hosted
   option, even though LM Studio, llama.cpp, vLLM, LocalAI and Jan all speak the
   same wire protocol on a different port.

Separately, and reported as a bug: **an Ollama server on a non-default port
cannot be configured in practice.** The capability exists — every API provider
has a base-URL override — but the field sits behind an "Advanced" disclosure,
starts empty, and shows a generic `https://api.example.com/v1` placeholder. A
user has no way to discover that the override exists or what shape to type.

## Scope

**In:** fifteen providers that are pure drop-ins (Bearer auth +
`<base>/chat/completions`), the base-URL discoverability fix, and two fixes to
UI code this change directly degrades.

**Out:** Azure OpenAI and Google Vertex AI — see "Deferred" below. Both need a
new provider implementation; bundling them would turn a three-line-per-provider
change into three separate pieces of work.

## Design

### 1. The fifteen providers

Every one speaks the OpenAI chat-completions schema with a
`Authorization: Bearer <key>` header, so `internal/llm/openaiProvider` handles
all of them **unchanged**. Adding one is exactly three edits:

| Edit | File | Field |
|---|---|---|
| Default base URL | `internal/llm/provider.go` | `defaultBases` |
| Register the factory | `internal/llm/openai.go` | the `init()` name list |
| Catalog row | `internal/coder/detect.go` | `apiProviders` |

The SPA provider picker is data-driven off the catalog, so no frontend change is
needed to make a provider selectable.

**Hosted tier (10):**

| Name | Base URL | Model hint |
|---|---|---|
| `bedrock` | `https://bedrock-mantle.us-east-1.api.aws/v1` | `us.anthropic.claude-sonnet-4-6` |
| `alibaba` | `https://dashscope-intl.aliyuncs.com/compatible-mode/v1` | `qwen-max` |
| `together` | `https://api.together.xyz/v1` | `deepseek-ai/DeepSeek-V3` |
| `fireworks` | `https://api.fireworks.ai/inference/v1` | `accounts/fireworks/models/deepseek-v3` |
| `cerebras` | `https://api.cerebras.ai/v1` | `llama-3.3-70b` |
| `sambanova` | `https://api.sambanova.ai/v1` | `Meta-Llama-3.3-70B-Instruct` |
| `nebius` | `https://api.studio.nebius.com/v1` | `deepseek-ai/DeepSeek-V3` |
| `deepinfra` | `https://api.deepinfra.com/v1/openai` | `deepseek-ai/DeepSeek-V3` |
| `huggingface` | `https://router.huggingface.co/v1` | `deepseek-ai/DeepSeek-V3` |
| `github_models` | `https://models.github.ai/inference` | `openai/gpt-4o` |

**Local tier (5):** all `RequiresKey: false`.

| Name | Base URL |
|---|---|
| `lmstudio` | `http://localhost:1234/v1` |
| `llamacpp` | `http://localhost:8080/v1` |
| `vllm` | `http://localhost:8000/v1` |
| `localai` | `http://localhost:8080/v1` |
| `jan` | `http://localhost:1337/v1` |

`llamacpp` and `localai` share port 8080 by design — they are alternative
servers, not concurrent ones, and each ships the default its own docs publish.

Catalog size goes 16 → 31. All fifteen append **before** the `generic` entry,
which must remain last (`TestAPIProviders_CustomIsGenericAndLast`).

#### Two decisions worth recording

**`defaultBases` holds a resolved, dialable URL — never a template.** Bedrock's
endpoint embeds a region and Cloudflare-style providers embed an account id, so
a `{region}` placeholder is tempting. It is wrong here: `llm.New` assigns
`defaultBases[provider]` straight into the HTTP client with no URL validation,
and `TestNew_CatalogProvidersBuild` only asserts the provider *constructs*. A
template would therefore pass every existing test and fail at request time with
a DNS error on a literal `{region}` — a failure with no useful message, arriving
at the worst moment. Bedrock ships `us-east-1` as its default and region
variation goes through the base-URL override, which already exists, already
persists, and is already tested.

**Bedrock's `Schema` field is `"openai"`, not `"anthropic"`.** That field
describes the *wire schema* used to talk to the endpoint, not the vendor of the
model being served. Bedrock serves Anthropic models over an OpenAI-shaped API;
`"anthropic"` there would route it to the wrong provider implementation.

**Bedrock endpoint choice:** AWS documents three path shapes —
`bedrock-mantle.{region}.api.aws/v1`, `bedrock-runtime.{region}.amazonaws.com/v1`,
and `bedrock-runtime.{region}.amazonaws.com/openai/v1`. We ship **mantle**, which
AWS explicitly recommends and which accepts a Bedrock API key as a bearer token
with no SigV4 signing. This is the only reason Bedrock is a drop-in at all.

### 2. Base-URL discoverability (the Ollama port fix)

No schema change, no API change, no new catalog field. The server already ships
each provider's default to the SPA as `entry.base` (from `llm.DefaultBaseURL`);
the form simply never uses it. Three changes in
`web/ui/src/pages/settings/CoderSection.tsx`:

- **Seed the input's value.** On provider change, set the Base URL field's
  `value` from `selectedEntry.base` — but only when the user has not explicitly
  edited it and no saved override exists, so switching providers never clobbers
  a deliberate value. Tracked with a "dirty" ref rather than by comparing
  against the default, because a user may legitimately type the default back in.
- **Auto-expand Advanced** when the selected provider is one where editing the
  URL is the normal case: the whole local tier, plus Bedrock (region).
- **Helper text** under the field: *"Change the port here if your server listens
  somewhere else."*

The saved value is `workspaces.coder_base_url`, unchanged; `llm.New` already
prefers it over the registry default.

### 3. Two fixes to code this change degrades

Both are in the file being edited and are made materially worse by this wave.
Neither is unrelated refactoring.

**The provider dropdown renders raw registry slugs.** `CoderSection.tsx` prints
`{c.name}` in each `<option>`, so the picker reads `ollama_local`, not
`Ollama (Local)`. At sixteen entries that is untidy; at thirty-one, with
`github_models`, `llamacpp` and `deepinfra` in the list, it is unusable. The
human label already exists on `coder.APIProvider.Label` and is already sent to
the SPA on a *different* DTO. Fix: add `Label` to `apiCoderCatalogEntry`
(`web/api_settings.go`) and render it.

**Group the catalog.** Add `Group string` to `coder.APIProvider`, valued
`"hosted"` or `"local"`. The dropdown renders two `<optgroup>`s and the
ProviderCards gallery two labeled sections. This is not only cosmetic: it is
what makes the keyless-provider invariant expressible as a test (below).
`generic` is grouped `"hosted"`; its position as the last entry is independent
of grouping and is preserved.

### 4. Tests

Three existing tests encode today's shape and must generalize:

- **`TestAPIProviders_RequiresKeyOnlyOllamaLocal`** asserts `ollama_local` is the
  only keyless provider. It becomes `TestAPIProviders_KeylessIsLocalTier`:
  `RequiresKey == false` **iff** `Group == "local"`. This is a stronger invariant
  than the original — it catches both a hosted provider that forgets its key
  requirement and a local one that demands a key it does not need — and it is
  only expressible because of the grouping field.
- **`TestAPIProviders_CatalogIntegrity`** — floor rises from 16 to 31.
- **`TestDefaultBaseURL_KnownProviders`** and **`TestNew_CatalogProvidersBuild`**
  (`internal/llm`) — both name-lists extended.

One test is new:

- **`TestAPIProviders_BaseURLsAreDialable`** — every non-`generic` catalog entry's
  default base URL parses via `url.Parse`, has scheme `http` or `https`, has a
  non-empty host, and contains no `{`. This is what makes the
  "resolved, never templated" decision enforceable rather than a comment that
  the next contributor is free to disagree with.

Frontend tests: `CoderSection.test.tsx` gains cases for the value-seeding
behaviour (seeds on provider change, does **not** clobber a saved override, does
**not** clobber a user edit) and for label rendering in the dropdown.

### 5. Small cleanup

`coder.PlanKeySecret` writes the literal string `"ollama"` as the dummy value for
providers that need no key, so `llm.New`'s non-empty-key check passes. With five
more local servers that reads as a bug rather than a placeholder. It becomes a
named constant, `placeholderLocalKey = "no-key"`, with its one test updated. The
stored-secret mechanism is otherwise unchanged.

### 6. Brand logos

`web/logo_coverage_test.go` fails the build for any provider slug with no
vendored SVG, so all fifteen need marks added to
`scripts/vendor-brand-logos.sh`. lobehub — the script's primary source and
authoritative for AI/LLM providers — carries most of them (bedrock/aws, qwen,
together, fireworks, cerebras, sambanova, huggingface, lmstudio, vllm).

Any slug that resolves in none of the three vendoring sources goes into the
existing `allowNoLogo` map with a one-line reason, exactly as the everyday-
connector waves did. Drawing an approximation is not the alternative: an
invented logo misrepresents someone else's brand, which is worse than a letter
tile.

## Deferred

**Azure OpenAI.** Authenticates with an `api-key` header rather than
`Authorization: Bearer`, puts the deployment name in the URL path rather than
the request body's `model` field, and requires an `api-version` query parameter
on every call. None of the three fits `openaiProvider`; it needs its own
`internal/llm/azure.go`. It is the most-requested gap after Bedrock and is worth
a dedicated follow-up.

**Google Vertex AI.** Authenticates with a service account, minting and
refreshing short-lived OAuth tokens rather than presenting a static key.
`llm.Config.APIKey` is a plain `string` that `llm.New` rejects when empty, so
Vertex needs a credential-shape change that touches every provider — a larger
piece of work than the provider itself.

## Risks

**These providers ship verified against vendor documentation, not against live
APIs.** This is the same position `internal/connectors` is in, and it uses the
same mitigation: what was checked, and against what, is written down. No
verification field is added to the catalog — unlike a connector YAML, a coder
provider has exactly one endpoint and one auth shape, so the endpoint table in
this spec *is* the record. A wrong base URL surfaces as a clear connection error on the
coder smoke test (`POST /api/v1/settings/coder/test`), not as silent
misbehaviour.

**Port collisions in the local tier are real but harmless.** `llamacpp` and
`localai` both default to 8080. A user running both must override one — which is
precisely the workflow §2 makes discoverable.
