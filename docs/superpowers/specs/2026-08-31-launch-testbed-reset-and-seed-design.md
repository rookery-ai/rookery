# Launch testbed: reset, workspace topology and demo seed

**Status:** proposed
**Date:** 2026-08-31
**Companions:** `2026-08-31-feature-test-book-design.md`, `2026-08-31-agent-designer-test-charter-design.md`

## Goal

Put this server into a known, reproducible state that serves three jobs at once:

1. **A clean slate** — no leftover experiments masquerading as data.
2. **A screenshot-grade knowledge base** — well-formatted documents that make the
   product look like what it is, for launch assets.
3. **A performance and correctness testbed** — corpora shaped to exercise the
   paths that actually break, not merely large ones.

The output is a re-runnable seeder, not a one-off afternoon. The next clean slate
must cost minutes.

## What is on the server today

Measured 2026-08-31 against `~/.rookery/rookery.db` (read-only) and `/healthz`.

| Fact | Value |
|---|---|
| Running server | v0.11.0, commit `96729ee` (= `origin/main` tip) |
| Local `main` checkout | 24 commits behind `origin/main` |
| Sandbox | Landlock ABI 8, enabled |
| Host tools | python3, rg, pdftotext, tesseract, **browser** all present |
| Workspaces | `Ilija Personal`, `test1`, `dqw` |
| Orphan vault dirs | 2 (`10dc2b2f…`, `d253a88c…`) with no workspace row |
| Agents | 4 (`amazon watcher`, `Check time`, `image-watch`, `vodovod`) |
| Runs / chats / messages / inbox | 49 / 70 / 501 / 129 |
| Service connections | **17, all in `Ilija Personal`** (6 Google `NEEDS_REAUTH`) |
| Secrets | **7 in `Ilija Personal`**, 2 in `test1` |
| Chat platforms | Telegram (Personal), Discord (test1) |
| MCP servers | **0** |
| Skills (user) | 1 |

Three of these facts drive the whole design.

**Every credential lives in one workspace.** `Ilija Personal` holds all 17 service
connections and all 7 meaningful secrets — including `CODER_KEY_OPENROUTER`, which
is the key the platform's own coder runs on. "Delete the existing workspaces"
taken literally destroys every OAuth token on the install and the key that makes
the product think. That is recoverable only by re-consenting to a dozen providers
by hand.

**Connector tokens are portable across workspaces.** `service_connections` is
`UNIQUE(workspace_id, provider, account_label)` — scoped to the workspace, so the
same provider may exist in several — and every token column is
`secrets.EncryptWithSystemKey`, which is workspace-agnostic (that is how the
scheduler decrypts headlessly). A connection row can therefore be **copied** into
another workspace and still decrypt, with no second OAuth consent.

**Secrets are not portable.** `secrets` rows are AES-GCM under a key derived from
the workspace's own master password and `secrets_salt` (Argon2id). A copied row
into a workspace with a different salt is undecryptable ciphertext. Re-encrypting
them across workspaces is possible in principle — `workspaces.encrypted_master_password`
is itself system-key encrypted — but it means writing crypto plumbing to save
retyping three API keys. **Rejected.** Secrets are re-entered by hand.

## Decision: rename and purge, do not delete

`Ilija Personal` is **renamed to `Personal` and purged of content**, not deleted.
Its `workspaces` row survives, so its `secrets_salt`, `encrypted_master_password`,
17 connections, 7 secrets and Telegram bot stay valid exactly where they are, with
no key handling of any kind.

Purged from it: agents, agent runs, agent schedules, chats, chat messages, inbox
messages, reminders, and the entire vault directory (re-seeded from scratch).

Deleted outright: `test1`, `dqw`, and the two orphan vault directories.

Created fresh: `Testing`, `Work`.

This is materially cleaner than a full wipe: the end state is identical from the
interface, and it costs zero re-consents.

### Connection fan-out

`Testing` and `Work` receive working connections by **copying rows**, not by
re-consenting:

