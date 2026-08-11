# Connector API coverage — deep expansion

**Date:** 2026-08-11
**Status:** design
**Scope:** `internal/connectors` (framework + data files), `internal/db`, `web`

## Problem

The catalog is broad and shallow: 126 providers, ~598 actions — a mean of 4.7
actions per provider. The breadth works; the depth does not. Gmail exposes 10
actions of an API with hundreds. Google Ads exposes 2. LinkedIn exposes 2.
Outlook exposes 5 and cannot touch a calendar, a contact or a file.

An agent asked to trawl a mailbox, file the results in a folder and update a
spreadsheet runs out of connector surface long before it runs out of task. The
goal is to make the priority providers **fully capable** — Google, Microsoft,
Meta and the social/advertising networks — and to raise the long tail as far as
each API allows.

The stated bar is higher than "an action exists": *the tools must work, not
merely be present*. That bar, not the action count, drives this design.

## The dominant failure mode is scope drift, not a wrong URL

A connector action fails silently in production for one reason far more often
than any other: **the connection's token was minted against a `default_scopes`
list that predates the action.** Every live Gmail connection holds a token
carrying the five scopes in `providers/google.yaml` today. Add
`gmail_update_settings` with no further thought and it returns 403 at 03:00
inside a scheduled run, having passed every test in the repository.

Three mechanisms address it, in decreasing order of leverage.

### 1. Prefer a new `auth_parent` child over growing a provider's scope list

This is architectural, free, and removes the problem rather than reporting it.
`google_drive` and `google_calendar` already prove the pattern: a child declares
its own `default_scopes`, `label`, `category` and actions, and inherits the
parent's OAuth app and endpoints. `buildConsentURL` passes the **child's** scopes
to the **parent's** authorize endpoint, so each child consents separately and
existing connections of the parent are never disturbed.

Microsoft's deep coverage therefore arrives as new children of `outlook` —
`onedrive`, `outlook_calendar`, `outlook_contacts`, `excel`, `onenote`,
`microsoft_todo`, `planner` — not as sixty actions crammed into `outlook`. A
user who wants calendar access connects a Calendar card and grants calendar
scopes. Nothing they already connected changes.

The same rule governs Google: new surface lands as `google_slides`,
`google_forms`, `google_chat`, `google_contacts`, never by widening
`google`'s scope list.

Growth of an existing provider's `default_scopes` is permitted only where the
new actions are the same *resource* as the existing ones and splitting would be
artificial — Gmail settings alongside Gmail messages, Drive permissions
alongside Drive files.

### 2. Declare scopes per action, and check the declaration mechanically

Each action gains an optional `scopes:` list naming the OAuth scopes it needs.
A test asserts every action's declared scopes are a subset of its provider's
(parent-resolved) `default_scopes`. This catches at build time the case where
an action is authored against a scope nobody added to the provider.

`scopes:` is optional. An action that omits it is unconstrained, which keeps
every existing action valid and makes adoption incremental.

### 3. Capture the granted scopes, and pre-check with fail-open

Google and Microsoft both return a `scope` field in the token response. The
existing `token_extra` mechanism captures arbitrary token-response fields into
`service_connections.extra` with no new machinery: adding `token_extra: [scope]`
to a provider records what the user actually granted.

`Execute` then compares an action's declared scopes against the connection's
granted scopes and, on a miss, returns a typed error naming the fix —
"reconnect this Google account to grant `gmail.settings.basic`" — instead of
letting an opaque provider 403 reach the model.

**The pre-check fails OPEN when the granted-scope string is empty.** Every
connection that exists today has no recorded scope, and a closed failure would
break working connections on upgrade. This matches the precedent already set by
`definitiveRejection` (one more retry is cheap, a lost connection is not),
`ParkerFor` (a DB error must not halt an autonomous agent) and the absent
`redirect_policy` (the zero `Policy` is permissive).

## The second failure mode: an extract path that never narrowed

`extract` returns the **whole body unchanged** when its dotted path does not
resolve. That is the right runtime behaviour — a third-party payload that
changed shape should degrade, not error — but it means a typo'd
`response_extract` is invisible: the YAML looks correct, every test passes, and
the only symptom is a truncated blob against the bridge's 8 KiB cap. CLAUDE.md
already records this biting twice (`$.data.children`, `$.data.user`), found by
accident both times.

