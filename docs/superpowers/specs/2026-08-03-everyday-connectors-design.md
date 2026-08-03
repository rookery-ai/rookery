# Everyday Connectors — Design

**Date:** 2026-08-03
**Status:** Approved
**Scope:** A second axis on the connector catalog — services people use in their personal
lives — delivered as nine wave-1 providers, one new auth kind, four new categories, and a
curated candidate list for later waves.

## Goal

Every one of the 45 providers Rookery ships today is a business/SaaS tool: CRM, ticketing,
advertising, commerce, developer infrastructure. Not one is a service a person uses in their
own life. That is a 0% covered axis, not a thin slice of an existing one.

This design opens that axis, prioritising **credential attainability** the way the social/ads
spec did: a provider you cannot authenticate is a provider you cannot test, and the fastest
credentials to obtain are personal API tokens against your own accounts.

Three tiers open at once:

- **Personal cloud SaaS** — a token pasted from a settings page (Todoist, YNAB, Raindrop).
- **Self-hosted / homelab** — a token plus the user's own base URL (Home Assistant, Immich,
  Paperless-ngx). This is Rookery's genuine differentiator: no hosted competitor can reach a
  box on your LAN.
- **Keyless utilities** — no credential at all (Open-Meteo).

## Non-goals

- No new coder capability. Connectors remain data files plus one typed `Execute` path.
- No agency or multi-tenant features. Every credential belongs to the owner's own accounts.
- No interactive per-service UI. Value is delivered through scheduled agents and chat.
- No change to how tools are named, bound, or exposed. `ToolDefs`/`ResolveTool` are untouched.

## Findings that shaped this design

Four facts were established by reading the code and checking live documentation. Each one
changed the design, so each is recorded rather than left implicit.

### `connectors.Execute` does not use the private-address dial guard

`internal/nethttp.GuardedClient` blocks loopback, RFC1918, link-local, CGNAT/Tailscale and
cloud-metadata ranges at **dial** time. It is used by `internal/websearch`, the coder's
`web_fetch`, `web/api_search_keys.go` and the Discord attachment fetcher.

It is **not** used by connectors. `execute.go` falls back to a plain
`&http.Client{Timeout: 30 * time.Second}`, and every call site (`coder/connectortools.go`,
`approval/approval.go`, `bridge.go`, `cmd/livecheck`) passes nil or an unguarded client.

A self-hosted tier is therefore possible today with no framework change. See
"Deliberate SSRF stance" below for how this becomes an explicit decision instead of an
inherited accident.

### Per-connection base URLs already work end to end

Mastodon proved the pattern: `connect_inputs` collects a value before consent, it rides the
signed OAuth state, `Provider.WithConnVars` resolves it into the auth endpoints, and
`{{conn.instance}}` resolves inside action URLs. Shopify does the same on the API-key path
with `{{conn.shop}}`.

The self-hosted tier needs exactly this and nothing more. It is a data exercise.

### Google Calendar and Google Tasks are not covered

The `google` provider is **Gmail-only** — ten actions, every one `gmail_*`. Calendar is the
single most everyday service in existence, it pairs directly with Rookery's existing
scheduler and reminder subsystems, and it is the cheapest provider on this entire list to
build: `auth_parent: google` plus scopes plus one connectors YAML, exactly as
`google_drive` and `google_sheets` already do. The OAuth app is configured and verified.

This was discovered during design, not planned, and it displaces two weaker candidates from
wave 1.

**The cost claim was verified, because it rests on how `auth_parent` resolves scopes.** If an
aliased child shared the parent's token, a workspace that had already connected Gmail would
hold a token carrying Gmail scopes only, and every `google_calendar` action would 403 with
insufficient scope — presenting as a broken connector rather than a missing consent.

It does not work that way. `web/handlers_services.go` calls
`oauth.ConsentURL(clientID, redirectURI, state, child.DefaultScopes)`: the **parent's**
authorize endpoint and app credentials, the **child's own** scopes. The callback stores those
child scopes on the child's own connection row. `oauth.go` states the intent directly —
scopes are passed explicitly "so a child provider aliased via auth_parent can request ITS OWN
scopes against the PARENT's authorize endpoint."