```
INSERT INTO service_connections (id, workspace_id, provider, account_label, …)
SELECT <new uuid>, '<target-ws>', provider, account_label, …
FROM service_connections WHERE workspace_id = '<personal>' AND provider IN (…)
```

Three caveats, each of which decides what may be copied:

- **A `NEEDS_REAUTH` row copies as a dead connection.** The 6 Google rows are
  already dead. Re-consent Google **once in `Personal` first**, then copy. Copying
  before re-consent produces three broken tenants instead of one.
- **A rotating refresh token cannot be shared.** `DBTokenStore` persists refresh-token
  rotation (Atlassian does this). Two rows holding one rotating token race: the
  loser's token is invalidated and it flips to `NEEDS_REAUTH`. So copy freely for
  API-key providers (Stripe, SendGrid, Mailchimp, OpenAI, AdGuard, and the keyless
  ones) and non-rotating OAuth (Google, GitHub, Notion); do **not** copy a provider
  known to rotate.
- **Copy deliberately, not wholesale.** `Testing` gets the full set because it is the
  crash-test tenant. `Work` gets only what its scenarios need (Google, SendGrid).

### Fixtures preserved on purpose

Two live artefacts are better test fixtures than anything synthetic, and both must
survive the purge:

- **The Stripe connection with `account_identity = 'test'`.** This is the live
  reproduction of the recorded overbind incident: a header containing the substring
  `test` granted a DNS-watchdog agent the owner's payment credentials, because
  `parseConnectionsLine` contains-matched a short shared identity. Keeping it makes
  the regression testable rather than theoretical. See the designer charter, TC-CONN-1.
- **The schedule timezone pair.** `amazon watcher` has an **empty** `agent_schedules.timezone`
  (pre-migration-014) and `vodovod` has `Europe/Skopje`. That pair is the control for
  "empty means the host's local zone, and no existing schedule moves." Record both
  rows before purging the agents, and re-create the pair in `Testing`.

## Workspace topology

| | **Testing** | **Personal** | **Work** |
|---|---|---|---|
| Purpose | crash-test, perf, hostile fixtures | screenshots, launch assets | second-tenant isolation proof |
| Origin | fresh | renamed + purged `Ilija Personal` | fresh |
| KB | bulk perf corpus + edge cases | showcase corpus (hand-crafted) | small realistic work corpus |
| Coder | `api` / openrouter cheap model; **flipped to local CLI** for backend parity | `api` / a strong model (screenshots show good output) | `api`, different model from Testing |
| Timezone | `Europe/Skopje` | `Europe/Skopje` | **`America/New_York`** (deliberate mismatch) |
| Connections | full copied set + the `test` Stripe fixture | the 17 originals, in place | Google + SendGrid only |
| Chat app | own Telegram bot (+ Discord if available) | own Telegram bot | own Telegram bot (or Slack) |
| MCP | the MCP server under test | none | none |
| Agents | the hostile + protocol scenarios | 3–4 polished, screenshot-worthy | 2–3 work scenarios |

`Work` runs on `America/New_York` on purpose. Timezone correctness is impossible to
test convincingly when every tenant shares the server's zone — a wrong conversion
and a right one produce the same wall-clock. A tenant 6–7 hours off makes the
difference visible in a single screenshot of a schedule.

## Knowledge-base corpora

### Track A — showcase (`Personal`, ~50 notes; `Work`, ~20)

These exist to be photographed. They must look like a real person's knowledge base
after a year of use, not like generated filler: a home-server runbook, project
notes, meeting notes with action items, a reading list, trip planning, recipes, a
"Rookery launch plan" note, a household-bills tracker.

Coverage requirement — between them the showcase notes must exercise every editor
construct at least once, because each is a distinct screenshot and a distinct
serializer path: callouts, toggle lists, `<div align>` block alignment, 2/3/4-column
layouts, both colour marks, underline, resized images, tables (incl. a pipe-bearing
cell), wikilinks with live backlinks, code blocks, and task lists.

