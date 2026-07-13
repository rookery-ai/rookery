# Connector Service Layer — Spec 2 (breadth) + Spec 3 (Composio removal)

**Date:** 2026-07-10
**Status:** Approved for implementation (decomposition already agreed in Spec 1 brainstorming)
**Builds on:** Spec 1 (`2026-07-10-connector-service-layer-design.md`) — Google/Gmail spine, done.

## Goal

Spec 2 — add the priority providers (GitHub, Notion, Outlook/Microsoft Graph, Jira) so
agents get native typed tools for them, extend the surface to CLI coders + one-off chat,
and polish multi-account UX. Spec 3 — remove Composio entirely, so there is exactly ONE
path to any connected service and weak models can't be lured onto the discovery flow.

## Honest finding: providers are NOT pure data files

Mapping the real OAuth flows shows each provider needs a small, provider-specific
capability the Spec 1 engine doesn't yet have. These are the code generalizations Spec 2
must add BEFORE the manifests are meaningful:

| Provider | Quirk that needs code |
|---|---|
| **GitHub** | OAuth tokens **don't expire** by default → `expires_in` absent. Spec 1 treats empty `expires_at` as "expired" and would loop to `NEEDS_REAUTH`. Need: a non-expiring token mode (empty/zero `expires_in` → never refresh). |
| **Notion** | Token exchange uses **HTTP Basic auth** (`Authorization: Basic base64(id:secret)`), NOT client creds in the body. API calls need a `Notion-Version` header. Search is `POST /v1/search`. Tokens don't expire. Need: per-provider token-auth style (`basic` vs `body`) + static request headers from provider config. |
| **Outlook / MS Graph** | Standard OAuth, but scopes must include `offline_access`; identity via `/me` field `userPrincipalName` (or `mail`). Mostly config; no new code beyond what GitHub/Notion add. |
| **Jira / Atlassian** | Authorize needs `audience=api.atlassian.com&prompt=consent`; after token, must call `/oauth/token/accessible-resources` to get a **cloud id**, then the API base is `https://api.atlassian.com/ex/jira/{cloudid}/...`. Need: extra authorize params from config + a **post-connect resolution hook** that stores a per-connection value, and URL templating with that value. |

**Design response — three engine generalizations (each with tests):**

1. **Non-expiring tokens.** `Provider.TokenExpiry` = `"expiring"` (default) | `"never"`. When
   `never`, connect/refresh store an empty `expires_at` and `AccessToken` treats empty as
   valid (never refresh). `ConnectionsNearExpiry` already excludes empty `expires_at`.
2. **Provider request/auth options in config:**
   - `token_auth: body|basic` — how client creds go to the token endpoint.
   - `static_headers: {Header: value}` — merged into every action request (e.g.
     `Notion-Version: 2022-06-28`, `Accept: application/vnd.github+json`).
   - `authorize_extra: {audience: ..., prompt: consent}` — extra consent-URL params.
3. **Post-connect resolution + per-connection extra field.** Add
   `service_connections.extra` (TEXT, JSON). A provider config may name a
   `post_connect: atlassian_cloudid` hook run once at callback; its result is stored in
   `extra` and made available to URL templates as `{{conn.cloudid}}`. Only Jira uses it in
   Spec 2; the mechanism stays generic.

Everything else per provider remains data files (authorize/token/userinfo URLs, scopes,
action manifests with request templates). Adding a *simple* provider (like GitHub after the
generalizations) is still "two data files."

## Spec 2 build order (each a checkpoint)

- **Phase A — engine generalizations** (non-expiring tokens; token_auth/static_headers/
  authorize_extra; `extra` column + post-connect hook + `{{conn.*}}` templating). Unit-tested.
- **Phase B — GitHub** (data files; the simplest post-generalization case). Proves a 2nd
  provider is data-only.
- **Phase C — Notion** (data files; exercises basic-auth token exchange + static headers).
- **Phase D — Outlook / MS Graph** (data files; offline_access scopes).
- **Phase E — Jira** (data files + the cloudid post-connect hook + `{{conn.cloudid}}`).
- **Phase F — CLI-coder surface**: a `sa-connect <provider> <action> --json '<args>'`
  subcommand on the binary that runs `connectors.Execute` for the active workspace, so CLI
  coders (claude-code/opencode) get the same typed actions via a thin CLI. Runtime prompt
  tells CLI backends to use it.
- **Phase G — one-off chat access**: expose bound connections' read-only actions in the
  chat tool set (parity with the CLI chat's file tools). Mutating actions stay agent-only.
- **Phase H — multi-account UX polish**: web page shows per-account status/reauth; account
  label validated to a safe slug at connect time.

## Spec 3 — Composio removal (after Spec 2 lands the replacements)

Once the priority providers are native, remove Composio so there's one path:

1. Delete `internal/composioassets` + its seeding calls (designer build, agent run).
2. Remove the `composio-toolkit` + `google-workspace` (Composio-based) core skills; the
   native connections replace them. (Keep `github-integration` only if it's not Composio-based.)
3. Remove `composioServicesBlock`/`composioRuntimeNote`/`composioServicesBlock` injection and
   the `ComposioEnabled` param + `loadComposioEnabled` everywhere.
4. Remove the Composio guardrail checks (SDK import/host/version) — no longer reachable.
5. Drop `COMPOSIO_API_KEY` special-casing. Leave any user secret untouched (it just stops
   being referenced).
6. Update CLAUDE.md + the Known-gaps/architecture sections.

Result: a Gmail/GitHub/etc. task has exactly one path — the native typed tools — so weak
models can't be pulled onto discovery. This is also what makes the E2E test meaningful.

## Testing

Per phase: registry-load + tool-name + Execute tests against `httptest` (as in Spec 1). New
engine bits (non-expiring tokens, basic-auth exchange, static headers, cloudid hook,
`{{conn.*}}` templating) each get a focused unit test. Manual E2E (real accounts) deferred to
the end, per the user's plan (Spec 2 + Spec 3 first, then E2E).

## Implementation status & known caveats (2026-07-13)

Phases A–E implemented + unit-tested (httptest); Phases F/G/H deferred. Two caveats that
the green unit suite structurally cannot catch — both must be understood before E2E:

1. **CLI-coder workspaces lost external-service access (regression, not just a deferral).**
   Connector tools live only in the API engine's `hostToolSet`; CLI coders (claude-code,
   opencode) run as subprocesses and never see them. Composio (their old path) is now
   removed. So a CLI-coder workspace currently has NO external-service access until Phase F
   (`sa-connect` CLI). The shipped state is **API-engine workspaces only**. The planned
   weak-model (Mistral/API) E2E is unaffected.
2. **Non-Google provider configs are UNVERIFIED against live docs.** GitHub/Notion/Outlook/
   Jira YAML were hand-authored; the mock-based tests only prove the engine + parsing, not
   that endpoints/scopes/auth/body shapes are correct. Google/Gmail is standard and
   trustworthy → **E2E Gmail first** to validate the architecture, then doc-verify each other
   provider before its own E2E. Known fixes already applied from review: Notion token
   exchange uses a JSON body (`token_content_type: json`); Jira search uses
   `/rest/api/3/search/jql` (the old `/search` is deprecated). Still to verify live: Outlook
   `$search` semantics, Jira create-issue ADF + `/search/jql` param shape, Notion `owner=user`
   consent param, GitHub scope names.

## Non-goals

- Discord/Slack/other providers beyond the five named (trivial to add later as data files).
- Webhook/event subscriptions (pull/polling only).
- Per-action scope minimization (use provider default scopes for now).
