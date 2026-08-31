# Three-workspace testbed: the from-zero runbook

**Status:** proposed
**Date:** 2026-08-31
**Companions:** `2026-08-31-launch-testbed-reset-and-seed-design.md` (rationale),
`2026-08-31-feature-test-book-design.md`, `2026-08-31-agent-designer-test-charter-design.md`

## What changed, and why this document exists

The reset spec designed a *credential-preserving* reset: rename and purge `Personal`
so 17 OAuth connections survive in place. **That decision is withdrawn.** The install
is being wiped to empty and rebuilt from scratch.

That has one cost and two benefits, and the benefits are why this is the better plan.

The cost is real: **every connection is re-consented by hand** (~12 authorisations,
30–45 minutes), and every secret is retyped.

The benefits:

- **Nothing in the testbed depends on today's accident.** The reset spec had to
  *preserve* two live artefacts because they happened to be good fixtures. Preserved
  fixtures cannot be rebuilt, so the testbed would have been unreproducible the moment
  it was torn down again. Here every fixture is **created deliberately** — including
  the two that were going to be salvaged.
- **The setup path is itself under test.** A from-zero build exercises onboarding,
  owner bootstrap, the setup wizard, first-workspace creation and every connect flow —
  the exact path a launch-day user walks, and the one no amount of testing an
  already-configured install can cover.

This document is the operational half: **what goes into each workspace, in what order,
and which charters run there.** It is written to be executed months from now against an
empty install with no memory of what is here today.

## The three workspaces

| | **Testing** | **Personal** | **Work** |
|---|---|---|---|
| Role | crash test | launch assets | second tenant |
| Timezone | `Europe/Skopje` | `Europe/Skopje` | **`America/New_York`** |
| Coder kind | `api`, cheap model; **flipped to local CLI** for parity | `api`, strong model | `api`, different provider from Testing |
| KB | perf corpus (B1–B9), ~2 300 notes | showcase corpus, ~50 notes | work corpus, ~20 notes |
| Agents | S1, S2, S3, S7, S10, S11, S12 | S8, S9 | S4, S5, S6 |
| Chat app | Telegram bot #2 (+ Discord) | Telegram bot #1 | Telegram bot #3 (or Slack) |
| MCP | the server under test | — | — |
| Connections | the full fixture set | screenshot-friendly set | Google Workspace + SendGrid |
| Data realism | deliberately hostile | must look genuinely lived-in | plausibly professional |

Three properties are deliberate and worth defending if they look arbitrary later:

**`Work` runs on `America/New_York`.** A timezone bug and a correct conversion produce
the same wall-clock when every tenant shares the server's zone. A tenant 6–7 hours off
makes the difference visible in one screenshot, and it is the only way TC-TZ-2 and
TC-SCH-1 mean anything.

**Each workspace gets a different coder.** Testing on a cheap model is where weak-model
handling is exercised — the truncation caveat, the missing-header fallbacks, the
verification nudge — because those paths only fire on models that actually fail.
Personal runs a strong model because its output is going into screenshots. Work runs a
third provider so provider diversity is covered without a dedicated exercise.

**Each workspace gets a distinct master password.** Re-entry is prompted on every
switch, and identical passwords would make TC-ISO-2 pass without proving anything.

---

## Stage 0 — Host and owner (once, ~20 min)

1. **Deploy from a checkout you can name.** Build, deploy, then assert the running
   binary is yours: `curl -s localhost:8080/healthz` must report the commit you just
   built. `make deploy` prints success even when another process holds `:8080`.
2. `rookery backup` the outgoing install, then `rookery backup verify <name>` — it
   decrypts and checksums without restoring, so it proves the passphrase *and* the
   archive. Keep it off-box. This is the only route back.
3. Wipe: stop the server, move `~/.rookery` aside (do not delete until the testbed is
   proven), start clean.
4. `rookery onboard` — walk the real wizard rather than scripting around it. Accept the
   browser install when offered. **This is TC-SETUP-1**: the wizard is under test here,
   and it is the only chance to see it as a new user does.
5. Confirm `/healthz`: sandbox enabled with a Landlock ABI, all four host tools present,
   `browser: true`, no warnings.

**Record**: version, commit, ABI, host-tool booleans. These head the results document.

## Stage 1 — Prerequisites you must gather (~45 min, parallelisable)