**Every showcase note is written through a real save, not dropped on disk.**
`checkFidelity` opens a note **read-only** when its serialized form is not the
canonical one, and hand-authored markdown reliably hits the non-canonical spellings
— glued `<details><summary>`, `style=`-spelled alignment, a `div` carrying both
`align` and `data-cols`. A note that opens read-only looks broken in precisely the
screenshot it was written for. The seeder therefore writes via
`PUT /api/v1/kb/note` and the test book asserts that **every showcase note opens
editable** (feature book, TC-KB-9).

### Track B — performance corpus (`Testing`)

Volume alone does not test what the KB navigation work built. The corpus reproduces
**shapes**:

| # | Fixture | Shape | What it tests |
|---|---|---|---|
| B1 | `reporting/api-transactions.md` | ~150 KB, ~100 rows, one JSON-blob column ≈ 88% of bytes | `kb_file_map` `dominantShare` warning; `ModestColumns` default projection; the recorded 30-turn blind-paging failure |
| B2 | `reporting/expenses-2026.md` | 10 000 rows × 8 modest columns (~3 MB) | `kb_table_query` group/sum/avg at scale; truncation pointing at column count |
| B3 | `handbook/platform-guide.md` | ~500 KB, 300+ headings, deep nesting | heading outline; `read_file` `section:`; `search_files` `path:` |
| B4 | `edge/exactly-1mib.txt`, `edge/over-1mib.txt` | exactly 1 MiB, and 1 MiB + 1 byte | KB file-kind boundary (`code` at the boundary) |
| B5 | `edge/photo.bin` | non-UTF8 bytes | `binary` kind, download-only panel, content omitted |
| B6 | `mesh/` | 200 notes, dense `[[wikilinks]]`, incl. broken links | `LinkIndex`, backlinks, unresolved-link rendering |
| B7 | `archive/` | ~2000 notes across 25 folders, 8 levels deep, mixed sizes | tree render, `FolderSummary` per-folder budget and the `…and N more folders` marker, rg vs Go-fallback agreement |
| B8 | `edge/unicode.md` | emoji, RTL, CJK, combining marks, a 200-char filename | path handling, search, display |
| B9 | `edge/dates.md` | a table with a date column and out-of-order months | date grouping ordered by KEY, not value (the recorded 08/06/05/07 bug) |

**Measured baselines** to record before any tuning, so a later regression has
something to be a regression against: KB tree first paint, `search_files` p50/p95
across the whole corpus, note open for a 500 KB document, `kb_file_map` on B1 and B2,
`kb_table_query` group-by on B2, and full-text search via the SPA.

B1 is the most important fixture in this document. It is the exact shape that
exhausted a 30-turn budget returning an empty completion, and it is the one that
proves the fix rather than merely exercising the code.

## The seeder

`cmd/seedtestbed` — a tracked dev harness, following the `cmd/livecheck` precedent.

**Not a `rookery` subcommand.** `check_cli_coverage` asserts every top-level command
in `cmd/rookery` has a `## ` section in the website's `reference/cli.md`; adding
`rookery demo` creates a documentation obligation on a developer tool that no user
should ever run.

Properties:

- **Idempotent and re-runnable.** Re-running converges on the same state rather than
  duplicating. This is the difference between a testbed and an afternoon.
- **Drives the JSON API** for anything a user could do (workspaces, KB writes, agents,
  secrets, reminders), so seeding exercises the real code paths — including the
  fidelity round-trip. Direct SQL is used only for the connection fan-out and the
  purge, which have no API.
- **Phased and resumable** — `--phase reset|topology|kb-showcase|kb-perf|agents|fixtures`,
  so a failure halfway does not mean starting over.
- **`--dry-run` prints the plan and touches nothing.** The destructive phase must be
  inspectable before it runs.
- Generated corpora are produced from seeded RNG so two runs yield byte-identical
  files, and a perf measurement is comparable across resets.