So each child consents separately, existing Gmail connections are untouched, and no
re-consent task is needed. The one non-code prerequisite: the Google Cloud Console OAuth app
must have the Calendar and Tasks scopes registered, or consent fails before any Rookery code
runs.

### The logo-coverage guard exists, but not where its comment says

`web/ui/src/components/brand/logos.ts` states that `logocoverage.test.ts` "asserts every
slug the app can actually render has a file, so a provider added on the Go side without a
logo fails the test run rather than silently degrading to a letter tile."

**No such TypeScript file exists** — but the guard is real: it is
`web/logo_coverage_test.go`, a **Go** test in the `web` package, which enumerates
connector providers, chat platforms, coder providers and web-search providers and fails
when any lacks `web/ui/src/assets/logos/<slug>.svg`. `ProviderLogo.test.tsx` covers only
that vendored assets are well-formed.

So every new provider genuinely does need a logo, and the correction is to the comment,
not to the coverage. It matters practically: three wave-1 providers (YNAB, Raindrop.io,
Open-Meteo) have no mark in lobehub, worldvectorlogo **or** simple-icons, and their own
sites publish no fetchable SVG — every candidate URL 404s. They take a documented
exemption in that test's `allowNoLogo` map rather than an approximated mark, because
drawing someone else's logo ourselves misrepresents their brand, which is worse than a
legible coloured initial.

### Live-documentation corrections

Checked this session, against the vendors' own docs:

| Service | Correction |
|---|---|
| **Oura** | Personal access tokens were **deprecated December 2025**. OAuth2 only. Drops out of the easy tier entirely. |
| **Todoist** | Sync and REST are unified as **API v1** at `api.todoist.com/api/v1`. Personal bearer token from Settings → Integrations → Developer. |
| **Toggl Track** | HTTP Basic with the token as *username* and the literal string `api_token` as *password* — the exact inverse of Rookery's `basic_user_template`, which templates the username and uses the credential as the password. Needs a new field. |
| **Open-Meteo** | Genuinely keyless. 10,000 calls/day, 5,000/hour, 600/minute. Non-commercial only; "personal home automation" is explicitly named as qualifying. CC BY 4.0 attribution required. |
| **Raindrop.io** | Test tokens do not expire. No OAuth app needed for personal use. |
| **AdGuard Home** | HTTP Basic, `username:password`. Fits `basic_user_template` as-is. |

## Wave 1 — nine providers

**Delivered.** All nine ship with ~51 curated actions. The "Verify" column below records
the plan; what was actually achieved is in "Verification outcome" further down.

| # | Provider | Auth | Category | Verify |
|---|---|---|---|---|
| 1 | **Google Calendar** | `auth_parent: google` | Google | Tier A |
| 2 | **Google Tasks** | `auth_parent: google` | Google | Tier A |
| 3 | **Todoist** | Bearer PAT | Productivity | Tier A |
| 4 | **YNAB** | Bearer PAT | Finance | Tier A if subscribed |
| 5 | **Raindrop.io** | Bearer test token | Productivity | Tier A |
| 6 | **Home Assistant** | Bearer LLAT + `base_url` | Self-hosted | Tier A if run |
| 7 | **Immich** | `x-api-key` + `base_url` | Self-hosted | Tier A if run |
| 8 | **Paperless-ngx** | `Authorization: Token` + `base_url` | Self-hosted | Tier A if run |
| 9 | **Open-Meteo** | **none** | Data & Reference | Tier A |

Roughly 50 actions total. `Health & Fitness` lands empty — precedented, since `Advertising`
shipped empty ahead of the Meta/Google Ads providers.

### Action sketches

Every action must narrow its output (see "Extract narrowly" below). Counts are targets, not
contracts.

- **Google Calendar** (7) — `list_calendars`, `list_events` (time range + query),
  `get_event`, `create_event`, `update_event`, `delete_event`, `freebusy`. Scopes:
  `calendar.events`, `calendar.readonly`.
