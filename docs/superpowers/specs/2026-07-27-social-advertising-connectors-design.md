# Social & Advertising Connectors — Design

**Date:** 2026-07-27
**Status:** Approved
**Scope:** Sixteen new connector providers across social publishing, advertising, and
audience measurement, plus the connector-framework extensions they require.

## Goal

Extend `internal/connectors` to cover social media and advertising platforms on both
sides of the ad economy: the **publisher** side (money earned — AdSense, GA4, Search
Console, YouTube) and the **advertiser** side (money spent — Meta Ads, Google Ads,
LinkedIn Ads, TikTok Ads), plus the social networks themselves for both reading and
publishing.

Agents should be able to report on yesterday's ad spend, summarise channel growth, and
publish a post — using the same typed-tool machinery every existing connector uses.

## Non-goals

- No new coder capability. Connectors remain data files plus one typed `Execute` path.
- No analytics warehouse, scheduling calendar, or content-composition UI. An agent that
  wants to post on a schedule already has the scheduler.
- No multi-tenant / agency features. This is a single-owner install; every credential
  belongs to the owner's own accounts.

## Provider tiering

Providers are tiered by **credential attainability** — whether one person can obtain
working credentials against their own accounts without a partner application. This, not
the social-versus-advertising split, is what determines build order: a provider you
cannot authenticate is a provider you cannot test.

### Tier A — rides the existing Google OAuth app

`auth_parent: google`. No new OAuth app, no approval, no new auth machinery. An
unverified Google app shows a consent warning screen and is capped at 100 users, which
is irrelevant for a single-owner install.

| Provider | Scope |
|---|---|
| `google_adsense` | `adsense.readonly` |
| `google_analytics` (GA4) | `analytics.readonly` |
| `google_searchconsole` | `webmasters.readonly` |
| `youtube` | `youtube.readonly`, `yt-analytics.readonly`, `youtube.force-ssl` (see Delivery status: upload NOT built; commenting shipped instead) |

### Tier B — self-serve, works against own accounts

| Provider | Notes |
|---|---|
| LinkedIn (personal) | `w_member_social` via the "Share on LinkedIn" product is open and self-serve — no approval. Organization posting is not. |
| Facebook Pages | Meta Development Mode grants full permissions to users holding a role on the app. App Review governs access to *other people's* accounts. |
| Instagram | Requires a Professional (Business/Creator) account. Two-step publish: create media container, then `media_publish`. 25 posts / 24h. |
| Threads | Rides the same Meta app. 250 posts / 24h; media must be at a publicly reachable URL. |
| Meta Ads | `ads_read` / `ads_management` against own ad accounts needs no App Review. |
| Bluesky | App password → `createSession` → short-lived JWT. No OAuth app at all. **BUILT** (`session_exchange`). |
| X / Twitter | Pay-per-use since Feb 2026: ~$0.015 per post created, ~$0.005 per post read, no monthly minimum. Basic/Pro closed to new signups. |

### Tier C — real approval gate

These ship as data and connect, but return nothing useful until approval lands. Their
`setup_steps` must state the gate plainly rather than implying connect-and-go.

| Provider | Gate |
|---|---|
| Google Ads | Developer token starts at Test-Account-only. Basic Access is an application; a July 2026 brand-verification pilot can reduce it to hours. |
| LinkedIn Ads | Marketing Developer Platform partner review — reported 1–4 weeks, up to 3–6 months. |
| TikTok | Unaudited clients are forced to `SELF_ONLY`. **Draft / inbox upload needs no audit.** |
| Pinterest | Trial approves in 1–2 days but creates sandbox pins visible only to the creator, capped at 1,000 req/day. Standard requires a demo video, 1–4 weeks. |
| Reddit | Self-service registration is closed; new OAuth clients need manual approval, 2–4 weeks. Free tier is non-commercial only. |

TikTok and Pinterest share a useful property: both work immediately in a constrained,
non-public mode. That maps onto the approval gate below rather than fighting it.

### Deferred

~~**Mastodon** needs per-connection `authorize_url` / `token_url`~~ — **BUILT.** It became
cheap once OAuth-path `connect_inputs` existed: the instance is collected before consent and
rides the signed state, so `Provider.WithConnVars` can resolve the endpoints at both the
consent URL and the callback's token exchange. Providers with literal URLs are unaffected.

