# Web search provenance and key validation

**Date:** 2026-08-03
**Status:** approved, not yet implemented

## Problem

A workspace with a Brave Search API key configured has no way to learn whether
Brave is actually serving its searches — and the chat assistant confidently
reports that it is not.

The wiring is correct. `SEARCH_KEY_BRAVE` reaches every surface that runs
`web_search`:

| Surface | Injection path |
|---|---|
| Web chat | `web/handlers_misc.go` → `websearch.ResolveKeyEnv` → `WithExtraEnv` |
| Platform chat (Telegram/Discord/Slack) | `cmd/rookery/main.go` → same |
| Agent runs | `internal/agentrunner/runner.go` → `svc.GetAll` → `WithExtraEnv` |
| Agent + skill designers | `WithSecretsLoader` → `WithExtraEnv` |

All four land in `hostToolSet.subprocessEnv`, and
`hostToolSet.searchProviders()` puts the keyed provider ahead of the keyless
cascade. Nothing is missing.

What is missing is any signal that this happened. Three separate gaps combine
into a single user-visible failure:

1. **The tool description hardcodes the engine.**
   `internal/coder/hosttools.go` describes `web_search` as *"Search the public
   web (DuckDuckGo)"*, and `internal/prompts/prompts.go` repeats the claim. The
   model reads the string and reports it as fact.

2. **`internal/websearch` logs nothing.** `Client.Search` bare-`continue`s past
   every provider failure and returns only `[]Result`. The engine that served a
   query is discarded at the moment it is known. Determining it after the fact
   required DNS forensics, which was itself inconclusive because
   `nsswitch.conf` routes lookups through systemd-resolved's cache, so repeat
   queries never reach the network-level log.

3. **A stored key is never validated.** `PUT /api/v1/search-keys` accepts any
   string. `GET` reports `brave: true` because a secret row exists, not because
   the key works. A typo'd key returns 401 — a non-transient error, so the
   cascade moves on silently and permanently.

### Observed failure

From a real chat transcript, after the user configured a Brave key:

> **user:** I added a brave api key
>
> **assistant:** I don't have a Brave Search tool — my `web_search` is hardcoded
> to DuckDuckGo, and I have no way to pick up an API key you've added. […] adding
> a key alone doesn't give me a tool to use it.

Every clause of that is false. Earlier in the same conversation the model had
also asserted the tool was "Anthropic's hosted web search service", then
"corrected" itself into the DuckDuckGo claim by quoting the tool description.
The user reasonably believed it.

## Goals

- The engine that served a search is visible to the model, to the user, and in
  the logs.
- A key that does not work is rejected when it is entered, not silently ignored
  forever.
- Search still degrades rather than fails when a provider is down.

## Non-goals

Two issues surfaced in the same investigation and are deliberately excluded:

- **Connector TLS error surfacing.** `adsense.googleapis.com` and
  `analyticsadmin.googleapis.com` are blocked by the host's AdGuard filter lists
  (`||adsense.googleapis.com^`), resolve to `0.0.0.0`, connect to the local
  Caddy instance, and abort with `remote error: tls: internal error`. The host
  fix is an AdGuard allowlist entry. There is a legitimate Rookery-side item —
  `connectors.Execute`'s error taxonomy surfaces a raw TLS alert with no hint
  that DNS resolved into private space — but folding a connector-error change
  into a websearch change makes both harder to review. Separate spec.

- **JavaScript-rendered pages.** `web_fetch` returns stripped HTML and cannot
  execute JavaScript, which is why a Pazar3 listing scrape failed in the same
  session. No search provider fixes this; a headless browser is a
  different-sized problem with its own dependency and sandbox implications.

## Design

### 1. Provenance in `internal/websearch`

`Client.Search` returns the winning engine alongside the results:

```go
// Outcome is the result of running the provider cascade. Provider names the
// engine that actually served the results; it is empty when every provider
// was exhausted. Tried lists every engine attempted, in order.
type Outcome struct {
    Results  []Result
    Provider string
    Tried    []string
}

func (c *Client) Search(ctx context.Context, query string) (Outcome, error)
```

The signature changes rather than gaining a parallel method: `hosttools.webSearch`
is the only production caller, so a second entry point would exist solely to
avoid touching one call site.

Exhausting every provider remains a **non-error** — `Outcome{Tried: […]}` with a
nil error. That property is load-bearing: the coder's oscillation guard treats
any `error:` result as a failing call worth blocking, which is wrong for a query
that simply matched nothing.

`Search`/`runProvider` gain structured logging, one line per provider outcome:

```
slog.Debug("websearch provider served", "provider", "brave", "results", 6)
slog.Warn ("websearch provider failed", "provider", "brave", "err", err)
```

