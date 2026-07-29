# OAuth redirect reliability — design

**Date:** 2026-07-29
**Status:** approved, pending implementation plan

## Problem

OAuth connector setup is currently unusable through the UI, and when it fails it fails
opaquely. Four defects compound:

1. **The redirect URI is never displayed.** Eighteen OAuth providers' `setup_steps`
   instruct the user to register "the redirect URI shown above"
   (`internal/connectors/providers/google.yaml:11`, `github.yaml:9`, `slack.yaml:21`, …).
   Nothing shows it. The provider DTO (`web/api_services.go:47-58`) has no such field and
   `ServiceWizard.tsx` never renders one. A user cannot learn the string they must register
   without reading Go source. **This blocks OAuth on every deployment, including localhost.**

2. **The URI is an emergent property of the browser's Host header.**
   `publicBaseURL` (`web/handlers_services.go:87`) returns `SA_PUBLIC_URL` or
   `c.Scheme() + "://" + c.Request().Host`. There is no stable answer to "what should I
   register?" — it changes with how the operator reached the page.

3. **It is computed twice from two different requests and never pinned.**
   `buildConsentURL` derives it at `:148`; `ExchangeCode` re-derives it at `:250` from the
   *callback* request. The signed state carries `workspaceID~provider~label~nonce~inputs` —
   not the URI. When the two differ, the token exchange fails `redirect_uri_mismatch`
   *after* consent succeeded, which reads as a provider fault.

4. **Nothing checks the URL against what the provider will accept**, and failures surface as
   `"Token exchange failed: " + err.Error()` (`:254`) dumped into a query string.

`SA_PUBLIC_URL` compounds all of it: it is unvalidated (`strings.TrimRight(b, "/")` is the
only processing, so a schemeless value silently produces a broken URI), and it is mandatory
behind any reverse proxy — `c.Request().Host` ignores `X-Forwarded-Host` while `Scheme()`
honours `X-Forwarded-Proto` — yet it appears in no shipping artifact: not the `Dockerfile`,
not `packaging/systemd/simple-agents.service`, not `packaging/README.md`.

## Goals

- A user who cannot read Go source can complete an OAuth connection.
- A user whose configuration *cannot* work learns this **before** creating an OAuth app,
  registering a URI and clicking through consent — and is told the specific remedy.
- Consent and token exchange can never disagree about the redirect URI.
- Correctness is locked in by table tests over pure functions, not by manual OAuth dances.

## Non-goals

- **TLS in the server.** HTTPS-only providers (Slack, Meta, LinkedIn, X, TikTok, …) still
  require the operator's own reverse proxy. We detect and explain; we do not half-solve.
- Changing the callback route. `/dashboard/connectors/services/callback/:provider` is a
  registered external redirect URI and stays frozen (pinned by `web/spa_test.go:108`).
- Making every provider work everywhere. Reliability here means *never being confused about
  why something failed* — not universality.

## Design decisions

| Decision | Choice | Rationale |
|---|---|---|
| URL source | One configured instance value, detection as default | Kills the "depends which URL you browsed from" class of bugs; gives a stable answer to "what do I register?" |
| HTTPS-only providers | Diagnose + document only | No new transport, no new attack surface, honest about the limit |
| Enforcement | Hard-block provable failures, warn on uncertain ones | A wrong policy entry in our YAML must never lock a user out of a provider that would have worked |

### Placement refinement

The public URL is **instance-level**, so it does not belong in the setup wizard — that
wizard's five steps (Basics → Master password → Coder → Profile → Chat app) are all
*workspace*-scoped and would ask for it once per workspace. It goes in **owner/admin
settings** (`GET`/`PUT /api/v1/admin/settings`, rendered by
`web/ui/src/pages/settings/OwnerSections.tsx`), persisted in the `system_settings` table —
which already has `GetSystemSetting`/`SetSystemSetting` helpers (`internal/db/repositories.go:1103`)
and currently zero callers, so no migration is required.

Configuration stays **optional**. The resolved value defaults to detection, and the user is
prompted to set it contextually — from the connection preflight, at the moment it matters —
rather than being made to answer a question they don't yet understand.

