# Connector Service Layer — Design (Spec 1: the Google/Gmail spine)

**Date:** 2026-07-10
**Status:** Approved for planning
**Branch context:** builds on `feat/agent-edit-flow`

## Problem

Agents reach external services (Gmail, GitHub, Notion, Jira, …) through Composio's
v3 REST API. The agent-facing flow is *discover a tool slug → pick the right one →
guess the argument shape → execute → parse the response*. Weak API-coder models
(and sometimes CLI coders) fail at this: too many degrees of freedom, no argument
schema (the seeded `list_tools` helper returns only slug/name/description, dropping
the input parameters Composio actually exposes), and slug/DRAFT-vs-SEND confusion.

We want to (a) make reads **and** writes reliable for weak models, and (b) **remove
the Composio dependency entirely**, owning the integration end-to-end.

## Goals

- Native, typed, per-service **tool** surface — the model picks a tool and fills
  typed args, exactly like `read_file`. No discovery, no slug/arg guessing.
- **Self-managed OAuth**: the platform owns the OAuth flow, encrypted token storage,
  and refresh. No third-party auth broker.
- **Extensible by data file, not code**: adding a service is adding a manifest +
  an OAuth-provider config, never bespoke Go.
- **Multi-account**: a workspace can connect several accounts of the same provider
  (two Gmail accounts) and an agent can target a specific one without free-text.
- Reliable for **read and write (POST)**, with a build-time guard against real
  outbound sends during generation.

## Non-goals (Spec 1)

- Providers other than Google/Gmail (Spec 2: Outlook, Notion, Jira, GitHub).
- CLI-coder tool surface (`sa-connect`) — Spec 2.
- One-off chat connector access — Spec 2 (Spec 1 is agent-only).
- Composio removal/migration — Spec 3 (Composio stays running in parallel until the
  priority providers are live on the new layer).

## Decisions (from brainstorming)

| Axis | Decision |
|---|---|
| Agent surface | **Native typed tools** (function-calling for the API engine) |
| Authoring | **Declarative manifest registry**; actions map to real provider REST calls |
| Auth backend | **Self-managed OAuth** (platform owns flow + tokens + refresh) |
| OAuth client creds | **Per-workspace** app creds per provider |
| Accounts | **Multiple connections per (workspace, provider)** — first-class |
| Multi-account tool resolution | **Label-suffixed tools** (`gmail_send_email__work`), not a free-text account arg |
| Pilot | **Google / Gmail** (hardest OAuth case first, de-risks the worst path) |

## Decomposition (build order)

1. **Spec 1 (this doc): the connector spine, proven on Google/Gmail, API engine only.**
   A workspace connects a Gmail account; an agent bound to it reads (search/list) and
   writes (create draft), with sends blocked at build time.
2. **Spec 2 — breadth:** Outlook, Notion, Jira, GitHub as data files; CLI-coder
   surface; multi-account UX polish.
3. **Spec 3 — Composio removal + migration.**

---

## Architecture

### Data model (two new tables, `systemKey`-encrypted)

Both tables are encrypted under the existing 32-byte `secrets` `systemKey` (the same
key that protects `workspaces.encrypted_master_password`), so the background refresh
loop and headless scheduler runs decrypt without a master password.

**`service_provider_configs`** — per-workspace OAuth *app* credentials.
```
id, workspace_id, provider,
encrypted_client_id, encrypted_client_secret,
created_at, updated_at
UNIQUE(workspace_id, provider)
```

**`service_connections`** — one row per connected account (multi-account = multiple rows).
```
id, workspace_id, provider,
account_label,            -- user nickname, e.g. "work"; unique per (ws, provider)
account_identity,         -- real email/username, fetched from provider at connect
scopes,                   -- granted scopes (space-separated)
encrypted_access_token,
encrypted_refresh_token,
expires_at,               -- access-token expiry (UTC)
status,                   -- ACTIVE | EXPIRED | REVOKED | NEEDS_REAUTH
created_at, updated_at
UNIQUE(workspace_id, provider, account_label)
```

New migration `005_connectors.up.sql` / `.down.sql`.

