# Connector Catalog B2: 10 new JSON providers (token-first)

**Date:** 2026-07-14
**Status:** Design approved; ready for implementation plan
**Package:** `internal/connectors` (+ `web/handlers_services.go` provider list)

## Context

Sub-project B (Catalog) adds the remaining services on the merged Foundation engine (API-key +
OAuth auth, generic nested/array `renderBody`). Batch **B1** (Google Drive/Sheets/Docs + Teams via
`auth_parent`) is merged. This spec is **B2**: 10 standalone providers that need **no new engine
mechanism** — standard OAuth2 or personal-token auth + JSON bodies.

Providers: **HubSpot, Dropbox, Zoom, Calendly, Asana, ClickUp, Airtable, Intercom, SendGrid, Monday.**

Deferred to later batches: **B3** (per-connection base-URL providers: Salesforce, Shopify, Zendesk,
Mailchimp) and **B4** (engine-work: Stripe, Twilio form-encoding; Trello OAuth1). AWS/PostgreSQL →
sub-project D.

## Decisions (from brainstorming)

- **Auth: token-first.** Use simple personal-token/API-key auth for the 8 providers that offer one
  (paste a token, no per-provider OAuth app to register); use OAuth2 only for **Dropbox** and
  **Zoom**, which lack a simple static token.
- **Action depth:** ~8-10 high-value actions per provider (~80-100 total).
- **Verification:** unit/rendering only; live E2E deferred (as B1).
- **No new engine code:** everything uses existing provider-YAML fields (`auth.kind`, `placement`,
  `value_prefix`, `header_name`, `static_headers`, `token_auth`, `authorize_extra`) + `renderBody`.

## Provider + auth configuration

| Provider | `auth.kind` | Auth detail |
|---|---|---|
| HubSpot | api_key | `Authorization: Bearer <private-app token>` (`value_prefix: "Bearer "`) |
| Calendly | api_key | `Authorization: Bearer <PAT>` |
| Asana | api_key | `Authorization: Bearer <PAT>` |
| Airtable | api_key | `Authorization: Bearer <PAT>` |
| SendGrid | api_key | `Authorization: Bearer <API key>` |
| Intercom | api_key | `Authorization: Bearer <token>` + `static_headers: {Intercom-Version: "2.11"}` |
| ClickUp | api_key | `Authorization: <token>` — **no** Bearer prefix (`value_prefix: ""`) |
| Monday | api_key | `Authorization: <token>` (`value_prefix: ""`); GraphQL endpoint (see below) |
| Dropbox | oauth2 | Bearer; authorize `https://www.dropbox.com/oauth2/authorize`, token `https://api.dropboxapi.com/oauth2/token`; `authorize_extra: {token_access_type: offline}` for a refresh token |
| Zoom | oauth2 | Bearer; authorize `https://zoom.us/oauth/authorize`, token `https://zoom.us/oauth/token` with **HTTP Basic** client auth (`token_auth: basic`, like Notion) |

Each api_key provider carries `key_label`/`key_hint`/`setup_url` for the paste-key UI (added in the
Foundation). Each OAuth provider carries `label`/`setup_url`/`setup_steps` for the OAuth-app UI.

## Actions (~8-10 each; final selection Composio-verified in the plan)

All JSON bodies via `renderBody`. Representative sets:

- **HubSpot:** search/list/get/create/update contacts; list/create companies; list/create deals.
- **Asana:** list tasks, get task, create task, update task, complete task, list projects, add comment, list workspaces.
- **Airtable:** list records, get record, create record, update record, delete record, list bases, list tables (metadata).
- **SendGrid:** send mail (`/v3/mail/send`, nested `personalizations[]`/`content[]`), list templates, get template, list suppressions/bounces, list verified senders.
- **Calendly:** get current user, list event types, list scheduled events, get event, list invitees, cancel event.
- **Zoom:** list meetings, create meeting, get meeting, update meeting, delete meeting, list users, get user, list recordings.
- **Dropbox:** list folder, get metadata, create folder, move, copy, delete, create shared link, search. (File **upload** = multipart → deferred.)
- **ClickUp:** list tasks, get task, create task, update task, list lists, list spaces, add comment, list folders.
- **Intercom:** list contacts, get contact, create contact, update contact, list conversations, get conversation, reply to conversation, create note.
- **Monday** (GraphQL): list boards, get board items, create item, update item column values, create update (comment), list groups, list users, archive item. Each action is a fixed GraphQL query/mutation in the body `{query, variables}` (a normal JSON body; no special engine code). Endpoint `https://api.monday.com/v2`.

Mutating actions (create/update/delete/send/cancel/reply/archive) keep the build-time guard
(`mutating: true`).

### Deferrals (consistent with prior batches)
Dropbox and Airtable **file uploads** (multipart) are out of scope; link/metadata/record actions
cover the value. SendGrid inbound/complex marketing-campaign actions out of scope.

## UI

Add the 10 providers to `availableServiceProviders`. The 8 api_key providers render the existing
paste-key form (Foundation); Dropbox + Zoom render the existing OAuth creds+connect form. No new UI
code — the auth-kind-aware card from the Foundation already handles both.

## Testing (unit/rendering only; live deferred)

- **LoadBundled parse** — all 10 providers + connector manifests load; api_key providers report
  `IsAPIKey()`, OAuth providers do not.
- **Auth rendering** — `applyAuth` table check for a `value_prefix: ""` provider (ClickUp/Monday →
  raw token in `Authorization`) and a `static_headers` provider (Intercom → `Intercom-Version`).
- **Body rendering** — representative bodies: SendGrid nested `personalizations[]`/`content[]`,
  Monday GraphQL `{query, variables}`, HubSpot/Airtable create-record objects.
- **Schema/`validateArgs`** — array/object params where used.
- **Registry counts** — each provider exposes its expected action count.

## Structure

One task per provider (provider YAML + connector YAML + tests) + one UI task
(`availableServiceProviders`) = ~11 tasks. Cohesive; one spec/plan.

## Explicitly deferred

Dropbox/Airtable multipart uploads; live E2E; B3 (base-URL providers); B4 (Stripe/Twilio/Trello);
AWS/PostgreSQL (sub-project D).