A keyed provider failing is `Warn`, not `Debug`: the user paid for that key and
the system is quietly falling back without it.

**The query is never logged.** It is user content. Log lines carry provider
name, status, result count and error only. API keys are already scrubbed from
error bodies by `errSnippet`.

`websearch.Label(name string) string` single-sources display names
(`brave` → `Brave Search`, `ddg-html` → `DuckDuckGo`, …) so the tool
description and the result tag cannot drift apart.

### 2. Engine-neutral prompts, engine-accurate results

`hostToolSet.tools()` builds the `web_search` description from
`h.searchProviders()` rather than a hardcoded string:

- key configured → `"Search the public web (Brave Search, with fallback engines) …"`
- no key → `"Search the public web (DuckDuckGo, Mojeek, Bing) …"`

`hostToolSet.webSearch()` prefixes the rendered result with the engine that
actually served it:

```
Results via Brave Search:
1. time.mk — Macedonian news aggregator
   https://time.mk
   …
```

and the empty case names what was attempted:

```
(no search results — tried Brave Search, DuckDuckGo, Mojeek, Bing)
```

Both mechanisms are needed because the transcript shows the model getting this
wrong in **both** states. Asked cold at 10:37:35 it quoted the description
verbatim ("The tool description states it explicitly"). Then at 10:38:54, having
just run a successful search and holding the results in context, it *still*
reported "`web_search` (DuckDuckGo)" and told the user their key "isn't wired
into anything I can call".

So the description is what the model quotes when it answers without searching,
and the result tag is what corrects it when it has actually searched. The result
tag is the stronger of the two — it is ground truth per call, so a silent Brave
failure is reported as the engine that actually served rather than the one that
was configured — but it only exists on turns that search, which is why the
description is fixed as well.

`internal/prompts/prompts.go` drops `(DuckDuckGo)` and stays engine-neutral.
Prompts are constructed without access to the coder's provider list, so naming
an engine there can only drift from reality.

A guard test bans hardcoded engine names in `prompts.go` and in the static
portion of the tool description, following the existing
`TestSetupStepsUsePlaceholderNotProse` precedent.

### 3. Save-time key validation

`doJSON` classifies authentication failures instead of flattening every status
into `fmt.Errorf("HTTP %d: %s", …)`:

```go
// ErrInvalidKey means the provider rejected the credential (401/403). It is
// distinct from a transient failure: retrying will not help, and the caller
// can tell the user their key is wrong rather than that search is flaky.
var ErrInvalidKey = errors.New("invalid api key")
```

New `websearch.Verify(ctx, hc, p Provider) error` runs one canned query against
a single provider and returns `nil`, `ErrInvalidKey`, or a transient error. The
query is the fixed string `"example"` — the content is irrelevant, since a 200
response with zero results is **success**: it proves the credential works, which
is all Verify tests. Verify does **not** retry; the caller distinguishes
"rejected" from "could not check", and a retry loop only makes the settings save
slower.

`web.apiPutSearchKey` calls it before storing, through
`nethttp.GuardedClient(10 * time.Second)`:

| Verify result | HTTP | Stored? | Body |
|---|---|---|---|
| `ErrInvalidKey` | 400 `invalid_key` | no | error |
| transient (429/5xx/network) | 200 | yes | `{"ok":true,"verified":false,"note":"could not verify right now"}` |
| success | 200 | yes | `{"ok":true,"verified":true}` |

A provider outage must not block a save, so only a definitive auth rejection is
fatal. The cost is one API call per save — negligible against Brave's free tier.

The check reaches the handler through a `Server.searchKeyVerify` field rather
than a direct call, defaulting to `verifySearchKey`. Without that seam every
test that saves a key would make a live request to Brave, which is both flaky
and rude; the existing `TestAPISearchKeysConfiguredStateAndDelete` would have
started failing on a 401 the moment this landed.

At runtime, `ErrInvalidKey` still falls through to the next provider. Search
degrades rather than fails; the difference is that it is now logged at `Warn`.

#### A blocked search host must not read as a network blip

`nethttp.DenyPrivateAddr` rejects at dial time with a plain
`fmt.Errorf("blocked: %s is a private or loopback address", host)` — no sentinel,
so callers cannot tell a DNS-policy block from a flaky network.

This matters concretely on the reporting host. If a filter list ever adds
`api.search.brave.com` — the same rule class that already catches
`||adsense.googleapis.com^` there — the hostname resolves to `0.0.0.0`, the dial
guard rejects it, and today that surfaces as a generic network error. Verify
would store the key with `verified: false`, every runtime search would fall
through to the keyless cascade, and the new `Warn` line would blame a network
blip. That is precisely the silent-degradation shape this spec exists to remove,
arriving through a door the rest of the design does not watch.

