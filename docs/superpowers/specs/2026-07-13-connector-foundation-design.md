# Connector Foundation: API-Key Auth + Generic Request Engine (3-Provider Proof)

**Date:** 2026-07-13
**Status:** Design approved; ready for implementation plan
**Package:** `internal/connectors` (+ small `web/handlers_services.go` touch)

## Context

The platform's connector layer (`internal/connectors`, self-managed OAuth, replaced
Composio) exposes curated, typed provider actions to agents through the single `Execute`
choke point. It ships 5 OAuth providers (google, github, notion, outlook, jira).

The larger goal is to support ~30 services with the maximum valuable actions each, via
OAuth **or** API-key auth. That effort decomposes into four independent sub-projects:

- **A. Foundation** — API-key auth + generic request/body engine + schema/UI plumbing *(this spec)*
- **B. Catalog** — research + author manifests for the remaining ~27 providers
- **C. UI/UX** — connectors-page redesign (categories, search, auth-aware forms)
- **D. Outliers** — AWS (SigV4 signing) and PostgreSQL (DB wire protocol) — each its own design

This spec covers **only sub-project A**, proven end-to-end through a thin vertical slice of
**3 providers**: Slack (new OAuth + nested body), OpenAI (API-key + Bearer), and an extended
Gmail (nested/array bodies). Composio is used **only** as a reference catalog to pick valuable
actions; it is not a runtime dependency (it remains fully removed).

## Problem: three hard limits block breadth

1. **Auth is hardcoded** to `Authorization: Bearer <token>` (`execute.go:94`). There is no
   API-key path anywhere. Services like OpenAI/Stripe/SendGrid/Twilio are key-based.
2. **Bodies are flat-only or Go-only.** `RequestTemplate` supports a flat `body_json`
   (`map[string]string`) or a named Go `body_builder`. Any nested or array body (label
   add/remove, Slack blocks, OpenAI messages) requires hand-written Go per action — which
   does not scale to hundreds of actions and breaks the layer's "adding a service = data
   files, no Go" property (`registry.go` package doc).
3. **`validateArgs` only understands flat object schemas** — no `array` params.

## Success criteria

Proven by **real calls against real accounts** (the way google/github/notion were verified):
the user supplies a Slack OAuth app, a Google OAuth app, and an OpenAI API key. `cmd/livecheck`
exercises each provider with real read calls **and one safe mutating call each** (Gmail
add-label / create-draft, Slack post to a test channel, OpenAI completion) — **never** an
outbound send on the user's behalf, honoring the existing build-time mutation guard.

## Scope

- **~10 actions per proof provider** (~30 total).
- Additive changes inside `internal/connectors`; reuse existing encryption, storage,
  multi-account, agent-binding, sandbox, and the `connector exec` bridge unchanged.

Out of scope (later sub-projects): the other 27 providers, connectors-page redesign, AWS,
PostgreSQL, Stripe form-encoding.

## Architecture

### 1. Declarative auth block (provider YAML)

Providers gain an optional `auth:` block. Absent or `kind: oauth2` → behaves exactly as
today (Bearer). It drives **both** the connect UI and request-time auth injection.

```yaml
# providers/openai.yaml
name: openai
auth:
  kind: api_key            # oauth2 (default) | api_key
  placement: header        # header | query | basic
  header_name: Authorization
  value_prefix: "Bearer "  # embedded credential prefix
  key_label: "OpenAI API key"
  key_hint: "sk-..."
  setup_url: https://platform.openai.com/api-keys
```

- `placement: header` → `header_name: value_prefix + credential`
- `placement: query` → append `param_name=<credential>` to the URL
- `placement: basic` → `Authorization: Basic base64(credential + ":")` (Stripe later)

Slack stays `kind: oauth2` (Bearer). Slack tokens do not expire → `token_expiry: never`
(same path as GitHub/Notion — no refresh).

### 2. `applyAuth` in Execute

The hardcoded line at `execute.go:94`:

```go
req.Header.Set("Authorization", "Bearer "+token)
```

becomes:

```go
applyAuth(req, prov, credential, &u) // u may be rewritten for query placement
```

`TokenStore.AccessToken` still returns "the credential" — an OAuth access token or a static
key. `applyAuth` injects it per the provider's `auth` block. One focused, table-tested function.

### 3. Data model — zero new columns

An API-key connection reuses `service_connections`:

- `encrypted_access_token` ← the API key (system-key encrypted, same as OAuth tokens)
- `encrypted_refresh_token` = `''`, `expires_at` = `''`, `status` = `'ACTIVE'`
- **No `service_provider_configs` row** (there is no OAuth app to configure).

`DBTokenStore.AccessToken` gains one branch: **if the provider's `auth.kind == api_key`,
decrypt and return the stored credential directly — never refresh.** Multi-account works
unchanged via `UNIQUE(workspace_id, provider, account_label)` and the existing `__<label>`
tool-naming. `account_identity` fetch becomes optional for `api_key` providers (no userinfo
endpoint required; falls back to the user-supplied label).

### 4. Declarative nested body renderer