Nothing in the repository currently verifies an extract path against a real
response shape. With the catalog tripling, that gap stops being acceptable.

**Every new action ships a response fixture.** When an API is researched, the
documented example response is captured to
`internal/connectors/testdata/responses/<provider>/<action>.json`, and a test
asserts that applying the action's `response_extract` to its fixture yields a
value that is **not the whole document** (unless the path is `$`) and is
non-empty. This makes both halves of an action offline-testable: the request
shape against the documentation, and the response handling against a real
payload.

Fixtures are required for new actions only. Backfilling all ~598 existing
actions is out of scope; the test skips an action with no fixture, and a
separate test reports the coverage percentage so the gap stays visible.

## The third failure mode: pagination destroyed by extraction

`response_extract: "$.files"` discards `nextPageToken`. The narrowing that keeps
a response under the 8 KiB cap destroys the cursor needed to get the next page,
so a list action can return page one and nothing else — the model has no way to
continue and, worse, no way to know it was truncated.

This must be settled before a single list action is authored, because it affects
every one of them.

**Design:** an optional `response_cursor:` dotted path alongside
`response_extract`. When the cursor path resolves to a non-empty value, the
result is wrapped:

```yaml
response_extract: "$.files"
response_cursor: "$.nextPageToken"
```

```json
{ "items": [...], "next_cursor": "ABC123" }
```

When the cursor is absent or empty the extracted value is returned bare, exactly
as today. So the envelope appears **only when there is genuinely a next page**,
which keeps every existing action byte-identical and means the model sees a
cursor precisely when a cursor is actionable. Every paginated list action pairs
this with a `page_token` parameter feeding the provider's cursor query
parameter.

## The fourth failure mode: response size

Deep coverage means fatter responses, and the bridge caps a result at 8 KiB. Two
YAML-only mitigations are standard on every list action authored here, not
afterthoughts:

- a `fields` / `$select` parameter, wired to the provider's own field-selection
  query parameter, so the model can ask for three columns instead of forty;
- a `max` / `$top` parameter with a documented default, so an unbounded list is
  never the default behaviour.

Where a provider offers neither (rare), the action's description states the
response is large and names the narrowing argument to use.

## Budget: ~40 actions per provider

Tool-list size is a shared budget. CLAUDE.md records this for MCP verbatim — one
server advertising 80 tools "degrades the model's selection across every *other*
tool, connector actions included" — and `mcp.MaxEnabledToolsPerServer` is 48.
The same physics applies to connectors.

**A provider targets ~40 actions and hard-caps at 48**, enforced by a test. If a
provider wants more, the actions are too granular (one `gmail_modify_message`
beats four single-purpose label mutators) or the provider should be split into
`auth_parent` children. Curated at that grain, Gmail's genuinely useful surface
lands around 30.

Per-connection action enablement — a DB table plus UI letting an owner tick
which actions are exposed — was considered and **rejected for this work**. It is
a sub-project bolted onto an already-large one, and the child-provider split plus
the existing per-agent binding already keep a realistic agent's tool list in
range. Revisit only if a provider genuinely cannot fit its useful surface in 48.

## What stays excluded

Unchanged, and worth restating because the obvious fix for each is to write the
actions:

- **Binary and XML payloads.** `Result.Data` is a `json.RawMessage` and `extract`
  returns a non-JSON body *unchanged*, so a response of image bytes or XML
  corrupts the envelope rather than merely failing to narrow. This has now bitten
  three times (S3, ElevenLabs/Stability, Plex). It rules out media *download*
  actions across the board, and Microsoft Advertising entirely (SOAP).
- **File upload of local bytes.** Instagram, Threads, TikTok and Pinterest all
  require a publicly reachable URL; the connector layer has no upload path and
  the agent has no public host. Actions state this in their description rather
  than pretending otherwise.
- **Credentials in a request body.** Still unsupported; no provider in this work
  needs it.

## Honesty about what an unreviewed app can do

Several priority providers gate their most valuable actions behind a review the
user has not passed. An action that cannot work on a fresh developer app is
"present and not working" unless its description says so plainly. This is a
documentation obligation on every affected action:

| Provider | Gate |
|---|---|
| Google Ads | developer token starts at test-account access; returns nothing for real accounts until Basic Access is approved |
| LinkedIn | posting to a **company page** needs Marketing Developer Platform (partner application); this connector posts as the member |
| LinkedIn Ads | reporting needs Marketing Developer Platform |
| TikTok | direct publish needs audit; unaudited apps post `SELF_ONLY` to the inbox |
| Pinterest | trial access creates sandbox pins visible only to the owner |
| Reddit | new OAuth clients need manual approval; free tier is non-commercial |
| X | pay-per-use since Feb 2026 |
| Meta | Development-mode apps reach only assets the developer administers |

Existing providers already carry this in `setup_steps`. New actions repeat the
relevant line in their own `description`, because the model reads the action
description and never reads the setup steps.

## Advertising is the hardest surface, and needs a different shape

Google Ads is not a REST API with many endpoints — it is **GAQL over a single
`:search` endpoint**. `google_ads_search` already exposes the whole API in one
action, so "more capable" cannot mean more endpoints. It means **canned report
actions** whose GAQL is written for the model: `google_ads_campaign_performance`,
`google_ads_search_terms`, `google_ads_keyword_performance` — each a typed
action over a fixed, correct query with date-range and metric parameters. The
generic `search` stays as the escape hatch.

Meta Ads is the opposite: a genuine REST tree (accounts → campaigns → ad sets →
ads → creatives → insights) where depth means real endpoints, plus an insights
action whose breakdowns and fields need proper parameters.

## Delivery: six pull requests

A single 600-action PR is unreviewable and would pressure the 900s `-race` test
budget. The work ships as six, each independently green and mergeable:

| PR | Contents |
|---|---|
| 1 | Framework: `scopes:`, granted-scope capture + fail-open pre-check, `response_cursor:`, fixture harness, action cap test |
| 2 | Google: Gmail, Drive, Sheets, Docs, Calendar, Tasks, YouTube + new children |
| 3 | Microsoft: Outlook mail deep, Teams, + seven new `auth_parent` children |
| 4 | Meta + social: Facebook, Instagram, Threads, X, LinkedIn, TikTok, Pinterest, Reddit, Mastodon, Bluesky |
| 5 | Advertising + analytics: Google Ads, Meta Ads, LinkedIn Ads, GA4, Search Console, AdSense |
| 6 | Long tail: raise the remaining providers as far as their APIs allow |

PR 1 must land first — every later wave depends on `scopes:`, `response_cursor:`
and the fixture harness.

## Documentation obligation

`make docs-sync-check` counts providers and actions **against source**. The
`126 providers (~598 actions)` figure in `CLAUDE.md` and `README.md`, the
provider tables on the documentation site and the landing page's counts all move
with every wave. The `docs-sync` skill runs per PR, not once at the end.

## Testing

Existing catalog-hygiene tests already enforce a great deal and apply
automatically to every new action: valid and globally-unique tool names, a
description over 20 characters, a declared params schema, and — the two that
matter most here — that every required *and* every optional parameter is
actually referenced by the request. An action offering a parameter its request
ignores fails the build.

New tests added by PR 1:

1. **`TestActionScopesAreDeclaredByTheProvider`** — every action's `scopes:` ⊆ the
   parent-resolved `default_scopes`.
2. **`TestResponseExtractResolvesAgainstItsFixture`** — for every action with a
   fixture, the extract path narrows and yields non-empty.
3. **`TestPaginatedActionsExposeAPageToken`** — an action declaring
   `response_cursor` also offers a `page_token` parameter.
4. **`TestProviderActionCountStaysWithinBudget`** — ≤ 48 actions per provider.
5. **`TestFixtureCoverageOfNewActions`** — reports fixture coverage; fails if a
   provider touched by this work has none.

Live verification (`zz_live_*_test.go`, `//go:build livecheck`) is extended for
the providers where a real connection exists on the development host. It stays
excluded from the normal run so CI never depends on a third party.

## Success criteria

- Priority providers reach the depth an agent needs: Google, Microsoft, Meta,
  the social networks and the advertising platforms each expose their genuinely
  useful surface rather than a token sample.
- No existing connection breaks. No action is added that a connection's existing
  grant cannot call without the user being told, in words, to reconnect.
- Every new action carries a response fixture proving its extract path narrows.
- Every action gated behind a provider review says so in its own description.
- Six green PRs, documentation synchronised with each.