- **Google Tasks** (5) — `list_tasklists`, `list_tasks`, `create_task`, `complete_task`,
  `delete_task`. Scope: `tasks`.
- **Todoist** (6) — `list_projects`, `list_tasks` (filter expression), `create_task`,
  `close_task`, `update_task`, `add_comment`.
- **YNAB** (6) — `list_budgets`, `get_month_summary`, `list_accounts`, `list_transactions`
  (`since_date` required), `create_transaction`, `list_categories`.
- **Raindrop.io** (5) — `list_collections`, `list_raindrops`, `search`, `create_raindrop`,
  `update_raindrop`.
- **Home Assistant** (6) — `list_states` (**domain/prefix filter required**), `get_state`,
  `call_service`, `list_services`, `get_history` (bounded window), `fire_event`.
- **Immich** (6) — `search_assets` (smart search, result cap), `get_asset`, `list_albums`,
  `get_album`, `create_album`, `server_statistics`.
- **Paperless-ngx** (6) — `search_documents`, `get_document`, `get_document_text`,
  `list_tags`, `list_correspondents`, `update_document_tags`.
- **Open-Meteo** (4) — `forecast`, `current_weather`, `air_quality`, `geocode` (place name
  → coordinates; without it every other action demands the model already know lat/lon).

## Framework changes

Four changes plus one deliberate deferral. Each is small and follows an existing precedent.

### 1. `auth.kind: "none"`

`AuthConfig.Kind` already has a non-obvious member — `session_exchange`, added for Bluesky —
so a third kind is an established pattern, not an invention.

- `applyAuth` returns without touching the request when `kind == "none"`.
- The connect flow creates a connection row carrying no credential. A row is still required:
  `ToolDefs(bound)` derives the tool set from bound connections, so a provider with no
  connection exposes no tools.
- `DBTokenStore.AccessToken` returns empty for this kind without attempting a refresh, and
  `RunRefreshLoop` skips it.
- The SPA connector card renders a bare "Connect" button with no key field.
- **Identity.** A keyless connection has no account behind it, so `FetchIdentity` cannot run.
  The row takes the provider's label verbatim ("Open-Meteo") rather than an account
  identifier. A second connection to the same keyless provider is harmless but pointless —
  `ToolDefs` would slug both by label and produce two identical tool sets — so the connect
  endpoint rejects a duplicate for `kind == "none"` instead of relying on the user not to.

**Why this kind is worth five touch-points for one wave-1 provider.** It spans `applyAuth`,
the token store, the refresh loop, the connect endpoint and the SPA card, and serves only
Open-Meteo today. Tier D lists five further candidates (Wikipedia, arXiv, Hacker News,
exchange rates, CoinGecko), so it amortizes — but the sharper argument is that keyless
services have the *highest* everyday value per unit of setup, precisely because there is no
credential to obtain. Weather is the single most requested everyday agent task and it should
not require a signup.

**Why not just let the agent `web_fetch` the same URL.** CLAUDE.md records core skills being
merged away for duplicating native tools, so this deserves an answer. A curated typed action
gives three things `web_fetch` cannot: validated arguments (`latitude`/`longitude` typed and
required, so a malformed call fails before the request), a `response_extract` that returns a
usable forecast rather than a JSON blob against the 8 KiB cap, and a `geocode` action that
turns "Skopje" into coordinates — without which the model must already know the lat/lon it is
asking about. `web_fetch` is also unavailable in chat (it is exec-gated to agent builds and
runs), while connector tools are exposed to chat. The typed action is the difference between
weather working conversationally and not.

### 2. `validCategories` grows by four

`Self-hosted`, `Health & Fitness`, `Finance`, `Data & Reference`, added to the closed set in
`internal/connectors/category_test.go` **and** to the SPA's category ordering. Both, or a
provider 400s on save or renders under the wrong heading — the same two-sided failure
`TestWorkspaceIconSlugsMatchTheSPA` exists to catch elsewhere.

