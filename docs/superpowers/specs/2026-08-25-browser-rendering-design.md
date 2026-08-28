# Browser-backed page rendering (Spec 1 of 2)

**Date:** 2026-08-25
**Status:** design approved, implementation in progress
**Follow-on:** `2026-08-25-browser-automation-design.md` (acting on the user's behalf)

## The problem, stated precisely

Two complaints arrive as one and are not the same defect.

1. **`web_fetch` returns an empty shell.** A single-page app serves ~400 bytes of
   markup and renders its content client-side. `web_fetch` is an HTTP client, so it
   returns markup with no words in it. The model then either reports the page as
   empty or invents content.
2. **The search cascade hits JS challenges.** `internal/websearch` already treats
   "this engine produced no parseable results" as a reason to try the next engine,
   precisely because a 200-OK interstitial is indistinguishable from genuine
   no-results. When every keyless engine returns a challenge page the cascade is
   exhausted and returns nothing.

A browser fixes (1) directly. It fixes (2) only if search itself routes through a
browser. Both are addressed here, by different mechanisms.

There is a third, quieter problem. The `playwright-browser` core skill already
teaches an agent to drive a browser from a Python script. It works for a strong
model and is close to useless for the weak models this platform actually runs:
the skill hands over an API, and the model has to write correct Playwright code,
in one shot, with no feedback loop. A native tool inverts that — the host owns the
Playwright code and the model supplies parameters.

## Scope

This spec covers **reading**: rendering a page and returning its text, and
rendering a search-results page when the cascade is exhausted. It deliberately
excludes clicking, typing, form submission, session persistence and secret
injection, all of which are Spec 2.

**The core skill is NOT deleted here.** It teaches acting as well as reading, so
removing it after the read-only half would take away a capability nothing yet
replaces. It is deleted at the end of Spec 2.

## Decisions taken, with evidence

Every claim below was measured on the development host (Fedora 44, kernel 7.0.9,
Landlock ABI 8) rather than recalled. The throwaway harness is recorded in the
implementation notes.

### The library and what it actually costs

`github.com/mxschmitt/playwright-go@v0.6201.1` (Playwright 1.62.1). Note the
module path: it still declares `mxschmitt`, so requiring it as
`playwright-community/...` fails to resolve even though that is where the
repository now lives.

It is pure Go — the build stays CGo-free and the cross-compile gate is unaffected.
The **runtime** is the cost, and it is not small:

| Component | Download | On disk |
|---|---|---|
| Node.js + `playwright-core` driver | ~70 MB | 134 MB |
| Chromium | 115 MiB | 389 MB |
| Chromium headless shell | — | 262 MB |

Plus system shared libraries (nss, atk, cups, gbm, asound, pango, xkbcommon) that
normally require root.

**Therefore: the browser is an optional capability that degrades with a warning**,
not a dependency. It is emphatically *not* a fifth entry in `onboard.HostTools` —
that type probes a binary on `PATH`, the browser is a cache directory, and the
"four host tools" count is asserted by tests and by `make docs-sync-check` across
four delivery surfaces. Forcing it in would ripple into `install.sh`,
`install.ps1`, the nfpms `recommends` lists and the container image for something
no package manager installs anyway.

Availability is its own probe (`browser.Available`), its own `/healthz` boolean,
and its own installer subcommand (`rookery browser install`), which prints the
system-library command for the manager `onboard.DetectManager` reports.

### Chromium runs under the existing Landlock spec — verified

This was the decisive unknown, because the project has already rejected a
structurally identical design. `CLAUDE.md`'s MCP section defers stdio transport
on the grounds that a server spawned by the host process is "strictly more
privileged than the coder's own Landlock-confined `bash`". `playwright-go` drives
the Node driver from the host process, so the naive implementation walks into
that precedent — and it would be a **regression**, since the core skill today runs
Chromium from inside the coder's already-sandboxed subprocess.

Measured result: Chromium launches, executes JavaScript and produces an aria
snapshot **under `landlock.V5.BestEffort()` at ABI 8**, given these grants:

- **RW**: the per-call scratch profile dir, `~/.cache/ms-playwright`,
  `~/.cache/ms-playwright-go`, `/dev/shm`
- **RO**: `sandbox.SystemReadOnlyPaths()` **plus the directory of the running
  binary** — without it the helper cannot `exec` itself and fails with a bare
  `permission denied`, which is the first thing anyone reimplementing this will
  hit.

So the browser runs as a **hidden `rookery __browser-host` subcommand under
`sandbox.Wrap`**, exactly like the existing `__sandbox-exec` helper. It owns
playwright-go, the Node driver, Chromium and the guarded proxy, and serves a
loopback API the host process calls. It cannot read the database, `system.key`,
`config.yaml` or any vault.

Process separation alone would buy nothing — same uid, same file access. Landlock
is what makes the split meaningful, which is why the split is only worth doing
because the confinement was proven first.

### The address guard is a proxy, not URL inspection — verified

Chromium performs its own DNS, so `net.Dialer.Control` cannot reach it and URL
inspection would miss redirects and subresources. Instead: an in-process
HTTP/CONNECT proxy on loopback whose dial decision **is**
`nethttp.DenyPrivateAddr`, with Chromium launched against it. Every request, every
redirect hop and every subresource dials through the one existing policy; there is
no second copy of the blocklist to drift.

Measured: a page fetched through the proxy returned 7,989 characters of body text
from a real JS-rendered site, and a `goto` at a loopback URL standing in for the
connector bridge was **refused at the proxy** with
`blocked address: 127.0.0.1 is a private or loopback address`.

Two details worth keeping:

- **`--proxy-bypass-list=<-loopback>` is set explicitly even though it proved not
  to be load-bearing.** Chromium bypasses the proxy for loopback by default;
  Playwright already compensates, so loopback was refused with and without the
  flag. Relying on an undocumented default for a security property is how it
  regresses silently, so the flag is set and a test asserts the *behaviour* —
  loopback refused — rather than the flag's presence.
- **The escape hatch is the guard, not the proxy.** An owner who genuinely wants
  to read a self-hosted dashboard flips one setting that starts the proxy with the
  guard disabled. Default is guarded, matching `web_fetch` and `websearch`, and
  deliberately *not* matching connectors/MCP — their hosts come from vendored YAML
  or an owner-typed URL, whereas here the model picks the URL out of untrusted
  search results and page content, which is exactly `nethttp`'s stated threat
  model.

### The aria snapshot is real, and too big

`page.AriaSnapshot(Mode: "ai")` returns element references of the form `[ref=e2]`,
and Playwright's `aria-ref=e2` selector engine acts on them. Verified directly.
This is the representation that makes a weak model viable — it says
`click(ref=e7)` instead of inventing a CSS selector.

It is also **53,592 characters for one news homepage**, against a `maxToolResult`
of 8 KiB. Spec 2 therefore cannot hand back a raw snapshot; it needs a filter down
to interactive roles. Recorded here because the measurement was taken here.

## Architecture

### `internal/browser`

A peer of `internal/connectors` and `internal/mcp`, mirroring their shape: one
typed choke point, one availability probe, one loopback bridge for CLI coders.

```
Available() (bool, string)          // installed? if not, why, in the operator's words
Render(ctx, Request) (Result, error) // the single door
```

`Request`: URL, optional `wait_for` (`load` | `networkidle` | a CSS selector | a
text string), timeout, and `offset`/`limit` for paging.

`Result`: extracted text, title, final URL after redirects, `Truncated` +
`NextOffset`, and a `Blocked` classification.

**`Blocked` is a reported outcome, never a retry loop and never a bypass
attempt.** A Cloudflare interstitial, a captcha frame or a login wall is detected
and named. Bypassing bot protection is a stated non-goal — the honest-failure
property `coderProducedNothing` and the thin-PDF warning already establish.

### Lifecycle

One browser host process per server, started lazily on first use and shut down
after an idle timeout, with a fresh incognito `BrowserContext` per call. The
ephemeral context *is* the tenancy boundary in this spec: nothing persists, so
there is nothing to leak between workspaces. Spec 2 changes that and must
introduce a real per-workspace boundary when it does.

### Tool surface

`browser_read` sits on the **always-on** side of `includeExecTools` — chat gets
it. It is offered only when the browser is available, so the tool list varies by
host exactly as connector tools already do. A missing runtime yields
`ErrBrowserUnavailable` naming the fix, mirroring `ErrLocalCoderDisabled`, never
a failing spawn.

**The routing guidance is the whole game for weak models**, and it is stated from
both ends:

- `browser_read`'s description says: use `web_fetch` first; use this when
  `web_fetch` reports the page rendered no content, or when the target is known
  to be an app.
- **`web_fetch` closes the loop.** When it extracts near-zero body text from an
  HTML response it returns a notice naming `browser_read`. This is what makes the
  separate tool work in practice: the model is told what to do at the moment it is
  stuck, rather than having to recall a prompt written thousands of tokens
  earlier. Silent escalation inside `web_fetch` was rejected — it would make a
  fetch cost seconds and spawn Chromium invisibly, and the tool's own description
  would stop being true.

Results are capped at the existing 8 KiB `maxToolResult` with `offset`/`limit`
paging copied from `read_file`, which already solved this problem.

### Search cascade

`websearch.BrowserProvider` registers **last** in the cascade and only when the
browser is available. When every keyless scrape returns a challenge page or zero
parseable results, it renders one in a real browser and hands the HTML to the
existing parser. All retry, dedupe and provenance machinery is reused unchanged;
`Label()` gains an entry so `Outcome.Provider` names it and the user sees which
engine actually served. Keyed providers (Brave, Tavily) stay first — the browser
is the slowest thing in the cascade.

### CLI-coder parity

A fifth bridge, alongside `connectors`, `mcp`, `vault` and `agentstate`: a
loopback listener with a per-run bearer token scoped to one workspace, a
`rookery browser read` subcommand, and `Bash(<bin> browser:*)` added to
`coder.ChatAllowedTools`.

This is not optional polish. `ChatAllowedTools`' own doc comment exists because
capability once diverged between surfaces that shared a prompt claiming otherwise.
A parity test asserts `browser_read` appears in both the API tool set and the CLI
grant.

Note the bridge serves **two** consumers: the CLI coder, and the host process
reaching its own sandboxed browser host. One mechanism, two callers.

### Prompts

A single-source `browserToolsBlock(backendType, available)` in `internal/prompts`,
injected into the tool list in `coderCapabilitiesBlock` and into
`BuildChatSystemPrompt`. It extends the existing "Choosing between them for WEB
access" paragraph, which is where a model actually looks. Single-sourced so the
CLI and API wordings cannot drift — the failure `connectedToolsBlock` was factored
out to prevent.

## Testing

- Pure unit tests, no Chromium: blocked-classification, text extraction, paging,
  the proxy's dial decisions, the search provider's parsing, the `web_fetch`
  empty-shell notice, and the tool-grant parity assertion.
- `//go:build browser` for the handful that need a real browser, following the
  `livecheck` precedent so CI never depends on a 500 MB download.
- One test pins that loopback is refused through the browser, because that is a
  security property and the flag protecting it proved not to be load-bearing.

## Non-goals

Clicking, filling, form submission, session persistence, secret injection,
screenshots returned to the model, headed mode, captcha/Cloudflare bypass, the
designer feasibility probe, and deleting the `playwright-browser` core skill. All
Spec 2.