`RequestTemplate` gains a `body:` field: arbitrary nested YAML (maps + arrays) whose leaf
values may be `{{arg}}` placeholders. New `renderBody` (in `render.go`) walks the tree and
produces JSON. `body_builder` is **kept** as the escape hatch for non-JSON encodings.

```yaml
# connectors/gmail.yaml — array body, no Go
- name: gmail_modify_labels
  mutating: true
  params:
    type: object
    properties:
      id:     {type: string}
      add:    {type: array, items: {type: string}}
      remove: {type: array, items: {type: string}}
    required: [id]
  request:
    method: POST
    url: "https://gmail.googleapis.com/gmail/v1/users/me/messages/{{id}}/modify"
    body:
      addLabelIds:    "{{add}}"
      removeLabelIds: "{{remove}}"
```

**Renderer semantics** (small, bounded, each unit-tested):

1. **Type-preserving substitution** — a leaf that is *exactly* a lone `{{arg}}` adopts the
   arg's real Go type (array→array, int→int, bool→bool). A placeholder embedded in a larger
   string (`"Bearer {{x}}"`) renders as a string.
2. **Optional-key omission** — a lone-`{{arg}}` leaf whose arg is absent/nil drops the key
   (optional fields never send `null`).
3. **Nested maps/arrays** — walked recursively (Slack `blocks`, OpenAI `messages`).
4. **Safe by construction** — values are placed as real Go values, then `json.Marshal`ed, so
   quotes/newlines in user data can never break the JSON or inject. (This is why the raw
   JSON-string-template alternative was rejected.)

`body_builder` remains only for genuinely non-JSON encodings: Gmail RFC822/base64
(`gmail_rfc822`, `gmail_draft`, new `gmail_reply`), Jira ADF, MS Graph.

### 5. Schema extension

`validateArgs` (`schema.go`) learns the `array` type and validates `items.type` element-wise.
Still no external JSON-schema library. Nested-object params are not needed for the proof (YAGNI).

### 6. UI — auth-type-aware connect (minimal)

The services page (`web/handlers_services.go`) branches on `auth.kind`:

- `oauth2` → today's flow unchanged (save client id/secret → Connect → provider redirect).
- `api_key` → **no creds step**; a single "paste your `<key_label>`" form (+ optional account
  label) showing the `setup_url` link and `key_hint` placeholder → stores the connection
  directly (encrypt key → `service_connections` row, `status=ACTIVE`).

Connected accounts render identically regardless of kind. The full connectors-page redesign
(categories, search) is deferred to sub-project C.

## Action lists (verified against Composio's live catalog; finalized in the plan)

### Gmail — keep existing 4, add 6 (→ 10)
Keep: `gmail_search`, `gmail_get_message`, `gmail_create_draft`, `gmail_send_email`.
Add: `gmail_reply_to_thread` (Go builder: RFC822+threadId), `gmail_modify_labels`
(**array body — engine proof**), `gmail_list_labels`, `gmail_create_label`,
`gmail_move_to_trash` (POST, no body, mutating), `gmail_list_threads`.

### Slack — new OAuth provider, token non-expiring (10)
`slack_send_message` (**nested blocks body — engine proof**), `slack_list_channels`,
`slack_fetch_conversation_history`, `slack_fetch_message_thread`, `slack_find_channels`,
`slack_find_user_by_email`, `slack_list_users`, `slack_add_reaction`, `slack_create_channel`,
`slack_invite_to_channel`. All JSON body + Bearer.

Scopes (bot token): `chat:write`, `channels:read`, `channels:history`, `groups:read`,
`users:read`, `users:read.email`, `reactions:write`, `channels:manage`. (Finalized in plan.)

### OpenAI — API-key Bearer (auth proof) (10)
Composio's `openai` toolkit is Assistants-only; anchor instead on the valuable direct
endpoints: `openai_chat_completion` (**nested messages body**), `openai_list_models`,
`openai_retrieve_model`, `openai_create_embedding`, `openai_create_image`,
`openai_moderation`, `openai_list_files`, `openai_upload_file`, `openai_delete_file`,
`openai_create_assistant`.

**Risk:** `openai_upload_file` is multipart, not JSON — it may drop to a Go builder or be
deferred from the proof. Flagged for the plan.

## Testing

- **Unit (pure Go, no network):**
  - `renderBody` — type-preserving substitution, optional-key omission, nested maps, array
    passthrough, embedded-placeholder-as-string.
  - `applyAuth` — header / query / basic placement table; oauth2 default = Bearer.
  - `validateArgs` — array param acceptance + element-type rejection.
- **Live E2E (`cmd/livecheck`):** real reads + one safe mutating call per provider against the
  user's real Slack app, Google app, and OpenAI key. No outbound sends.

## Risks & mitigations

- **OpenAI multipart upload** — defer or Go builder (flagged above).
- **Renderer scope creep** — keep the four semantics fixed; anything they can't express uses
  `body_builder`. No general expression language.
- **Slack Web API content-type** — use `application/json` + Bearer (supported for the chosen
  chat/conversations methods); confirmed per-method in the plan.

## Explicitly deferred

The other 27 providers (sub-project B), connectors-page redesign (C), AWS SigV4 +
PostgreSQL wire protocol (D), Stripe form-encoded/bracket-array bodies.
