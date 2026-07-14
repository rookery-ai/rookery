# Connector Catalog B3: base-URL providers (Salesforce, Shopify, Mailchimp, Zendesk)

**Date:** 2026-07-14
**Status:** Design approved; ready for implementation plan
**Package:** `internal/connectors` (+ `web/handlers_services.go`, `web/templates/dashboard/services.html`)

## Context

Sub-project B (Catalog) on the merged Foundation engine. B1 (Google-family + Teams) and B2
(10 JSON providers) are on `main`. This is **B3**: four providers whose API base URL is
**per-connection** — resolved at connect time and injected via the existing `{{conn.<key>}}`
templating (the mechanism Jira's cloud id already uses).

Each provider resolves its base URL a *different* way, so B3 adds **four small, declarative engine
primitives** rather than one generic abstraction.

Deferred to later: **B4** (Stripe, Twilio form-encoding; Trello OAuth1); sub-project **D**
(AWS SigV4, PostgreSQL).

## Decisions (from brainstorming)

- **All four in one batch.**
- **Auth (token-first where possible):** Salesforce OAuth2; Shopify api-key (custom-app admin
  token); Mailchimp api-key (dc parsed from key); Zendesk API-token via HTTP Basic.
- **Action depth:** ~8-10 per provider (~32-40 total).
- **Verification:** unit/rendering only; live E2E deferred.
- **Primitive style:** four narrow declarative primitives, each unit-testable, over one generic
  "extra resolver."

## Engine primitives

Existing machinery reused: `service_connections.extra` (JSON) → `{{conn.<key>}}` in URL/body
templates (via `subst`), and (OAuth-only) `post_connect` hooks. B3 adds:

### 1. `connect_inputs` (provider field) — additive
```yaml
connect_inputs:
  - {key: shop, label: "Store domain", hint: "mystore.myshopify.com", required: true}
```
The api-key connect form renders one input per entry; `handleConnectAPIKey` reads each
`c.FormValue(key)`, builds a map, and stores it as JSON in `service_connections.extra`. Exposed as
`{{conn.<key>}}`. → Shopify `shop`; Zendesk `subdomain` + `email`.

### 2. `token_extra` (provider field) — additive
```yaml
token_extra: [instance_url]
```
`OAuthClient.tokenRequest` captures the named fields from the OAuth **token JSON response** into a
new `TokenSet.Extra map[string]string`; `handleOAuthCallback` merges `ts.Extra` into `extra`. →
Salesforce `instance_url` (Salesforce returns it in the token response, not a separate call).

### 3. `key_extra` (provider field) — additive
```yaml
key_extra: {dc: suffix}   # take the substring after the last '-' in the API key
```
`handleConnectAPIKey` derives the value from the pasted key and stores it in `extra`. Only the
`suffix` rule is implemented (Mailchimp keys are `<secret>-<dc>`). → Mailchimp `dc`.

### 4. Templated Basic username (`auth.basic_user_template`) — small signature change
```yaml
auth:
  kind: api_key
  placement: basic
  basic_user_template: "{{conn.email}}/token"
```
`applyAuth` gains the connection's `extra` map (signature `applyAuth(req, prov, credential,
connExtra map[string]string)`). For `placement: basic` with a `basic_user_template`, it resolves the
template against `connExtra` and calls `SetBasicAuth(resolvedUser, credential)` (credential is the
password). All current callers pass `nil` → unchanged behavior (`SetBasicAuth(credential, "")` when
no template). This is the ONLY cross-cutting change (touches `Execute` + `applyAuth` tests). →
Zendesk `Authorization: Basic base64({email}/token:{apitoken})`.

`Execute` passes `conn.Extra` (already on `ConnRef`) to `applyAuth`.

## Providers, auth & actions (~8-10 each; Composio-verified in the plan)

| Provider | Auth | Base URL | Representative actions |
|---|---|---|---|
| **Salesforce** | OAuth2 + `token_extra: [instance_url]` | `{{conn.instance_url}}/services/data/v60.0` | `soql_query` (GET ?q=), `get_sobject`, `create_sobject`, `update_sobject`, `delete_sobject`, `describe_sobject`, `search` (SOSL), `list_recent` |
| **Shopify** | api-key header `X-Shopify-Access-Token` (`value_prefix: ""`) + `connect_inputs: [shop]` | `https://{{conn.shop}}/admin/api/2024-10` | `list_products`, `get_product`, `create_product`, `update_product`, `list_orders`, `get_order`, `list_customers`, `create_draft_order` |
| **Mailchimp** | api-key + `key_extra: {dc: suffix}` | `https://{{conn.dc}}.api.mailchimp.com/3.0` | `list_audiences`, `get_audience`, `add_member`, `update_member`, `list_members`, `get_member`, `list_campaigns`, `create_campaign`, `send_campaign` |
| **Zendesk** | api-key Basic + `basic_user_template: "{{conn.email}}/token"` + `connect_inputs: [subdomain, email]` | `https://{{conn.subdomain}}.zendesk.com/api/v2` | `list_tickets`, `get_ticket`, `create_ticket`, `update_ticket`, `add_comment`, `list_users`, `get_user`, `create_user`, `search` |