Do all of these before Stage 2; every one of them blocks a workspace.

| # | Item | For | Note |
|---|---|---|---|
| P1 | Three Telegram bots (BotFather) | one per workspace | `platform_connections` is `UNIQUE(workspace_id, platform)` — one token cannot serve two workspaces |
| P2 | Google OAuth client (id + secret) | Personal, Work, Testing | one client serves all three; each workspace consents separately |
| P3 | Google Workspace account consent | Work | the `kroute.ai` account, distinct from the personal one |
| P4 | OpenRouter API key | all three | the coder's own key |
| P5 | A second LLM provider key | Personal or Work | so the three do not share one provider |
| P6 | Stripe **test-mode** key | Testing | the overbind fixture |
| P7 | SendGrid (or Mailgun) key | Work, Testing | email send / `public_write` |
| P8 | Notion token | Testing | `token_expiry: never` path |
| P9 | An MCP server URL + bearer token | Testing | see below |
| P10 | Brave search key | Testing | the keyed search provider |
| P11 | *Optional* Discord app; Slack bot + app token | Testing; Work | 3-platform parity |

**On the MCP server (P9).** Zero have ever been configured on this install, so the
entire surface is unexercised. Self-host one on the LAN with a static bearer token
rather than pointing at a public service. MCP deliberately bypasses the private-address
dial guard (like connectors), so a LAN address works — and the decisive advantage is
that **you can change its tool list on purpose**, which is the only way to test the
first-sync-enabled / later-sync-disabled asymmetry (TC-MCP-2, TC-MCP-3), the
vanished-tool marking (TC-MCP-4), and the over-cap notice (TC-MCP-6). A third-party
server whose tool list you cannot mutate tests roughly half of the surface.

---

## Stage 2 — Build each workspace

Build in the order **Personal → Work → Testing**. Personal first because it is the one
whose content takes real effort and whose screenshots gate the launch; Testing last
because it is the most disposable and the most likely to be rebuilt.

### 2A. Personal — the launch-asset workspace

This one has to look like a real person's knowledge base after a year of use. It is the
only workspace whose *content quality* is a requirement rather than a convenience.

**Settings.** Name `Personal`; a distinct workspace icon; profile: real name, location
Skopje, timezone `Europe/Skopje`, language English, a tone note. Coder: `api` +
OpenRouter (or Anthropic) on a **strong** model — this workspace's output is going into
screenshots, and a weak model's prose is not the product you want photographed.

**Connections.** Google (personal), Spotify, Open-Meteo (keyless), Wikipedia (keyless).
Chosen because they render well: a connections page with recognisable, colourful logos
is itself a screenshot.

**Chat app.** Telegram bot #1.

**Secrets.** `CODER_KEY_OPENROUTER`, plus whatever S9 needs.

**Knowledge base (~50 notes).** Structured so that backlinks are dense and every editor
construct appears at least once:

```
README.md                          home note — columns, callout, links out
memory/USER.md  SOUL.md  GENERAL.md   filled in properly, not placeholders
notes/home-server/runbook.md       service table, code blocks, callouts
notes/home-server/network-map.md   table + alignment
notes/projects/rookery-launch.md   task lists, toggles, a decision callout
notes/projects/rookery-launch/pricing-research.md
notes/reading/2026-reading-list.md table w/ ratings, colour marks
notes/reading/<book>.md × 4        backlinks into the list
notes/travel/japan-2026.md         2-column layout w/ images, itinerary table
notes/recipes/*.md × 6             ingredient tables, resized images
notes/finance/household-bills.md   table, a warning callout
notes/meetings/2026-08-*.md × 8    action items, [[people]] backlinks
notes/people/*.md × 5              backlink targets
notes/health/training-log.md       date table
notes/ideas/*.md × 6               short, varied
```

Coverage requirement: between them these must exercise **callouts, toggle lists,
`<div align>` alignment, 2/3/4-column layouts, both colour marks, underline, resized
images, a pipe-bearing table cell, wikilinks with live backlinks, code blocks and task
lists** — each is a distinct serializer path and a distinct screenshot.

