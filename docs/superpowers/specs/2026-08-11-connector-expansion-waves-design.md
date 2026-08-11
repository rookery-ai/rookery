# Connector expansion: 91 → ~130, in six independent waves

**Date:** 2026-08-11
**Status:** approved, ready for implementation
**Scope:** Spec D of four. See also: `2026-08-11-reconnect-and-workspace-images-design.md` (A),
`2026-08-11-cli-coder-model-and-ai-providers-design.md` (B),
`2026-08-11-sigv4-auth-kind-and-aws-design.md` (C).

The goal is to pass 100 connector providers with things people actually use, not to pad a
count. Every provider here is data — two YAML files, a logo manifest line, a category —
with the two framework exceptions called out explicitly below.

Each wave is independently mergeable and independently reviewable. Ship them as separate
pull requests; there is no ordering dependency between them.

## What the framework already supports, and what it does not

Three of the candidate providers need auth shapes that already exist and are worth naming,
because each looks harder than it is:

- **Mailgun** wants HTTP Basic with the literal username `api` and the key as the password.
  `basic_user_template: "api"` produces exactly that — the template needs no substitution
  to be useful.
- **Deepgram** wants `Authorization: Token <key>`. That is `value_prefix: "Token "`.
- **AssemblyAI** wants the raw key in `Authorization` with no scheme at all. That is
  `value_prefix: ""` with `header_name: Authorization`.

One shape does **not** generalise, and it disqualifies several otherwise obvious
candidates. `auth.kind: session_exchange` is hardcoded to Bluesky: it posts
`{"identifier": …, "password": …}` and reads `accessJwt` out of the response
(`internal/connectors/dbstore.go:171-195`). The field names are not configurable, and it
can only return a **bearer token** — there is no cookie jar anywhere in the connector
layer.

### One framework change, deliberately small

Generalise `session_exchange`'s field names so it covers a login-for-token flow that is
not Bluesky's:

```yaml
auth:
  kind: session_exchange
  session_url: https://hub.docker.com/v2/users/login
  session_request_fields: { username: "{{conn.username}}", password: "{{credential}}" }
  session_token_path: token          # dotted path, same walker as response_extract
```

Both new fields default to today's Bluesky spelling, so `bluesky.yaml` is untouched and
the existing test keeps passing. This unlocks Docker Hub and GoCardless. It does **not**
unlock the cookie-session providers, which stay excluded.

## Wave 1 — AI services (9)

`openai.yaml` already ships image generation, embeddings, moderation and assistants, so
the category is established as more than a chat passthrough. These follow that precedent:
each earns its place through a capability the coder does not already have.

| Provider | Auth | What an agent gains |
|---|---|---|
| Anthropic | `x-api-key` header + `anthropic-version` static header | Claude access independent of the workspace coder |
| ElevenLabs | `xi-api-key` header | Text to speech |
| Deepgram | `Authorization: Token …` | Speech to text |
| AssemblyAI | raw key in `Authorization` | Transcription with diarisation |
| Replicate | Bearer | Arbitrary hosted models |
| Stability AI | Bearer | Image generation |
| OpenRouter | Bearer | Model routing, and credit/usage checks |
| Perplexity | Bearer | Grounded search answers |
| Hugging Face | Bearer | Inference endpoints, dataset and model metadata |

Category `AI`. New category.

Chat-completions passthrough actions are **not** included for providers where that is all
they would offer — the coder is already an LLM, and a passthrough action is count-padding.
Anthropic and OpenRouter are the exceptions and they are deliberate: a workspace running a
local coder has no other way to reach a frontier model, and OpenRouter's usage endpoint
answers "how much credit is left", which is a real agent task.

## Wave 2 — Cloud-adjacent (8)

All plain Bearer tokens, so all pure YAML. These cover what a self-hoster actually
automates: DNS, droplets, deploys.

Cloudflare, DigitalOcean, Vercel, Netlify, Fly.io, Hetzner, Linode, Railway.

Category `Cloud`, shared with `aws.yaml` from Spec C.

Notes: Cloudflare needs `account_id` and `zone_id` as `connect_inputs`; Fly.io's Machines
API needs the app name per action; Vercel takes an optional `team_id`. Railway is GraphQL —
each action is a fixed query document with substituted variables, which `body:` can
express, but it is the fiddliest of the eight and should be the one dropped if the wave
needs trimming.

## Wave 3 — Money (3, possibly 4)

Finance has three providers today (Stripe, YNAB, Firefly III).

| Provider | Auth | Notes |
|---|---|---|
| Wise | Bearer | Profile id as a `connect_input` |
| CoinGecko | `x-cg-demo-api-key` header | Free tier is generous |
| Alpha Vantage | `apikey` query parameter | Market data |
| GoCardless Bank Account Data | generalised `session_exchange` | **Conditional** — see below |

GoCardless swaps `secret_id` + `secret_key` for an access token, which the generalised
`session_exchange` above should cover. It ships **only if** it works without further
framework changes. If it needs refresh-token rotation on top, defer it: real open banking
is worth its own spec, not a rushed corner of this one.

## Wave 4 — Notifications and email (5)