**`agent_connections`** — agent → bound connection, mirrors `agent_skills`.
```
agent_id, connection_id
PRIMARY KEY(agent_id, connection_id)
```
Source of truth for which connections an agent may use, exactly as `agent_skills`
(by name) is for skills. Deleting a connection cascades / cleans dangling rows.

### `internal/connectors` — registry + execution engine

The new package owns everything provider-facing. Two embedded file kinds per provider:

- **`providers/google.yaml`** — OAuth config: `authorize_url`, `token_url`,
  `default_scopes`, `userinfo` (endpoint + JSON path to the account identity),
  `refresh` semantics (Google returns no new refresh token on refresh; keep the old).
- **`connectors/google.yaml`** — action manifest: a curated set of actions. Each:
  ```yaml
  - name: gmail_send_email          # becomes the tool name
    description: "Send an email from the connected Gmail account. Use when the user
                 wants to actually send mail (not draft it)."
    mutating: true                  # outbound; blocked at build time
    params:                         # JSON-schema object
      to:      {type: string, required: true}
      subject: {type: string}
      body:    {type: string}
    request:                        # how typed args -> a real HTTP request
      method: POST
      url: "https://gmail.googleapis.com/gmail/v1/users/me/messages/send"
      body_builder: gmail_rfc822    # named builder for the few non-trivial encodings
    response:
      extract: "$.id"               # normalized result payload
  ```
  Spec 1 Gmail action set (curated, small): `gmail_search`, `gmail_list_messages`,
  `gmail_get_message` (reads); `gmail_create_draft` (write, non-mutating);
  `gmail_send_email` (write, mutating).

**Types:**
- `Registry` — `LoadBundled()` parses embedded provider + manifest files; lookups by
  provider/action.
- `Provider` — OAuth config; `AuthorizeURL(cfg, state)`, `ExchangeCode`, `Refresh`,
  `FetchIdentity`.
- `Action` — name, description, JSON-schema, `mutating`, request template, response
  extract.
- `Execute(ctx, conn, action, args) (Result, error)` — the single choke point.

**`Execute` flow:**
1. Validate `args` against the action's JSON-schema (reject unknown/missing).
2. `ensureFreshToken(conn)` — if `expires_at` is near, refresh via the provider
   config, re-encrypt + persist the new access token, update `expires_at`. On refresh
   failure set `status = NEEDS_REAUTH` and return a typed error.
3. Render the request template from typed args (path/query/body); attach
   `Authorization: Bearer <access_token>`.
4. Call the provider (reuse the HTTP client plumbing already in `hosttools.go`;
   retry transient 429/5xx).
5. Normalize response via `response.extract`; normalize provider errors into a
   consistent `ConnectorError` (actionable message, like `ComposioError`).

Most actions need no custom code — only a handful of named `body_builder`s (e.g.
`gmail_rfc822` base64url-encodes an RFC-822 message) are Go, shared across providers.

### OAuth subsystem (web)

Routes (under the existing connectors area):
```
GET  /dashboard/connectors/services                 -- list provider configs + connections
POST /dashboard/connectors/services/:provider/creds -- save workspace client_id/secret
POST /dashboard/connectors/services/:provider/connect  -- begin OAuth (redirect to provider)
GET  /oauth/callback/:provider                      -- exchange code, fetch identity, store
POST /dashboard/connectors/services/:id/reauth      -- re-run consent for an existing conn
POST /dashboard/connectors/services/:id/delete      -- revoke + delete connection
```

**Connect flow:** ensure client creds exist → build `AuthorizeURL` with a signed,
short-TTL `state` (encodes workspace_id + provider + account_label + CSRF nonce) →
redirect. Callback: verify `state` → `ExchangeCode` → `FetchIdentity` (sets
`account_identity`) → encrypt + insert `service_connections` row (`status=ACTIVE`).

**Redirect URI:** the workspace registers its Google app with the platform's callback
URL (`https://<host>/oauth/callback/google`). Documented in the connect UI; the host is
config-driven (`SA_PUBLIC_URL`, defaulting to the request host).