**Every note is written through `PUT /api/v1/kb/note`, never dropped on disk.**
`checkFidelity` opens a note **read-only** when its markdown is not canonical, and
hand-authored callouts, toggles and alignment reliably produce the non-canonical
spellings. A read-only note looks broken in exactly the screenshot it was written for.
TC-KB-9 asserts all ~50 open editable.

**Agents.** S8 (KB curation, nightly) and S9 (utility bill watcher, monthly, uses
secrets and the browser).

**Charters that run here.** Section J (UI polish) in full, TC-KB-9/10/11/18, TC-CHAT-*,
TC-TZ-1, and the launch-asset capture in Stage 5.

### 2B. Work — the second tenant

Its job is to prove isolation and to make timezone behaviour visible.

**Settings.** Name `Work`; profile timezone **`America/New_York`**; a work persona in
`memory/USER.md`. Coder: `api` on a **third** provider/model.

**Connections.** Google Workspace (`kroute.ai`) and SendGrid. Deliberately narrow — a
second tenant with a different, smaller connection set is what makes TC-ISO-3 and
TC-CONN-6 meaningful.

**Chat app.** Telegram bot #3, or Slack if you want the third platform covered.

**Knowledge base (~20 notes).** `notes/clients/*.md`, `notes/meetings/*.md`,
`notes/processes/onboarding.md`, `notes/processes/invoicing.md`, plus memory files in a
work voice. Small on purpose: it is the contrast case against Testing's bulk.

**Agents.** S4 (email triage + draft, weekday mornings), S5 (gated weekly send —
the approval-gate scenario), S6 (morning brief, 08:00 **New York**).

**Charters that run here.** Section B (timezones) — TC-TZ-2 and TC-SCH-1 are the whole
reason this workspace has a foreign zone; Section F's approval-gate charters
(TC-CONN-8/9/10/11); Section H chat-app parity; TC-ISO-2 and TC-ISO-3.

### 2C. Testing — the crash-test workspace

Everything unpleasant lives here. Nothing in it needs to look good.

**Settings.** Name `Testing`; timezone `Europe/Skopje`. Coder: `api` + OpenRouter on a
**cheap, weak** model (e.g. a DeepSeek flash tier) as the primary configuration — weak-
model handling only gets exercised by a model that actually fails. Keep a **local CLI
coder** configuration ready to flip to for the TC-PAR-* parity runs.

**Connections.** The full fixture set: Google, Stripe (**test-mode**), SendGrid, Notion,
AdGuard (LAN address), Open-Meteo, Wikipedia. Optionally a rotating-refresh-token
provider (Atlassian) for TC-CONN-12/13.

**Secrets.** `CODER_KEY_OPENROUTER`, `SEARCH_KEY_BRAVE`, `TEST_LOGIN_PASSWORD`.

**MCP.** The server from P9, added and synced.

**Chat app.** Telegram bot #2, plus Discord if available.

**Knowledge base.** The performance corpus B1–B9 from the reset spec, generated by the
seeder from a fixed RNG seed so two builds are byte-identical and perf numbers compare.

**Agents.** S1, S2, S3, S7, S10, S11, S12.

**Charters that run here.** Section A (KB) in full, Section D (search + browser),
Section G (MCP), Section I (isolation), and the bulk of the agent-designer charter.

---

## Stage 3 — The deliberate fixtures

These were going to be salvaged from the old install. On the from-zero path they are
**built on purpose**, which is strictly better: each is reproducible, and each is
documented as to what it proves.

| Fixture | How to create it | Proves |
|---|---|---|
| **F1 — overbind** | In `Testing`, connect Stripe test-mode with `account_label` / identity literally `test`. Then build a DNS/uptime watchdog agent whose `# Connections:` header contains the word "test" | TC-BIND-1. A binding is a grant of live payment credentials; the recorded bug bound Stripe to a DNS watchdog by substring match |
| **F2 — empty schedule timezone** | Create a schedule normally, then `UPDATE agent_schedules SET timezone='' WHERE …`. Every new schedule now writes a zone, so this row cannot arise from the interface — that is precisely the pre-migration shape being tested | TC-TZ-5. Empty must mean host-local, and must not re-time an existing schedule |
| **F3 — corrupted state** | Take a working agent's `state.md` and move its JSON **one line below** the fence | TC-RUN-9. This exact shape burned ~930k tokens/hour undetected, silently, with `exit=0` |
| **F4 — file-kind boundary** | Generate a file of exactly 1 MiB and one of 1 MiB + 1 byte | TC-KB-7 |
| **F5 — non-UTF8** | A file of raw non-UTF8 bytes | TC-KB-7, binary download-only panel |
| **F6 — shared family identity** | Several `google_*` connections in `Testing`, all carrying the same address | TC-BIND-2 |
| **F7 — dominant column** | Fixture B1: ~150 KB, ~100 rows, one JSON-blob column ≈88% of bytes | TC-KB-2/3, S10. The single most important fixture in the testbed |

