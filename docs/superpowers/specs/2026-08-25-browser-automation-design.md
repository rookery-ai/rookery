# Browser automation: acting on the user's behalf (Spec 2 of 2)

**Date:** 2026-08-25
**Status:** design approved, implementation in progress
**Depends on:** `2026-08-25-browser-rendering-design.md`

Spec 1 gave the platform a confined, guarded browser that can *read* a rendered
page. This spec lets an agent *act* in one: click, type, wait, submit — including
flows that need a login and a stored credential.

## What makes this different from every other tool here

Every other capability in this platform either reads, or writes somewhere we own.
This one performs irreversible actions on third-party systems using the user's own
credentials. A mistake is not a bad answer; it is a payment.

Three consequences shape the whole design:

1. **A prompt is not a boundary.** `CLAUDE.md` already records this about
   `dryRunSendProhibition`: it is "a PROMPT, not a boundary", relied upon while
   injecting real secrets and granting real `Bash`. That was tolerable for a
   script that *might* send an email. It is not tolerable for a tool whose whole
   purpose is to click buttons. Acting needs a refusal at its own choke point.
2. **Consent cannot be per-click.** See the approval section — the existing
   `park, plain` semantics structurally cannot express it.
3. **The model must never see the secret**, and "never sees" has more leak paths
   than it first appears.

## Scope

Acting tools, the page representation that makes them usable, secret injection,
persistent logins, the consent model, the designer feasibility probe, and removal
of the `playwright-browser` core skill.

## The page representation

Spec 1 measured `page.AriaSnapshot(Mode: "ai")` at **53,592 characters for one
news homepage**, against a `maxToolResult` of 8 KiB. So the raw snapshot cannot be
returned, and the naive fixes are both wrong: truncating it cuts the page off
mid-way through the elements the model needs, and summarising it with a model
costs a turn and loses the refs.

Instead the host **filters to interactive roles** — button, link, textbox,
checkbox, radio, combobox, menuitem, tab, plus any element carrying an explicit
`[ref=]` and an accessible name — and renders one compact line each:

```
e7   button   "Pay now"
e12  textbox  "Card number"        (empty)
e13  textbox  "Expiry"             (empty)
e21  link     "Back to invoices"
```

The model then calls `browser_click(ref: "e7")`. It never writes a CSS selector,
which is the single property that decides whether a weak model can use this at
all. Refs are resolved through Playwright's `aria-ref=` selector engine.

When the filtered list still exceeds the cap it is paged, and the result says how
many elements were withheld — never a silent truncation, per the standing rule.

## Tools

All acting tools are gated behind `includeExecTools`, i.e. **agent builds and runs
only, never chat**. This matches `run_script`/`bash` exactly, and the reasoning is
the same: chat is a human typing in real time with no approval gate
(`ParkerFor` returns nil when `agentID == ""`), so a chat that can click "Pay"
holds the user against themselves. Chat keeps `browser_read` from Spec 1.

- `browser_open(url, wait_for)` — navigate, return the interactive-element list
- `browser_click(ref)`
- `browser_fill(ref, value)` — `value` may contain `${SECRET_NAME}`
- `browser_press(key)` — Enter, Tab, Escape
- `browser_wait(for, timeout)` — a selector, a text string, or network idle
- `browser_read_page(offset, limit)` — the rendered text, paged

A run holds **one browser context for its duration**, so navigation and state
persist across calls within the run. That is what lets a flow proceed step by
step, and it is the reason the context is torn down at run end.

## Secret injection

The model writes `${ELECTRIC_BILL_PASSWORD}`; the host resolves it at fill time
and types the value. The model never receives the digits.

**A dedicated resolver, not `secrets.Service.Proxy`.** Same `${NAME}` syntax, two
deliberate differences. `Proxy` leaves an unresolvable placeholder *as-is* — which
here would type the literal string `${CARD_NUMBER}` into a payment field — so the
browser resolver **fails closed**, erroring with the missing name (never a value).
`Proxy` also carries a comment restricting it to `AgentRunner.Run()`, and widening
that contract silently is how a "must not be logged" guarantee stops holding.

Four leak paths, each closed explicitly:

- **Read-back.** `input.value`, page text and the aria snapshot all echo a filled
  value. Every browser result is passed through a redactor seeded with the values
  resolved during this run — the same discipline `hostToolSet.redactSecrets`
  already applies to script output.
- **Error text.** Playwright includes actual values in some timeout and assertion
  messages. Errors go through the same redactor.
- **URLs.** A GET form puts the value in the query string, which then appears in
  the final URL. Redacted.
- **Screenshots.** Redaction cannot touch pixels. **Screenshots are never returned
  to the model** — carried forward from Spec 1 as a permanent non-goal rather than
  a deferral. If a screenshot is ever wanted for the *user*, it goes to the vault
  and the inbox, never into a completion.