## Framework extensions

Six changes, each traced to a specific provider need and a specific file.

### 1. The approval gate (Phase 2)

Publishing is different from every mutating action the system has today. `github_create_issue`
is reversible and private; `linkedin_create_post` is neither. And while `Execute` refuses
mutating actions during a build (the `buildPhase` guard), **a scheduled run has no gate at
all** — a cron agent posts publicly with nothing in the way.

The gate is **optional and defaults to off**, so every existing agent, connector, and test
is unaffected. It has three layers:

**Action level — `public_write: true` in the connector YAML.** Marks actions that publish
irreversibly. `mutating` is too blunt: pausing a Meta Ads campaign is mutating but private
and reversible; only `public_write` actions are ever eligible for gating. Declared in the
data file, preserving "adding a service = YAML only".

**Binding level — `agent_connections.approval_mode`.** `auto` (default) or `approve`. That
join table already means exactly "this agent, this account", which is the right granularity:
one agent can post autonomously to a personal Bluesky while requiring approval on a company
LinkedIn Page. A per-agent flag would be simpler but could not express that, at the same
build cost.

**Runtime — `Execute`'s trailing `buildPhase bool` becomes a `Policy` struct.** There are
four non-test call sites (`bridge.go`, the API engine's connector tools, `cmd/livecheck`,
and the runner's wiring); all pass `Policy{BuildPhase: x}` and behave byte-identically.

**Semantics: park, plain.** When a gated call fires, `Execute` writes the rendered request
to a `pending_actions` row and returns a queue ticket to the coder. The run finishes
normally. The owner approves via `/approve <id>` in chat — a text command through the
existing Router, so no per-adapter button plumbing — or a button in the web inbox
(`inbox_messages` already exists with unread tracking, a poll endpoint, and a navbar badge).
A worker executes the real call and delivers the outcome to the inbox.

Park was chosen over blocking because a blocking `Execute` holds a coder subprocess open at
3am, can have the coder's own timeout fire underneath it, and loses the pending call on
restart.

**Park has three costs, accepted deliberately:**

1. **No chaining.** "Post, then comment on it" needs the post ID from step one. The agent
   has a queue ticket instead, so the second step is impossible within the run.
2. **No error reaction.** If the platform rejects the post at approval time, the agent is
   not running to reword and retry. The error goes to the inbox.
3. **State drift — the one needing an explicit answer.** An agent tracking "which posts have
   I made" in `state.md` would record success when nothing was published; if the owner then
   *rejects*, its state is silently wrong and it will never retry. Mitigation is not left to
   chance: the tool result carries an explicit `"status": "queued_for_approval"` plus a
   `"note"` stating **NOT yet published — do not record this as posted**, and
   `prompts.platformContextBlock` gains a sentence teaching the queued state.

### 2. OAuth-path `connect_inputs` (Phase 5)

`ServiceWizard.tsx` renders `provider.connect_inputs` only inside the API-key branch, gated
behind the `apiKey` field. OAuth providers cannot collect per-connection values.

Only **Google Ads** actually needs this, and only because a developer token cannot be
discovered — it is issued out of band. Every Tier-A identifier is discoverable with the same
scope the reporting call already needs (`accountSummaries.list` for GA4 properties,
`sites.list` for Search Console, `accounts.list` for AdSense, `mine=true` for YouTube), so
those ship as **list actions**, not connect-time configuration. Discovery-by-action is also
more agent-native: the agent enumerates properties and picks one.

### 3. Encrypted `service_connections.extra` (Phase 3)

`extra` is `TEXT NOT NULL DEFAULT ''`, plaintext. That is fine for a Jira cloud id. It is
not fine for a Facebook **Page access token**, which is a credential and is exactly what a
Meta `post_connect` hook resolves. Storing it plaintext would be a downgrade from how every
other token in the system is handled.

**Superseded during Phase 3 implementation — this change is NOT being made.** The premise was
that a Page access token must live in `extra`. It does not: the cleaner model stores the page
token as the connection's OWN access token — `encrypted_access_token`, already encrypted — so a
connection means "this Page" the same way an existing connection means "this account". `extra`
then holds only non-secret identifiers (page id, IG user id, ad account id), which is exactly
what it already holds for Jira's cloud id, and needs no encryption.

The cost of that model is that one connection maps to one Page, resolved by the `post_connect`
hook at connect time. For a single-owner install that is the normal case; managing several Pages
means connecting several times. The alternative — one connection holding a user token plus a map
of page tokens — is what would have forced the encryption change, and it buys multi-Page support
this install does not need yet.

This does mean `post_connect` gains one new capability: replacing the connection's access token,
not just writing to `extra`.

### 4. Meta long-lived token exchange (Phase 3)

Meta does not issue a standard refresh token. A short-lived user token is exchanged for a
~60-day long-lived token via the `fb_exchange_token` grant, and refreshed by re-exchanging
before expiry. `Provider.TokenExpiry` knows only `expiring` and `never`; it gains a third
mode, `exchange`, with the exchange request declared in the provider YAML so `RunRefreshLoop`
handles it without a Meta-specific branch.

### 5. Bridge response byte cap (Phase 1)

`bridge.go` returns `res.Data` uncapped to CLI coders, while the API engine truncates via
`connectortools.go`. A GA4 `runReport` or a 30-day Meta Ads insights call is precisely the
payload that exploits the asymmetry, dumping unbounded JSON into a coder's context. The
bridge gets the same cap and the same truncation notice.

### 6. Bluesky's auth kind (Phase 4) — NOT BUILT

Bluesky is neither `oauth2` nor `api_key`: handle plus app password are exchanged at
`com.atproto.server.createSession` for an access JWT (minutes) and a refresh JWT. This is a
third auth kind — credentials stored like an API key, tokens refreshed like OAuth. It is
isolated to Bluesky, so it is implemented as `auth.kind: session_exchange` with the session
endpoint in the provider YAML rather than as a general mechanism.

### Housekeeping

- **`availableServiceProviders` is a hardcoded slice** in `web/handlers_services.go`, which
  already falsifies the package doc's "adding a service = two YAML files, no Go changes".
  Derived from the registry instead.
- **Sixteen vendored SVG logos.** `logocoverage.test.ts` fails the run otherwise.
- **Provider categories** on the connections page. 28 providers today, 44 after Phase 5; a
  flat list stops being usable well before that.

## Phasing

Ordered by what can be verified working today. Each phase ships something usable on its own.

**Phase 1 — Publisher side + foundations.** Tier A read-only: AdSense, GA4, Search Console,
YouTube reporting. No publishing, so no gate needed. Framework: bridge byte cap,
registry-derived provider list, provider categories, four logos. Testable end-to-end on day
one against the existing Google OAuth app.

**Phase 2 — The approval gate + first publishing.** `Policy`, `public_write`,
`approval_mode`, `pending_actions`, the worker, `/approve`, inbox wiring. Then YouTube
upload, LinkedIn personal posting, Bluesky. Three publishing providers across three
different auth models exercise the gate properly.

**Phase 3 — Meta family.** Encrypted `extra`, the page-token `post_connect` hook, the
`exchange` token mode. Facebook Pages, Instagram, Threads, Meta Ads.

**Phase 4 — Remaining self-serve social.** X, TikTok (draft mode), Pinterest (trial),
Reddit. Each ships with `setup_steps` stating its constraint honestly.

**Phase 5 — Approval-gated advertising.** OAuth-path `connect_inputs`, then Google Ads and
LinkedIn Ads.

The riskiest machinery (Meta) lands third, after the plumbing has been exercised twice
against live providers.

## Response size

Advertising and analytics responses are the largest payloads any connector returns. Three
defences, in order of precedence:

1. **`response_extract` narrows at the source** — report actions extract the rows array, not
   the envelope with its metadata, quota, and schema blocks.
2. **Report actions take a required `limit`** with a low default, and a required date range
   rather than defaulting to all-time.
3. **The bridge cap** (framework change 5) is the backstop for whatever still gets through.

## Error handling

New failures map onto the existing `ConnectorError` taxonomy rather than extending it:

- Approval-gated action while `approval_mode: approve` → a **non-error** result carrying the
  queue ticket. It must not be an `error:` string, which the coder's tool loop treats as a
  failing call worth retrying or blocking on.
- Tier-C provider called before approval lands (TikTok `SELF_ONLY`, Pinterest sandbox,
  Google Ads test-account-only) → `KindOther` with the provider's own message surfaced
  verbatim. These are not bugs and must not read as bugs.
- Meta token exchange failure → `KindNeedsReauth`, same as a failed OAuth refresh.
- Rate limits (Instagram 25/24h, Threads 250/24h, Bluesky ~1,666 records/hour, Reddit 100
  QPM) → `KindRateLimit`, already retried once by `Execute`.

## Testing

Follows the existing connector test pattern — rendering and auth are unit-tested against
recorded shapes; live verification is manual via `cmd/livecheck`.

- **Per-provider render tests** — every action's URL, query, and body render correctly from
  typed args, in the style of `render_test.go`.
- **Registry tests** — every new provider parses, every `auth_parent` resolves, every action
  name is unique and matches the tool-name regex.
- **Approval-gate tests** — `Policy{}` default is byte-identical to today's `buildPhase:
  false`; a `public_write` action under `approve` parks instead of executing; a non-
  `public_write` mutating action under `approve` executes normally; the queue-ticket result
  is not an `error:` string.
- **Bridge cap test** — an oversized `res.Data` is truncated with a notice, matching
  `connectortools.go`.
- **Logo coverage** — `logocoverage.test.ts` already enforces this; it must stay green.

Live verification per provider is a manual step recorded in the plan, not a test. Tier-C
providers cannot be verified until their approvals land, and the plan says so rather than
pretending otherwise.

## Risks

- **Tier-C providers may never be approved.** Google Ads Basic Access and LinkedIn Marketing
  Developer Platform are both discretionary. Phase 5 ships code that may sit inert. This is
  accepted: the data files are cheap and the approval is out of our hands.
- **Meta Development Mode is a moving target.** The own-accounts exemption is current
  behaviour, not a contractual guarantee. If it tightens, Phase 3 providers degrade to
  needing App Review.
- **Park's state-drift cost is mitigated by prompt wording, not by a mechanism.** A
  sufficiently weak model may still record a queued post as published. The follow-up-run
  design that would fix this properly was considered and deferred; `pending_actions` carries
  the agent id from day one so it can be added later without a migration.


## Delivery status

Recorded after implementation so the spec does not read as a description of what exists.
Everything below was verified by the test suite; **nothing was verified against a live
provider API**, which needs real apps on each platform and would publish real content.

**Built:** 45 providers / ~272 actions (up from 28). Google publisher side (AdSense, GA4,
Search Console, YouTube), the approval gate, LinkedIn, Meta Ads, Facebook Pages, Instagram,
Threads, X, Reddit, TikTok, Pinterest, Google Ads, LinkedIn Ads, Bluesky, Mastodon.

**Framework changes built:** the approval gate (1), OAuth-path `connect_inputs` (2), Meta
token exchange (4), the bridge byte cap (5) — plus three not anticipated by this spec:
`post_connect` may replace the connection's access token, `token_exchange_grant` (Threads
uses `th_exchange_token`), and `client_param` (TikTok uses `client_key`). Static header
values are now templated and merge parent-then-child, which Google Ads' developer-token
header required.

**Not built, with reasons:**

- **Encrypting `extra` (change 3)** — superseded; see that section. The Page token became the
  connection's own already-encrypted token, so no secret lands in `extra`.
- ~~**Bluesky's `session_exchange` auth kind (change 6)**~~ — **BUILT.** The app password is
  the durable credential and is swapped for a short-lived accessJwt on use, cached in memory
  for an hour rather than persisted (a stored JWT would be stale more often than fresh, and a
  restart simply re-exchanges).
- **YouTube upload** — `videos.insert` needs a multipart/resumable binary body and the
  framework has no body kind for it. `youtube_post_comment` shipped instead, which exercises
  the approval gate on the same provider.
- **Mastodon** — now BUILT; see the Deferred section.

**Everything in this spec is therefore built except YouTube upload**, which needs a
streaming/multipart body kind. That is a framework capability with real design weight —
buffering a multi-hundred-MB video in memory, and fetching an arbitrary URL from inside the
connector layer (an SSRF surface `internal/nethttp` exists to guard) — not something to
smuggle in as a body builder.