Fix, bounded to two lines of `internal/nethttp`: export a sentinel and wrap the
existing errors with `%w`.

```go
// ErrBlockedAddr means the host resolved into address space the guard refuses
// (loopback, RFC1918, link-local, CGNAT, cloud metadata). Distinct from a
// network failure: retrying cannot help, and the cause is usually local DNS
// policy resolving a public name to 0.0.0.0.
var ErrBlockedAddr = errors.New("blocked address")
```

`websearch` then classifies a dial-guard rejection as **non-transient** (no
retry — the address will not change) and logs it distinctly:

```
slog.Warn("websearch provider blocked", "provider", "brave", "host", h,
          "hint", "resolved into blocked address space; check local DNS filtering")
```

Verify maps it to its own `PUT` response note ("host is blocked by local DNS
policy") rather than the generic "could not verify right now", so the operator
is pointed at their resolver instead of at Brave.

This is the only change outside `internal/websearch`, `internal/coder`,
`internal/prompts` and `web/`, and it is included because it closes the exact
failure mode that produced this spec's originating report.

### 4. Verification state is not persisted

`GET /api/v1/search-keys` continues to report *configured*, not *working*.

Persisting a last-known-good flag means a new column or a `settings` row, plus
deciding when it goes stale. The save-time gate catches the failure that
actually occurred (a key that never worked), and runtime logging catches later
expiry or quota exhaustion. The SPA (`src/lib/searchKeys.ts`,
`src/pages/connections/ConnectionsPage.tsx`) surfaces `verified` and `note` from
the PUT response inline immediately after save — no schema change.

This is a deliberate boundary, not an oversight. Revisit if key expiry turns out
to be common enough that a stale-state indicator earns its complexity.

## Components

| Unit | Responsibility | Depends on |
|---|---|---|
| `websearch.Outcome` | Carries results + serving engine + attempted list | — |
| `websearch.Label` | Engine name → display string | — |
| `websearch.Verify` | Single-provider credential check | `Provider`, `http.Client` |
| `websearch.ErrInvalidKey` | Typed auth rejection | — |
| `nethttp.ErrBlockedAddr` | Typed dial-guard rejection | — |
| `hostToolSet.tools()` | Provider-aware tool description | `searchProviders()`, `Label` |
| `hostToolSet.webSearch()` | Renders results with provenance | `Client.Search`, `Label` |
| `web.apiPutSearchKey` | Validates before storing | `Verify`, `nethttp.GuardedClient` |

## Data flow

```
PUT /search-keys {provider,key}
  → KeyedProvider(provider, key, "")
  → Verify(ctx, guardedClient, p)
      ErrInvalidKey → 400, not stored
      transient     → store, verified:false
      nil           → store, verified:true

web_search(query)
  → searchProviders()            [brave, ddg-html, ddg-lite, mojeek, bing]
  → Client.Search
      per provider: served → Outcome{Provider}      + slog.Debug
                    failed → next                   + slog.Warn
  → "Results via <Label(Provider)>:" + numbered entries
     or "(no search results — tried <Label(t)> …)"
```

## Testing

- **`websearch`** — `Outcome.Provider` names the first engine returning results;
  a failing first provider falls through and the second is reported; `Tried`
  accumulates in order. `Verify` classification against `httptest`: 401 →
  `ErrInvalidKey`, 429 → transient, 200-with-zero-results → nil.
- **`hosttools`** — the description names the configured keyed provider when one
  is set and the keyless engines when none is; the rendered result carries
  `Results via …`; the no-results notice lists every attempted engine.
- **`web`** — `PUT /search-keys` returns 400 and stores nothing for a key the
  provider rejects; stores with `verified:false` on a transient failure; returns
  `verified:true` on success.
- **`prompts`** — guard test failing the build on a hardcoded engine name in
  `prompts.go` or the static tool description.
- **`nethttp`** — `DenyPrivateAddr`'s rejections satisfy
  `errors.Is(err, ErrBlockedAddr)`, and an ordinary dial failure does not.
- **`websearch`** — a dial-guard rejection is treated as non-transient (one
  attempt, not three) and reported distinctly from a generic network error.

No new routes, so `web/api_parity_test.go` is unchanged.

## Risks

- **Signature change to `Client.Search`** touches one production caller and the
  package's own tests. Contained, but it is the one breaking change here.
- **Save-time verification adds a network call to a settings write**, so a slow
  provider makes the save feel slow. Mitigated by a short timeout and by
  treating any non-auth failure as "store anyway".
- **The description is built at toolset construction**, so a key added mid-run
  is not reflected until the next toolset is built. Acceptable: chat builds a
  fresh toolset per message.