## Sequence

**Phase 0 — safety.** `rookery backup` to a passphrase-encrypted `.rkb`, plus a raw
copy of `rookery.db` and a `git`-tracked dump of the fixture rows to preserve
(the Stripe row, both schedule rows). Then `rookery backup verify <name>`, which
**decrypts and checksums the snapshot without restoring it** — so it proves both the
passphrase and the archive's integrity, which is the whole reason this step exists.
An unverified backup is an assumption, and Phase 2 is irreversible without a real one.

**Phase 0b — deploy from a known checkout.** The server currently on `:8080` is the
`feat+browser-automation` worktree, and local `main` is 24 commits behind it. Deploy
from a checkout you can name before touching data: `make deploy` prints success even
when another worktree's process is holding the port, so assert the running build is
yours — `curl -s localhost:8080/healthz` must report the commit you just built.

**Phase 1 — human-gated prerequisites** (front-loaded so they parallelize with the
build; see below).

**Phase 2 — reset and topology.** Purge `Personal`; delete `test1`, `dqw`, orphan
vaults; create `Testing` and `Work`; fan out connections; re-enter secrets.

**Phase 3 — seed.** Showcase corpus, perf corpus, agents, reminders, fixtures.

**Phase 4 — perf baseline.** Record the measurements above into
`docs/testing/baseline-2026-08-31.md`.

**Phase 5–6 — execute the test books** (companion specs), then capture launch assets.

## Human-gated prerequisites

These need your hands and nothing else blocks on them, so do them while the seeder
is being written:

1. **Two more Telegram bots** via BotFather — one for `Testing`, one for `Work`.
   `platform_connections` is `UNIQUE(workspace_id, platform)` and a bot token cannot
   serve two workspaces, so chat-app parity across three tenants needs three bots.
   (~5 minutes.)
2. **Re-consent Google in `Personal`** — 6 rows are `NEEDS_REAUTH` and must be live
   *before* the fan-out copies them. (~5 minutes.)
3. **An MCP server URL + token.** Zero are configured, so nothing in the MCP surface
   has ever been exercised on this install. Recommendation in the feature book.
4. **The OpenRouter API key to hand**, for re-entry into `Testing` and `Work`.
5. *Optional, for 3-platform parity:* a Discord app for `Testing` and a Slack app
   (bot token + app-level token) for `Work`.

Not gated: the browser runtime is already installed (`/healthz` reports
`tools.browser: true`).

## Decisions you may want to overturn

Recorded explicitly because each was made without asking, and each is cheap to
reverse *before* Phase 2 and expensive after.

| Decision | Rationale | Cost of the alternative |
|---|---|---|
| Rename+purge `Personal` rather than delete it | Preserves 17 connections, 7 secrets, the Telegram bot, with zero crypto handling | A true delete costs ~12 OAuth re-consents and re-entering every secret |
| Secrets re-entered by hand, not migrated | ~3 keys; migrating means writing crypto plumbing | ~40 lines of Go handling master passwords, for two minutes of typing |
| `Work` on `America/New_York` | Makes timezone bugs visible in one screenshot | Same-zone tenants make a wrong conversion indistinguishable from a right one |
| `Testing` gets copies of live connections | Real credentials find real bugs; mocks find mock bugs | Genuine risk: a misbehaving test agent acts on a live account. Mitigated by the build-phase guard and approval gating, not eliminated |
| Perf corpus ~2000 notes, not 50 000 | Shapes find the bugs; volume mostly finds disk | If you want a stress number for launch copy, B7 scales by one flag |
| Keep the `test`-identity Stripe fixture | Live reproduction of a recorded credential-overbind bug | Cleaning it away removes the only real instance of the bug |

## Out of scope

Multi-owner testing (the platform is single-owner by design), upgrade/rollback
testing across versions, container and package installs (both covered by CI gates),
and load testing beyond single-user latency — this is a home server with one owner.
