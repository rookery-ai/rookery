# Connector Catalog B1: Google-family + Teams via `auth_parent` alias

**Date:** 2026-07-14
**Status:** Design approved; ready for implementation plan
**Package:** `internal/connectors` (+ `internal/db` for parent-resolved config lookup, `web/handlers_services.go`)

## Context

The connector **Foundation** (sub-project A, merged to `main`) added API-key auth + a generic
nested/array request-body engine, and ships providers google/github/notion/outlook/jira/slack/openai.

The larger **Catalog** effort (sub-project B) adds the remaining ~23 services from the owner's
Phase 1/2 list. B is itself too big for one spec and is split into four batches by auth/body shape:

- **B1 — Reuse existing OAuth** *(this spec)*: Google Drive, Sheets, Docs (share the `google`
  login), Teams (shares the `outlook`/MS-Graph login). Zero-to-minimal engine change.
- **B2 — New OAuth/API-key JSON providers**: HubSpot, Dropbox, Zoom, Calendly, Asana, ClickUp,
  Airtable, Intercom, SendGrid, Monday. Pure data on the current engine.
- **B3 — Per-connection base-URL providers**: Salesforce, Shopify, Zendesk, Mailchimp (need a
  `post_connect` resolver — mechanism already exists for Jira's cloudid).
- **B4 — Engine-work providers**: Stripe, Twilio (form-encoded bodies; Twilio two-part basic auth),
  Trello (OAuth 1.0a).
- **Deferred to sub-project D**: AWS (SigV4), PostgreSQL (wire protocol).

This spec covers **B1 only**. Each batch is its own spec → plan → build cycle.

## Decisions (from brainstorming)

- **Model:** separate service cards/providers that **share one login** (the owner's mental model;
  per-service agent binding; no tool-list bloat). Chosen over "more actions on the existing Google
  card" and over "fully independent providers with their own OAuth app."
- **Mechanism:** an `auth_parent` alias (see Architecture) — a child provider reuses the parent's
  OAuth app + endpoints while keeping its own scopes/actions/label and its own connection rows.
- **Action depth:** ~8-10 high-value actions per service (~30-40 total).
- **Verification:** unit/rendering only for B1; **all live calls deferred** to a later live pass
  (matches how outlook/jira shipped).

## Architecture: the `auth_parent` alias

### New field

`Provider` (in `registry.go`) gains:

```go
// AuthParent names another provider whose OAuth app + endpoints this provider reuses.
// A child provider (e.g. google_drive → google) declares only its own scopes/actions/label;
// its authorize_url/token_url/token settings/app credentials all resolve from the parent.
AuthParent string `yaml:"auth_parent"`
```

### One resolver, three call-sites

Add:

```go
// OAuthProvider returns the provider whose OAuth config governs authentication for name:
// the auth_parent when set, else the provider itself. Used for endpoints, token settings,
// static_headers, authorize_extra, and the app-credentials lookup key.
func (r *Registry) OAuthProvider(name string) (Provider, bool)
```

`ProviderByName` continues to return the **child** as-is (its scopes, label, actions, post_connect).
`OAuthProvider` returns the **parent** for OAuth mechanics. The three call-sites that switch to
`OAuthProvider`:

1. **Consent URL** (`web` connect handler): build from the parent's `authorize_url` +
   `authorize_extra`, using the **child's** `default_scopes`, and add `include_granted_scopes=true`
   so re-consent for a second service keeps the first service's grant (Google + MS Graph both
   support incremental consent).
2. **OAuth-app credentials:** `service_provider_configs` stays keyed to the **parent** (`google`).
   The app client id/secret are saved once on the Google/Outlook card; children resolve them via
   the parent. The credentials-lookup key is `OAuthProvider(child).Name`.