The category agents most need and that is thinnest today. These are how an agent reaches a
user outside the chat adapters.

| Provider | Auth |
|---|---|
| Pushover | `form:` body; app token as the credential, user key as a `connect_input` |
| Pushbullet | `Access-Token` header |
| Resend | Bearer |
| Mailgun | Basic with `basic_user_template: "api"`; domain as a `connect_input` |
| Matrix | Bearer + homeserver `base_url` with `normalize: base_url` |

Matrix here is a **connector**, not a chat adapter — it lets an agent post to a room. It
does not make Matrix a Rookery chat platform, which remains unimplemented and is a
different piece of work.

## Wave 5 — Homelab (6)

The strongest existing category (21 self-hosted providers). All take a `base_url` via
`connect_inputs` with `normalize: base_url`, which preserves a reverse-proxy path prefix.

| Provider | Auth | Notes |
|---|---|---|
| Prowlarr | `X-Api-Key` header | Direct precedent: Sonarr, Radarr |
| Lidarr | `X-Api-Key` header | Same |
| Bazarr | `X-API-KEY` header | Same, note the spelling |
| Proxmox | `Authorization: PVEAPIToken=<user@realm!id=secret>` via `value_prefix` | Credential is the whole token string |
| Tailscale | Bearer | Tailnet as a `connect_input` |
| Plex | `X-Plex-Token` header | Also accepts the token as a query parameter |

Reaching these at RFC1918 or Tailscale addresses works because `connectors.Execute`
deliberately does not use the private-address dial guard —
`TestExecuteReachesPrivateAddresses` pins that, and its failure message says what breaks.

## Wave 6 — Developer tools (6, possibly 7)

Developer has three providers today (GitHub, Gitea, n8n).

| Provider | Auth | Notes |
|---|---|---|
| GitLab | Bearer | `base_url` for self-hosted instances |
| Bitbucket | Basic via `basic_user_template: "{{conn.username}}"` | App password as the credential |
| Linear | raw key in `Authorization` (`value_prefix: ""`) | GraphQL; fixed query per action |
| Sentry | Bearer | Org slug as a `connect_input` |
| npm registry | `auth.kind: none` | Public package metadata, downloads, versions |
| PyPI | `auth.kind: none` | Public package metadata |
| Docker Hub | generalised `session_exchange` | **Conditional**, same rule as GoCardless |

## Deliberately excluded, with reasons

Recording these matters: for each one, the obvious fix for "it is missing" is to write the
YAML, which would ship a connector that cannot authenticate.

- **qBittorrent** — authenticates by posting to `/api/v2/auth/login` and receiving an
  `SID` **cookie**. The connector layer has no cookie jar and `session_exchange` can only
  return a bearer token.
- **Pi-hole v6** — same shape: a session id returned as a cookie.
- **Uptime Kuma** — has no REST API. It is socket.io only, which nothing here can speak.
- **Transmission** — requires an `X-Transmission-Session-Id` CSRF handshake, where a 409
  response carries the token to retry with. The renderer cannot express a two-step call.
- **Fitbit and Zoom** — stay removed. `TestRemovedProvidersStayRemoved` exists precisely
  because re-adding the YAML is the obvious fix for their absence. Fitbit's API is
  decommissioned; Zoom's connect flow could not be completed against a real account.

Cookie-session support would unlock qBittorrent and Pi-hole together and is a reasonable
future spec. It is out of scope here because it changes how credentials are held per
request, not just how they are formatted.

## Per-provider checklist

Every provider in every wave needs all of:

- `providers/<name>.yaml` with `category:`, and `key_label`/`key_hint` naming the actual
  credential. Not "API key" for something that is an app password or a login token — those
  fields exist because the generic wording was wrong for the providers that take neither.
- `connectors/<name>.yaml` with `mutating: true` on anything that writes, and
  `public_write: true` only on genuinely irreversible public publishing.
- A `scripts/vendor-brand-logos.sh` manifest line. **Never hand-edit
  `web/ui/src/assets/logos/`** — the next run rewrites the whole manifest and silently
  discards the edit. After running it, check `git status` and revert incidental upstream
  churn.
- Confirm the rendered mark is visible on the white tile. A white-on-transparent variant
  passes every structural test and renders as an empty square; prefer a monochrome variant
  where the brand offers one.
- `unverified: true` unless the provider was confirmed against its live API.
  `TestWave1ProvidersDeclareVerificationStatus` requires one or the other.
- A `User-Agent` in `static_headers` for any provider that blocks default clients.
  Wikipedia, Nominatim, Open Library and Open Food Facts all needed one, and every action
  failed until they had it.

## Documentation obligation

The connector provider count is asserted against source by `make docs-sync-check` and
appears in `README.md`, `CLAUDE.md` and the website. Run the `docs-sync` skill before each
wave's pull request — the count changes with every wave, and the count in `README.md`
once drifted for months because it was copied forward instead of measured.

Final expected count: 91 + 9 + 8 + 3 + 5 + 6 + 6 = **128**, plus `aws.yaml` from Spec C =
**129**, plus GoCardless and Docker Hub if their conditional gate passes = **131**.