F2 and F3 need direct SQL or direct file writes because **the interface cannot produce
them** — which is the point. They are the shapes that arrive from migration and from
model error, not from a user.

---

## Stage 4 — Execution order

Charters are defined in the two companion documents; this is the order to run them.

| Wave | What | Where | Depends on |
|---|---|---|---|
| W1 | Setup + health (TC-SETUP-*, TC-HLTH-*) | host | Stage 0 |
| W2 | Isolation + backup (Section I) | all three | Stage 2 complete |
| W3 | KB (Section A) | Testing, Personal | corpora seeded |
| W4 | Reminders + timezones (Section B) | Personal, Work | Work on NY zone |
| W5 | Connections (Section F) | Testing, Work | Stage 1 keys |
| W6 | MCP (Section G) | Testing | P9 |
| W7 | Chat + search + browser (C, D) | Testing, Personal | — |
| W8 | Skills (Section E) | Testing | — |
| W9 | Chat apps + parity (Section H) | all three | three bots |
| W10 | **Agent designer charter** | all three | everything above |
| W11 | UI polish (Section J) | Personal | — |
| W12 | Launch assets | Personal (+ Work) | W11 green |

**W1–W9 before W10.** The designer charter is the expensive one — a create build now
runs a dry run, one measured over 1.5M tokens — and it consumes connections, skills,
MCP and the KB. Finding a broken connector *during* an expensive build wastes the
build. Run the cheap structural waves first.

**W2 immediately after Stage 2**, not later. If workspace isolation is broken, every
subsequent result is suspect, and it is a five-minute check.

## Stage 5 — Launch assets

Capture from `Personal`, with `Work` for the multi-tenant shots. Both themes, at 1440
and 2560 wide. The shot list: the KB browser on a well-formatted note; the editor with
the bubble toolbar and AI actions open; a callout/columns/toggle note; the agent
designer mid-conversation with a settled plan and the Build button; the spec view; a
finished build's review card; an agent run transcript with tool calls; the run list
showing silent / failed / normal rows; the connections page; the MCP servers section;
the inbox with grouped notifications; the workspace switcher with three distinct icons;
chat with a KB-grounded answer; the Telegram side-by-side with the same conversation.

Run Section J **immediately before** this, not weeks earlier — it is the wave that gates
whether the screenshots are usable.

## Time and cost

| Stage | Effort |
|---|---|
| 0 — host and owner | ~20 min |
| 1 — prerequisites | ~45 min, mostly waiting on third parties |
| 2 — three workspaces | ~2 h, of which Personal's showcase corpus is most |
| 3 — fixtures | ~30 min |
| 4 — charters W1–W9 | ~4–6 h |
| 4 — W10 designer charter | **the long pole**; 12 builds plus edits and reruns |
| 5 — launch assets | ~2 h |

Budget the designer charter separately in tokens, not hours. Record the portfolio's
total from `agent_runs` — it answers whether the designer is something people use
casually or ration, which is a launch-copy fact and not only a test result.

## Open decisions

Still yours, and all cheap to change before Stage 2:

- **Slack for `Work`, or a third Telegram bot?** Slack covers a third adapter and its
  known reconnect gap (TC-APP-13); a third Telegram bot is five minutes and covers
  parity only.
- **A rotating-refresh-token provider in `Testing`?** Atlassian would cover TC-CONN-12/13
  properly. Without one, refresh-rotation handling stays untested.
- **How faithful should `Work` be to the real `kroute.ai` account?** Real mail makes S4
  and S5 meaningful and puts a live account behind an agent that drafts and sends.
  The approval gate is the mitigation; it is not a guarantee.
- **Keep the old `~/.rookery` until the testbed is proven?** Recommended — move it
  aside, delete it only once all three workspaces are green.