## Components

### 1. `internal/publicurl` (new package)

One purpose: own the instance's public base URL and judge it against a provider's policy.

```go
type Source int // SourceConfigured | SourceEnv | SourceDetected

func Resolve(d *db.DB, detected string) (base string, src Source)
func Detect(c echo.Context) string          // today's inference, isolated
func Normalize(raw string) (string, error)  // require scheme; reject path/query/fragment; trim trailing "/"
func Check(base string, p Policy) []Problem // pure: no I/O, no DB, no request
```

```go
type Severity int // SeverityHard | SeveritySoft

type Problem struct {
    Severity Severity
    Code     string // "malformed_url" | "scheme_not_https" | "raw_ip" | "non_public_host"
    Message  string // what is wrong, in plain language
    Fix      string // what to do about it
}
```

`Check` being pure over `(URL × Policy)` is the load-bearing choice: it is the one place
correctness is asserted, and it is exhaustively table-testable.

`Normalize` fixes defect (4) for `SA_PUBLIC_URL` — it is applied to the env var and the
configured value alike, and a malformed value is a **startup error**, not a silent
mis-render.

### 2. Host classification

Four classes, derived once and shared by every rule:

| Class | Test |
|---|---|
| `loopback` | host is `localhost`, `127.0.0.0/8`, or `::1` |
| `raw_ip` | host parses as an IP literal and is not loopback |
| `reserved_host` | TLD in the RFC-reserved deny-list (below), or the host has no dot |
| `public_domain` | `publicsuffix.PublicSuffix(host)` returns `icann == true` |
| `uncertain` | anything else |

**Why a deny-list *and* the PSL.** `golang.org/x/net` is already a direct dependency
(`go.mod:18`), so `publicsuffix` costs nothing — and it encodes exactly the rule Google
enforces ("must use a domain that is a valid top private domain"). But the `icann` flag
alone cannot carry the decision. Verified behaviour:

```
agents.example.com   suffix=com         icann=true    → public
agents.rookie.lan    suffix=lan         icann=false   → PSL fallback, NOT a real suffix
agents.github.io     suffix=github.io   icann=false   → a real PSL *private* entry
```

`.lan` and `github.io` both return `icann=false`, so treating that as failure would reject
`github.io`, which genuinely works. Therefore:

- `icann == true` → **public**, no problem.
- TLD in the RFC-reserved deny-list (`.local`, `.lan`, `.home`, `.internal`, `.test`,
  `.invalid`, `.example`, `.localdomain`, `.home.arpa`) or no dot at all → **reserved**,
  provably unusable.
- Everything else (private-PSL entries, unknown TLDs) → **uncertain** → soft only.

This asymmetry is deliberate and is what makes the hard-block safe: we hard-block only what
is reserved by RFC, never merely what we failed to recognise.

### 3. Redirect policy as provider YAML

A new optional block on `connectors.Provider`, matching the codebase's "a new service is two
YAML files and no Go change" ethos:

```yaml
redirect_policy:
  scheme: https_or_loopback    # https | https_or_loopback | any     (default: any)
  allow_raw_ip: loopback_only  # yes | no | loopback_only            (default: yes)
  allow_reserved_host: false   # .lan / .local / dotless             (default: true)
  verified: true               # confirmed against live provider docs (default: false)
```

**An absent block means unverified, which means soft warnings only.** The feature therefore
ships incrementally: the four providers verified against live documentation get policies now;
the other fourteen stay soft until someone verifies them.