**Per-provider notes:**
- **Salesforce** API version pinned to `v60.0`; `soql_query` is `GET .../query?q={{soql}}`; write actions POST/PATCH/DELETE `.../sobjects/{{type}}[/{{id}}]` with a fields object body.
- **Shopify** REST Admin API (not GraphQL); orders/customers require the custom app's read scopes; bodies wrap under the resource key (`{product: {...}}`, `{draft_order: {...}}`).
- **Mailchimp** `add_member` POSTs `/lists/{{list_id}}/members` (avoids the MD5-subscriber-hash a PUT upsert would need — no client-side-hash primitive); `update_member` also POSTs to members with status update, or is scoped to fields the plan pins.
- **Zendesk** ticket create/update wraps under `{ticket: {...}}`; comment is part of a ticket update (`{ticket: {comment: {body}}}`).

Mutating actions (create/update/delete/send/comment) set `mutating: true`.

## UI

Add the 4 to `availableServiceProviders`. Shopify/Mailchimp/Zendesk render the api-key card; the
api-key card gains rendering for `connect_inputs` fields (from primitive #1). Salesforce renders the
OAuth card. No other UI change.

## Testing (unit/rendering only; live deferred)

**Primitive tests:**
- `connect_inputs`: `handleConnectAPIKey` stores the collected fields into `extra` (or a focused
  parse test) — assert `{{conn.shop}}` resolves in a Shopify action URL.
- `token_extra`: `OAuthClient` captures `instance_url` from a stub token JSON into `TokenSet.Extra`.
- `key_extra`: dc-from-key split (`abc-us21` → `us21`); handles a key with no `-` (empty dc).
- `applyAuth` templated Basic: `{{conn.email}}/token` + credential → correct Basic header; **plus a
  regression test that a nil-extra caller is unchanged** (Bearer/existing basic still work).

**Provider tests:** LoadBundled for all 4; representative renders with `{{conn.*}}` substituted
(Zendesk ticket under `{ticket:{}}`, Shopify product create under `{product:{}}`, Salesforce SOQL
URL carries `?q=`, Mailchimp base uses `{{conn.dc}}`); registry counts.

**UI:** the api-key card renders `connect_inputs` fields (`handlers_services_test.go` style + build).

## Structure (~6 tasks)

1. `connect_inputs` primitive (provider field + `handleConnectAPIKey` extra storage + template) + **Shopify** provider/actions.
2. `token_extra` primitive (OAuthClient capture + callback merge) + **Salesforce** provider/actions.
3. `key_extra` (dc-from-key) primitive + **Mailchimp** provider/actions.
4. Templated-Basic-username (`applyAuth` signature change + `Execute` + tests) + **Zendesk** provider/actions.
5. UI: add the 4 to `availableServiceProviders` + all-load test.

## Explicitly deferred

Live E2E; Salesforce bulk/composite APIs; Shopify GraphQL Admin API; Mailchimp file/template
uploads; B4 (Stripe/Twilio/Trello); AWS/PostgreSQL (sub-project D).