`Self-hosted` groups by auth shape ("runs on my box, needs a base URL") rather than by
domain, unlike the other eight. That is a deliberate inconsistency: it matches how a user
shops this page, and the alternative scatters Home Assistant, Immich and Paperless across
three unrelated headings.

### 3. Shared `base_url` normalization

Three new providers plus Mastodon and Shopify take a user-supplied host, and today the only
thing enforcing its shape is prose in a hint field ("include https://, no trailing slash").
One normalizer, applied at connect:

- require a scheme (`http` or `https`);
- **allow an optional path prefix**;
- strip any trailing slash;
- reject a query string or fragment;
- reject anything that does not parse as a URL.

The path prefix must be allowed, not rejected. `https://host/nextcloud` and a Paperless-ngx
sitting behind a reverse proxy at `/paperless` are mainstream homelab deployments, and
Paperless is in wave 1 — rejecting a path would refuse working installs at connect time.
Action templates read `{{conn.base_url}}/api/...` and concatenate correctly with or without
a prefix, which is exactly why stripping the trailing slash is the load-bearing part.

Rejecting at connect matters because the alternative is a 404 at action time that reads as a
broken connector rather than a mistyped URL.

### 4. Deliberate SSRF stance, documented and pinned

Connectors reach private address space on purpose. Today that is true by accident — no test
asserts it, and the next person to harden HTTP clients across the codebase would silently
break every self-hosted connector.

- A test asserting `connectors.Execute` does **not** use `nethttp.GuardedClient`, naming the
  reason in its failure message.
- A CLAUDE.md paragraph recording the rationale: reaching your own LAN box is the feature,
  and in a single-owner install the request host comes from either vendored YAML or a value
  the owner typed themselves. The threat model that motivates `nethttp` — untrusted content
  steering a fetch — does not apply to a curated action manifest.

This is a stance, not an absence of one. If Rookery ever becomes multi-tenant, it must be
revisited; the test is where that conversation will start.

### 5. Logo vendoring gains a fourth source

`scripts/vendor-brand-logos.sh` draws from lobehub, worldvectorlogo and simple-icons.
Checked against simple-icons (3,450 marks): Todoist, Home Assistant, Immich, Paperless-ngx,
Strava, Toggl, Trakt, Jellyfin, AdGuard, Nextcloud, Sonarr, Radarr, ntfy, Firefly III,
Actual Budget, Wallabag, FreshRSS and Grafana are present. **YNAB, Raindrop.io, Readwise,
Pushover, Open-Meteo, Miniflux, Habitica, Last.fm and TMDB are absent.**

Add an `upstream` source: pinned URLs to each project's own published SVG, in the same
strip-and-commit pipeline. Most of the missing set is open-source with a logo in its own
repository under a permissive licence.

Separately: either write the `logocoverage.test.ts` that `logos.ts` claims exists, or correct
the comment. A comment describing a guard that is not there is worse than no comment, because
it stops the next person from adding one.

### Deferred: the Basic-password literal

Toggl Track needs HTTP Basic with the credential as *username* and a literal constant as the
password. Today `basic_user_template` templates the username and always uses the credential
as the password — the inverse. A `basic_pass_literal` field would fix it, in the same
one-field style as `token_exchange_grant` and `client_param`.

Toggl is not in wave 1, so the field waits until it is. Recorded here so the next person does
not rediscover it.

## Extract narrowly — a per-action requirement

The connector bridge caps a result at 8 KiB (`maxBridgeResult`, mirroring the API engine's
`maxToolResult`). Consumer and self-hosted APIs return far fatter payloads than the business
APIs the existing manifests were written against.

Home Assistant is the clearest case: `GET /api/states` returns **every entity in the house**.
A modest smart home blows the cap on the first call, and the model receives a truncated blob
with a note telling it to narrow a query it has no parameter to narrow.

So, for every wave-1 action:

- `response_extract` must select the useful subtree, never `$`, for any list-shaped response.
- Every list action takes a filter or cap parameter: Home Assistant a domain/entity-id
  prefix, Immich a result cap, Paperless a page size, Calendar a time range, YNAB a
  `since_date`.
- `get_history` and any other unbounded-window action requires an explicit window.