| Provider | scheme | allow_raw_ip | allow_reserved_host | Source |
|---|---|---|---|---|
| `google` | `https_or_loopback` | `loopback_only` | `false` | [Google OAuth 2.0 for Web Server Applications](https://developers.google.com/identity/protocols/oauth2/web-server), [Manage OAuth Clients](https://support.google.com/cloud/answer/15549257) |
| `github` | `any` | `yes` | `true` | [Authorizing OAuth apps](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps) |
| `notion` | `https_or_loopback` | `yes` | `true` | [Notion authorization](https://developers.notion.com/guides/get-started/authorization) |
| `slack` | `https` | `no` | `true` | [Installing with OAuth](https://docs.slack.dev/authentication/installing-with-oauth/) |

Google's policy inherits to its seven `auth_parent: google` children (Drive, Sheets, Docs,
AdSense, GA4, Search Console, YouTube) through the existing `OAuthProvider()` resolution —
policy is read from the OAuth parent, not the child, exactly as endpoints and credentials are.

`Severity` is then a pure function of the policy: violation + `verified: true` → **Hard**;
violation + `verified: false` → **Soft**.

### 4. Pin the redirect URI into the signed state

The state payload gains a sixth field:

```
workspaceID ~ provider ~ label ~ nonce ~ inputsB64 ~ redirectURI
```

`handleOAuthCallback` uses the **pinned** URI at `ExchangeCode` instead of recomputing it
from the callback request. Consent and exchange then agree by construction. The handler
already accepts 4- and 5-field shapes for states in flight across a deploy
(`handlers_services.go:216-218`); 6 follows that precedent, and the existing HMAC covers the
new field with no change to `signState`/`verifyState`.

If a state's pinned URI disagrees with the currently-resolved one, that is itself a
diagnosable condition — report it as "this instance's URL changed after you started;
start the connection again" rather than letting the provider reject it.

### 5. Failure taxonomy

Replace the raw `err.Error()` dump with a mapper from provider error to remedy:

| Provider signal | Message | Fix |
|---|---|---|
| `redirect_uri_mismatch` | The redirect URI registered with *Provider* doesn't match this instance's. | Register exactly `<uri>` (with copy button). |
| `invalid_client` | The Client ID or Secret is wrong, or belongs to a different app. | Re-enter credentials from the provider console. |
| `invalid_scope` | *Provider* rejected the requested permissions. | The app may not have those APIs/scopes enabled. |
| `access_denied` | You declined the permission request. | Retry and accept. |
| *(unrecognised)* | Raw provider text, framed as a provider response. | Link to the provider's console. |

Rendered as a structured banner on the connections page, not a query-string blob.

## UX flows

### Before consent — `ServiceWizard`

1. **The redirect URI, with a copy button.** This single field unblocks all eighteen
   providers and is the highest-value change in the spec.
2. **The preflight verdict** from `Check`:
   - ✅ clean → Connect enabled.
   - ⚠️ soft → Connect enabled, warning shown.
   - ❌ hard → **Connect disabled**, with the problem, the fix, and a direct link to the
     instance-URL setting.

The preflight also names the **remedy tier** rather than only the local problem — e.g.
"`agents.rookie.lan` works for 4 of your 18 providers; a public domain unlocks the rest" —
so the trade-off is visible while the user is choosing a URL, not discovered provider by
provider.

### Instance URL setting — owner settings

Field + **"Test this URL"** button. The server generates a nonce, fetches
`<base>/healthz/echo?token=<nonce>` and asserts the nonce comes back. This proves the
URL reaches **this process** — catching a typo, a wrong port, DNS resolving elsewhere, or a
proxy aimed at a different instance. Reachability alone would not catch the last two.

**The echo endpoint must be unauthenticated**, mounted outside `/api/v1` beside the existing
`/healthz` (which is already unauthenticated). The self-test is a server-to-server fetch and
carries no session cookie, so an authenticated endpoint would fail every time regardless of
whether the URL is correct — inverting the signal the test exists to give. It is safe to
expose because it is not an oracle: the nonce is a 128-bit random value held in memory,
valid once and for 30 seconds, and the endpoint echoes **only** a nonce it issued, returning
404 for anything else. It reveals no configuration.

> **Deliberate exception:** `internal/nethttp.GuardedClient` blocks loopback and RFC1918 by
> design — that guard is why chat cannot reach the connector bridge. The self-test must use
> an unguarded client, because dialling ourselves is exactly the point. This is the single
> intentional exception in the codebase and needs a comment saying so, or a later reader will
> "fix" it back.

### Deployment guidance

The docs gap is part of the deliverable, not a follow-up. `SA_PUBLIC_URL` gets documented in
`Dockerfile`, `packaging/systemd/simple-agents.service` and `packaging/README.md`, alongside
the deployment matrix:

| Deployment | Redirect URI | Outcome |
|---|---|---|
| Local, browsed at `localhost:8080` | `http://localhost:8080/…` | Google/GitHub/Notion work; Slack-class need HTTPS |
| LAN server, plain HTTP on an IP | `http://192.168.1.194:8080/…` | Google rejects raw IPs; GitHub works |
| LAN server, internal CA, `.lan` name | `https://agents.rookie.lan/…` | HTTPS satisfies Slack-class; Google rejects the reserved TLD |
| **Real domain, DNS-01 cert, LAN-resolved** | `https://agents.example.com/…` | **All providers work; no inbound exposure** |

The last row is the recommended target for self-hosted installs: the provider never dials
the server — it redirects the browser — so the hostname only has to be *publicly valid*, not
publicly *reachable*. A real domain with a DNS-01 certificate, resolved locally to a private
IP, satisfies every provider while the host stays behind the firewall.

## Testing

The reliability claim rests on pure-function tests, not on manual OAuth runs:

- **`publicurl.Check`** — table test over every access-URL shape (`localhost`, `127.0.0.1`,
  `::1`, LAN IP, `.lan`, `.local`, dotless, `github.io`, `example.co.uk`, real domain,
  schemeless, trailing path) × every policy class → expected `[]Problem`. Includes an
  explicit case asserting `github.io` is **not** hard-blocked.
- **`publicurl.Normalize`** — schemeless, trailing slash, embedded path/query/fragment.
- **State round-trip** — 4-, 5- and 6-field payloads; tampering still rejected.
- **Policy inheritance** — a `google` child (e.g. `google_drive`) resolves the parent's policy.
- **Error mapper** — provider error string → taxonomy entry.
- **Frontend** — the URI renders and is copyable; Connect is disabled on a hard problem and
  enabled on a soft one.

## Surface touched

| Area | Change |
|---|---|
| `internal/publicurl/` (new) | `Resolve`, `Detect`, `Normalize`, `Check`, `Policy`, `Problem` + tests |
| `internal/connectors/registry.go` | `RedirectPolicy` field on `Provider`; resolve via OAuth parent |
| `internal/connectors/providers/*.yaml` | Policy blocks for google/github/notion/slack; fix the "shown above" wording |
| `web/handlers_services.go` | `publicBaseURL` → `publicurl.Resolve`; pin URI into state; use pinned URI at exchange; error mapper |
| `web/api_services.go` | Provider DTO gains `redirect_uri` + `preflight` |
| `web/api_workspaces.go` | Instance URL in admin settings GET/PUT; the self-test trigger |
| `web/server.go` | Unauthenticated `/healthz/echo` beside the existing `/healthz` |
| `web/ui/src/pages/connections/ServiceWizard.tsx` | URI display + copy + preflight banner |
| `web/ui/src/pages/settings/OwnerSections.tsx` | Instance URL field + "Test this URL" |
| `Dockerfile`, `packaging/*`, `CLAUDE.md` | Document `SA_PUBLIC_URL` + the deployment matrix |
| `web/api_parity_test.go` | Register the new route in the `want` inventory |

## Risks and accepted costs

- **A wrong policy entry blocks a working provider.** Mitigated structurally: hard-blocks
  require `verified: true`, and only four providers carry it. Everything else is advisory.
- **The reserved-TLD deny-list is conservative.** A bogus-but-unlisted TLD passes preflight
  and fails at the provider. This is the correct asymmetry: we never block something valid.
- **Provider policies drift.** The `verified` flag records that a human checked against live
  docs; the source is cited per provider in the table above so re-verification is cheap.
- **The self-test needs an unguarded HTTP client**, a deliberate hole in an otherwise
  absolute guard. Confined to one function, commented, and it dials only the configured
  instance URL.

## Out of scope

TLS termination in the server; changing the frozen callback path; per-workspace public URLs
(the URL is an instance property); automatic reverse-proxy configuration.