3. **Token refresh:** `DBTokenStore.refresh` resolves `auth_parent` for BOTH the endpoints
   (`OAuthProvider`) and the provider-config lookup (parent's `service_provider_configs` row).

### Connections & binding — unchanged

Each service keeps its **own** `service_connections` row (`provider: google_drive`) with its own
token obtained from its own consent. `agent_connections` (connection-keyed) therefore binds
per-service with no change. `applyAuth` needs **no change**: a child provider has no `auth:` block,
so it takes the default OAuth `Authorization: Bearer <token>` path using its own row's token. The
one `Execute` tweak is to resolve its per-request `static_headers` via `OAuthProvider` rather than
`ProviderByName` (a 1-line change) so a child inherits the parent's static headers — **moot for B1**
(google and outlook define none) but kept correct for future aliased providers.

### Net new code

One struct field + one `OAuthProvider` resolver + three ~2-line resolution tweaks (consent URL,
creds lookup, refresh). Everything else in B1 is data files + UI wiring.

## Provider & action files

### New provider files (aliased)

| File | `auth_parent` | Added scopes |
|---|---|---|
| `providers/google_drive.yaml` | `google` | `https://www.googleapis.com/auth/drive` |
| `providers/google_sheets.yaml` | `google` | `https://www.googleapis.com/auth/spreadsheets` |
| `providers/google_docs.yaml` | `google` | `https://www.googleapis.com/auth/documents` |
| `providers/teams.yaml` | `outlook` | `Team.ReadBasic.All`, `Channel.ReadBasic.All`, `ChannelMessage.Read.All`, `ChannelMessage.Send` |

Each carries `label`, `setup_url`/`setup_steps` pointing back to the parent's console, and (for
Google) inherits `access_type=offline`/`prompt=consent` from the parent's `authorize_extra` plus the
new `include_granted_scopes=true`.

### Actions (~8-10 each; final selection Composio-verified in the plan)

- **Drive** (`connectors/google_drive.yaml`): `list_files`, `search_files`, `get_file`,
  `create_folder`, `copy_file`, `move_file`, `rename_file`, `share_file` (permissions.create),
  `delete_file` (mutating), `export_file`.
- **Sheets** (`connectors/google_sheets.yaml`): `get_values`, `append_values`, `update_values`,
  `clear_values`, `create_spreadsheet`, `add_sheet`, `get_metadata`, `batch_update` (nested body).
- **Docs** (`connectors/google_docs.yaml`): `create_document`, `get_document`, `insert_text`,
  `replace_text`, `append_text`, `batch_update` (nested `requests[]` array).
- **Teams** (`connectors/teams.yaml`): `list_joined_teams`, `list_channels`, `get_channel`,
  `send_channel_message` (nested `body.content`, mutating), `list_channel_messages`,
  `reply_to_message` (mutating), `list_members`.

Mutating actions (`delete_file`, `send_channel_message`, `reply_to_message`, and any write the plan
marks) keep the build-time guard.

### Gotchas (handled explicitly in the plan)

- **Drive file upload = multipart/form-data** → **deferred** (same as `openai_upload_file`);
  `create_folder`/`copy_file` cover creation without raw upload.
- **Drive `export_file`/download returns binary** → return the export/download **link** or a capped
  text export, not raw bytes.
- **Sheets & Docs `batchUpdate`** use nested objects/arrays → already supported by `renderBody`
  (this is what it was built for).
- **Teams** message bodies are nested JSON → supported by `renderBody`.

## UI

- Add `google_drive`, `google_sheets`, `google_docs`, `teams` to `availableServiceProviders`.
- `showServices` renders a child card showing connection status + a **Connect** button that reuses
  the parent's app creds (resolved via `auth_parent`). Child cards show **no** client-id/secret
  form. If the parent has no creds yet, the child card links to the parent (Google/Outlook) card to
  set them first.
- `handleConnectService` resolves `auth_parent` to build the parent-based consent URL with the
  child's scopes + `include_granted_scopes=true`.
- The connected-accounts list renders unchanged.

## Testing (unit/rendering only; live deferred)

- **Load + alias resolution:** all four child providers load; `OAuthProvider("google_drive")`
  resolves to `google` (endpoints/creds key) while `ProviderByName("google_drive")` keeps the
  child's scopes/actions/label.
- **Consent-URL resolution:** the `google_drive` consent URL uses `google`'s `authorize_url` +
  client id, carries the child's `drive` scope, and includes `include_granted_scopes=true`.
- **Creds + refresh resolution:** `DBTokenStore.refresh` for a `google_drive` connection looks up
  the parent `google` provider-config + endpoints (fake OAuth client, mirroring
  `TestAccessTokenRefreshesNearExpiry`).
- **Body rendering:** table tests for `sheets.batch_update`, `docs.batch_update` (`requests[]`
  array), `teams.send_channel_message`, and `sheets.append_values`/`update_values` arrays.
- **Schema/`validateArgs`:** the array/nested params above.
- **Registry counts:** each new provider exposes its expected action count.
- **UI:** `showServices` renders child cards; a child card's `HasCreds` resolves via `auth_parent`;
  `handleConnectService` for a child builds the parent-based consent. Follows
  `handlers_services_test.go` style.

## Explicitly deferred

- Live E2E for all four (Google-family against the owner's Google account; Teams against an M365
  tenant) + `cmd/livecheck` plans — a later live pass.
- Drive multipart upload + binary download.
- Batches B2/B3/B4; connectors-page visual redesign (sub-project C); AWS/PostgreSQL (sub-project D).