Note what this does **not** claim: an agent that already has `run_script`/`bash`
holds every secret as an environment variable, so the browser is not a new
exfiltration channel for such an agent. The redactor protects against accidental
echo, not against a deliberately hostile agent — that boundary is the guardrails
and the sandbox, unchanged.

## Persistent logins

Paying a bill means being logged in, and logging in on every run is both fragile
and a reliable way to trigger 2FA and fraud heuristics. So a **session** captures
Playwright's `StorageState` (cookies plus local storage) after a successful login
and re-seeds it on the next run.

Stored cookies are bearer tokens. Therefore:

- encrypted with `secrets.EncryptWithSystemKey` — the system key, not the master
  password, because the scheduler runs headless at 03:00;
- written to `<data_dir>/browser-sessions/<workspaceID>/`, **outside the vault**.
  This is the important one: the vault is readable by every agent and is included
  in backups. `claude-homes/` is already excluded from snapshots for exactly this
  reason, and sessions follow it.
- scoped per workspace, and bound per agent.

## Consent: a standing grant, never a per-click prompt

The existing approval gate cannot be reused here, and the reason is structural.
`internal/approval`'s recorded semantics are "park, plain": the call is stored and
**the run finishes immediately**. A bill payment is a stateful multi-step flow, so
by the time the owner approves — minutes or hours later — the browser context, the
login and the half-filled form are gone. Parking mid-flow would produce a ticket
that cannot be honoured.

Holding a live browser context open across an arbitrary human wait is the feature
that would make per-click approval work. **It is not built**, and this is recorded
as a limitation rather than designed around.

What is built is a two-tier standing grant, both defaulting to off:

1. **`allow_acting`** — this agent may click and type in this session at all.
   Without it the agent gets `browser_read` and nothing else. An owner turns this
   on per (agent, session) in the UI, deliberately, once.
2. **`allow_irreversible`** — additionally permits actions the host classifies as
   irreversible: an element whose accessible name matches pay / purchase / confirm
   / submit order / delete / transfer.

The name match is a **heuristic and is treated as one**. It is a second layer, not
the protection: the protection is that acting is off entirely until the owner
turns it on for one agent on one site. A heuristic that misses costs nothing that
tier 1 was not already gating.

Additionally, every irreversible action writes an inbox notification and a line in
the run transcript, so an autonomous action is never invisible after the fact.

## The build-phase refusal is a real boundary

`buildphase.EnvVar` currently gates `connectors.Execute` and `mcp.Execute`. The
browser's acting path becomes the **third** instance — not a fourth prompt.

During a build or a create-build dry run, navigation and reading are permitted
(a rehearsal that cannot look at the page cannot rehearse) and **every acting call
is refused** with a result that explains why, so the model reports the limitation
instead of retrying into the oscillation guard.

This makes a bill-paying agent untestable at build time. That is consistent and
deliberate: connectors have had exactly this property since they shipped, refusing
`mutating` actions at build for the same reason.

## Designer feasibility probe

Both designers are `WithNoTools`, so a live probe is a structural change, not a
prompt change. The precedent is `vault.BuildKBContext`: the designer has no search
tool, so retrieval is performed *for* it and injected as a block each turn. The
same shape applies here — the designer is never handed a browser tool.

When the design conversation names a URL, `Flow` renders it read-only and injects
`<site_feasibility>`: reachable, JS-rendered or static, login wall detected,
captcha or Cloudflare interstitial detected, and whether form controls are
present. That turns "will this work?" from a guess into a finding, before the user
approves a build that cannot succeed.

Bounded and best-effort: one probe per distinct URL per session, cached; skipped
entirely when the browser is unavailable; any failure leaves the block absent
rather than failing the turn.

## Removing the `playwright-browser` core skill

Deleted once the tools above exist, because it now teaches a worse way to do the
same thing. The removal has fallout in four places, each of which fails silently
if missed:

- the bundled-skill count (22 → 21) asserted in `skilllibrary` tests and counted
  by `make docs-sync-check`;
- README and the website's skill list;
- `web-research`'s SKILL.md, which refers readers to it;
- existing `agent_skills` rows naming it, which would dangle — swept by a
  migration, the way `DeleteAgentSkillsByName` already handles a deleted user
  skill.

A test pins the removal, following `TestRemovedProvidersStayRemoved`: the obvious
fix for "the playwright skill is missing" is to re-add the file, which would ship
a skill instructing models to hand-write Playwright against a native tool that
does it properly.

## Non-goals

Per-click human approval (above), screenshots to the model, captcha or Cloudflare
bypass, headed mode, browser acting from chat, and file uploads/downloads through
the browser.