This is a correctness requirement, not an optimisation. An action that reliably truncates is
an action the agent cannot use.

## Verification outcome

**Open-Meteo is live-verified** against the real API — it is keyless, so no credential is
needed and the check runs anywhere. All four actions return correctly narrowed payloads:
geocode 271 bytes, current 178, forecast 362, air quality 121 — every one far under the
8 KiB bridge cap. The test lives behind a `//go:build livecheck` tag so CI never depends
on a third party being reachable.

**The other eight carry `unverified: true`** — no credential was available at build time.
`TestWave1ProvidersDeclareVerificationStatus` fails if a wave-1 provider is neither
verified nor marked, so the honest state is enforced rather than remembered.

## Verification plan

Wave 1 follows the "verify tier A" bar: live-verified providers ship as verified; the rest
ship marked, rather than silently joining the hand-authored-and-unverified pile CLAUDE.md
already records as a known gap.

**Live-verified via `cmd/livecheck` against real credentials:** Google Calendar, Google Tasks
(existing Workspace account and OAuth app), Todoist, Raindrop.io, YNAB, Open-Meteo (keyless,
so verification is free), plus any of Home Assistant / Immich / Paperless-ngx actually
running on the install.

**Marked otherwise:** a provider not live-verified carries an explicit marker in its provider
YAML. The marker is data, so the SPA can surface it later without another schema change.

`cmd/livecheck` is currently **uncommitted**, which has a practical consequence: it exists in
the primary working tree but not on any branch, so a worktree-isolated implementation does
not have it. Committing it is a wave-1 task, not a footnote — the verification bar is
unenforceable without the harness that enforces it. Note also that its `connectors.Execute`
call passes `connectors.Policy{}`, so it predates nothing, but it should be re-checked
against the current signature when it lands.

## Curated catalog for later waves

Auth kinds marked **[v]** were checked against live documentation during this design;
**[?]** must be verified before that provider is built. The distinction is the whole point:
authoring an auth kind from memory is how a provider ends up in the wrong tier.

### Tier A — API key or token, self-serve

| Provider | Auth | Category | Note |
|---|---|---|---|
| Readwise / Reader | `Authorization: Token` **[?]** | Productivity | v2 highlights + v3 Reader |
| Pushover | app token + user key, form POST **[?]** | Communication | $5 one-time licence |
| ntfy | optional token **[?]** | Communication | keyless on ntfy.sh; also self-hostable |
| Toggl Track | Basic, `token:api_token` **[v]** | Productivity | needs `basic_pass_literal` |
| Clockify | `X-Api-Key` **[?]** | Productivity | |
| Habitica | `x-api-user` + `x-api-key` **[?]** | Productivity | two-header auth |
| Steam Web API | key in query **[?]** | Data & Reference | |
| Last.fm | `api_key` in query **[?]** | Data & Reference | |
| TMDB | Bearer v4 token **[?]** | Data & Reference | |
| WakaTime | Basic, base64 key **[?]** | Developer | |
| OpenWeatherMap | key in query **[?]** | Data & Reference | commercial-safe alternative to Open-Meteo |

### Tier B — OAuth, self-serve

| Provider | Category | Note |
|---|---|---|
| Spotify | Publishing & Media | playlists, recently played |
| Strava | Health & Fitness | seeds the category |
| Withings | Health & Fitness | |
| Fitbit | Health & Fitness | |
| Oura | Health & Fitness | **PAT deprecated Dec 2025 [v]** — OAuth only |
| Trakt | Publishing & Media | `client_id` for public data, OAuth for user data |
| Feedly | Productivity | developer token available |

### Tier C — self-hosted (token + `base_url`)