**Refresh goroutine:** a background loop (sibling of scheduler/reminder, started in
`serve`) scans `service_connections` for tokens near expiry and refreshes proactively,
so a scheduled run never hits an expired token.

### Agent-facing tool surface (the reliability payoff)

- **Exposure:** `hostToolSet` gains the bound connections + the `Registry`. `tools()`
  appends one `llm.Tool` per action of each bound connection; `execute()` routes any
  connector tool call to `connectors.Execute`. Non-bound → not exposed, so the tool
  list stays tight.
- **Binding (mirrors skills):** AGENT.md gains a `# Connections:` header the designer
  emits (like `# Skills:`); parsed into `agent_connections`. The designer's system
  prompt lists the workspace's available connections (label + identity + provider);
  the runner (`runCoderAgent`) reads bound connections from `agent_connections` and
  passes them to the coder.
- **Multi-account resolution:** bind one Google connection → tool name is bare
  (`gmail_send_email`), targeting it. Bind two → each action's tools are
  **label-suffixed** (`gmail_send_email__work`, `gmail_send_email__personal`) so the
  model selects the account by tool name — never a free-text account string.
- **Build-time safety:** during generation (`SA_BUILD_PHASE=generation`), `Execute`
  refuses `mutating: true` actions and returns a `BuildTimeBlocked` result (reusing
  the existing Composio-guard pattern). Non-mutating writes (create draft) run for
  real at build time as proof.

### Prompt changes (`internal/prompts`)

- Replace the run-time `composioServicesBlock`/`composioRuntimeNote` **for bound
  connections** with a `connectedToolsBlock` that simply names the available typed
  tools and their accounts — no discovery spec (the tools ARE the interface). Composio
  blocks remain for now (parallel), removed in Spec 3.
- Designer prompt gains an `<available_connections>` block + the `# Connections:`
  header contract, analogous to `<available_skills>` / `# Skills:`.

## Data flow (happy path, run time)

```
scheduler/manual run
  -> runner loads agent + agent_connections (bound connection ids)
  -> coder.ForWorkspace(..).WithConnections(bound, registry)
  -> API engine hostToolSet.tools() includes gmail_* typed tools
  -> model calls gmail_search{query:"invoice"}
     -> connectors.Execute: validate -> ensureFreshToken -> GET gmail messages
        -> normalize -> return result to the model
  -> model calls gmail_create_draft{...} -> real draft created
  -> [CHAT] summary delivered
```

## Error handling

- `ConnectorError` (with `ConnectorAuthError`, `ConnectorRateLimit`, `ConnectorServer`
  subtypes) carries an actionable message surfaced verbatim to the model, mirroring the
  `ComposioError` contract the prompts already teach.
- Token refresh failure → `status = NEEDS_REAUTH`; the tool result tells the agent the
  connection needs re-auth and points the user at the connectors page. The connectors
  UI shows the same status.
- Schema-validation failure → returned to the model as a typed error naming the bad
  field, so it can correct within the loop.

## Testing strategy

- `internal/connectors`: unit tests for manifest/provider loading, JSON-schema
  validation, request-template rendering, `body_builder` encoders, response extraction,
  and `ConnectorError` normalization — against an `httptest` server (no live Google).
- OAuth flow: unit tests for `state` sign/verify, `ExchangeCode`/`Refresh`/`FetchIdentity`
  against `httptest` fakes; token encryption round-trip under `systemKey`.
- Refresh goroutine: table-driven test that a near-expiry connection is refreshed and a
  failed refresh flips `status`.
- Tool exposure: test that `hostToolSet.tools()` includes the right tools for 1 vs 2
  bound Google connections (bare vs label-suffixed) and none when unbound; that
  `mutating` actions are blocked under `SA_BUILD_PHASE=generation`.
- Manual E2E (documented, not automated): connect a real Gmail account, run a bound
  agent on a weak API-coder model, confirm search + create-draft succeed and send is
  build-blocked.

## Open items to confirm during planning

- Exact curated Gmail action list and their real endpoints/scopes.
- Whether `account_label` is user-entered at connect time or defaulted from
  `account_identity` (proposed: prompt for it, default to the email local-part).