| Provider | Auth | Category |
|---|---|---|
| Jellyfin | `X-Emby-Token` **[v]** | Self-hosted |
| AdGuard Home | Basic `user:pass` **[v]** | Self-hosted |
| Miniflux | `X-Auth-Token` **[v]** | Self-hosted |
| Nextcloud | Basic app password **[?]** | Self-hosted |
| Sonarr / Radarr | `X-Api-Key` **[v]** | Self-hosted |
| Firefly III | Bearer PAT **[?]** | Finance |
| Actual Budget | sync-server token **[?]** | Finance |
| FreshRSS | Google Reader API **[?]** | Self-hosted |
| Grafana | Bearer **[?]** | Self-hosted |
| n8n | `X-N8N-API-KEY` **[?]** | Self-hosted |
| Karakeep | Bearer **[?]** | Self-hosted |
| Wallabag | OAuth, self-hosted **[?]** | Self-hosted |

Uptime Kuma is **excluded**: it has no REST API, only push endpoints.

### Tier D — keyless

| Provider | Category | Note |
|---|---|---|
| Wikipedia / Wikidata | Data & Reference | attribution required |
| arXiv | Data & Reference | |
| Hacker News | Data & Reference | Firebase API |
| Frankfurter / exchange rates | Finance | |
| CoinGecko | Finance | demo key now required for some endpoints **[?]** |

## Implementation sequencing — two plans, not one

There is a clean dependency seam: none of the nine providers can be authored until the four
new categories, the `none` auth kind and the `base_url` normalizer exist. Splitting on it
gives one small fully-testable plan and one repetitive parallelizable plan, instead of a
single plan whose first half gates its second.

**Plan 1 — framework.** The `none` auth kind, the four categories on both sides, the
`base_url` normalizer, the SSRF stance test and CLAUDE.md paragraph, the `upstream` logo
source, resolving the `logocoverage.test.ts` comment, and committing `cmd/livecheck`. Small
surface, every item independently testable, no external credentials required.

**Plan 2 — the nine providers.** Eighteen YAML files (provider + connector per service),
~50 actions, logos, and the tier-A live verification pass. Highly repetitive and
parallelizable once plan 1 has landed.

## Testing

New YAMLs must clear the existing gates — `schema_test.go`, `category_test.go`,
`setup_steps_test.go` (which bans "shown above" prose and requires the `{{redirect_uri}}`
placeholder), and `redirect_policy_test.go`. Beyond those:

- **`auth.kind: "none"`** — `applyAuth` leaves the request untouched; the token store neither
  refreshes nor errors; a connection row is created without a credential; `ToolDefs` exposes
  the provider's actions from that row.
- **Category parity** — every category a provider declares is in the SPA's ordering, and
  vice versa. The failure this prevents is invisible in either file alone.
- **`base_url` normalization** — a table test over schemeless input, trailing slashes, query
  strings and junk, plus positive cases asserting a path prefix (`https://host/nextcloud`)
  survives normalization intact.
- **SSRF stance** — the assertion described above, that connectors do not use the guarded
  client.
- **Extract narrowly** — a manifest test asserting no list-shaped wave-1 action uses
  `response_extract: "$"` and that each declares a filter or cap parameter.
- **Live** — `cmd/livecheck` against real credentials for the tier-A set.

## Risks

- **Open-Meteo's licence is non-commercial.** Fine for a self-hosted personal install, and
  "personal home automation" is explicitly named as qualifying — but it is a licence
  constraint on a shipped product, so the provider YAML must state it and the actions must
  carry the CC BY 4.0 attribution through to the agent. OpenWeatherMap is the commercial-safe
  alternative already listed in Tier A.
- **Self-hosted providers version fast.** Immich in particular has broken its API across
  releases. The `unverified` marker plus a stated tested-against version is the mitigation;
  there is no way to pin a third party's API.
- **`Self-hosted` mixes axes with the other eight categories.** Accepted deliberately, on the
  grounds that it matches how the page is actually browsed.
- **Letter tiles.** Until the `upstream` logo source lands, several providers render as
  initials. Cosmetic, non-blocking, but visible on the page that sells the feature.
- **YNAB is subscription-only.** There is no free tier beyond a trial, so it is the one
  wave-1 provider whose tier-A verification may not be attainable on this install. It stays
  in wave 1 because it seeds the `Finance` category and its auth is trivially simple; if the
  credential cannot be obtained it ships marked, and Firefly III (self-hosted, free) is the
  Tier C alternative already listed.
