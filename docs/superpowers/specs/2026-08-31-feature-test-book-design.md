# Feature test book

**Status:** proposed
**Date:** 2026-08-31
**Companions:** `2026-08-31-launch-testbed-reset-and-seed-design.md` (prerequisite),
`2026-08-31-agent-designer-test-charter-design.md` (the centerpiece, separate)

## How to use this

Every charter is `TC-<AREA>-<n>`, with **preconditions**, **steps**, **expected**, and
**evidence** — the artefact that proves it passed. A charter without evidence is an
opinion. Evidence is one of: a screenshot path, a `logs/server.log` grep, an API
response body, or a database row.

**Record the failure, not just the tick.** The point of this pass is to find things
before launch, so a charter that fails gets an issue with the evidence attached and
the run continues. Do not stop the book on the first red.

Priority: **P0** blocks launch, **P1** should be fixed before launch, **P2** is a
known-gap note.

Three properties are asserted across many charters rather than in one, because they
are where this codebase has repeatedly shipped defects:

- **Slice fields never marshal to `null`** — assert on RAW response bytes. A test that
  decodes into `[]string` erases the distinction, and this has unmounted a whole route
  in production.
- **Empty states say something.** A blank panel and a working-but-empty panel look
  identical, and the codebase records several bugs whose only symptom was silence.
- **The record and the live view agree.** Anything shown live (SSE progress, run
  activity) must match what the durable record shows afterwards.

---

## 0. First-run setup (P0 — only testable once)

This section exists because the from-zero rebuild is the **only** opportunity to test
it. An already-configured install cannot exercise onboarding, and this is the exact
path a launch-day user walks. Run it deliberately, as a new user would, and resist
scripting around any step.

| ID | Charter | Expected | Evidence |
|---|---|---|---|
| TC-SETUP-1 | `rookery onboard` on an empty install, interactively | Keys generated, owner created, host tools reported, browser offered, service step completes; output stays short enough to actually read | terminal capture |
| TC-SETUP-2 | `rookery onboard` where the browser runtime is **already** installed | Says **nothing** about it — the silence is deliberate | terminal capture |
| TC-SETUP-3 | `rookery onboard --non-interactive` | Reports what to do; **prompts nothing, acts on nothing** | output |
| TC-SETUP-4 | First sign-in, then the setup wizard through to Done | Completes; the coder step's **Skip** is present and works | screenshots |
| TC-SETUP-5 | Finish the wizard **with** a coder configured | Done offers exactly one action — "Explore what you can do!" — which opens a chat with the opening question **already sent** | screenshot |
| TC-SETUP-6 | Refresh that intro chat, and re-open it | The question is **not** re-asked and no second coder call is spent | transcript + log |
| TC-SETUP-7 | Finish the wizard having **skipped** the coder | Done offers "Create your first agent" instead — never a chat with nothing behind it | screenshot |
| TC-SETUP-8 | Ask that intro chat "what is the purpose of this platform?" | Answers about the platform's own features; **no `[CHAT]`/`[SILENT]`/`[STATE]` markers** | transcript |
| TC-SETUP-9 | Create the first workspace and enter it | Master password prompted; icon picker offers 36 presets; an unset icon renders the default mark | screenshots |
| TC-SETUP-10 | `rookery owner bootstrap` a second time | Refuses cleanly rather than creating a second owner | output |
| TC-SETUP-11 | Reach the app before any workspace exists | Owner-scoped surfaces work; workspace-scoped ones return `no_workspace` rather than erroring opaquely | responses |

## A. Knowledge base (P0)

| ID | Charter | Expected | Evidence |
|---|---|---|---|
| TC-KB-1 | Open the tree in `Testing` (~2 300 notes, 25 folders, 8 deep) | Renders; deep nesting navigable; dotfiles hidden; no horizontal page scroll | screenshot + timing |
| TC-KB-2 | `kb_file_map` on B1 (`api-transactions.md`) via chat | Reports ~100 rows, column list, token cost, **and names `apiTransaction` as ~88% of the file** | chat transcript |
| TC-KB-3 | Ask chat "how much did I spend in total?" over B1 | Answers from `kb_table_query`; does **not** page the file blindly; does not exhaust the turn budget | transcript + `chat: turn finished` log |
| TC-KB-4 | `kb_table_query` group-by-month sum on B2 (10 k rows) | Correct totals; **months ordered by key** (not 08, 06, 05, 07); default projection drops the dominant column | response |
| TC-KB-5 | `read_file` with `section:` on B3; `search_files` with `path:` | Returns just that heading / just that file's hits | responses |
| TC-KB-6 | Same `search_files` query with `rg` present, then with it hidden from `PATH` | Ripgrep and Go-fallback results **agree** | two responses, diffed |
| TC-KB-7 | Open B4 pair and B5 | 1 MiB → `code` (read-only monospace); over → per implementation; non-UTF8 → `binary`, download-only, content omitted | screenshots |
| TC-KB-8 | Backlinks on the B6 mesh; open a note with a broken link | Backlinks list correct; unresolved link visibly distinct, does not error | screenshot |
| TC-KB-9 | **Open all ~50 showcase notes in `Personal`** | Every one opens **editable**, none read-only (`checkFidelity` canonical) | scripted assertion + screenshots |
| TC-KB-10 | Round-trip each editor construct: callout, toggle, alignment, 2/3/4 columns, both colour marks, underline, resized image, pipe-bearing table cell | Saves, reloads identical, still editable | per-construct screenshots |
| TC-KB-11 | Table editing: insert/delete row and column, every picker size 1×1–8×8, header-row toggle | Correct structure; headerless table promotes first row on save (stated caveat) | screenshot |
| TC-KB-12 | Selection assist: improve / proofread / reformat / explain | Three return replacements; **`explain` returns commentary and cannot be pasted over the prose**; no `[CHAT]`/`[STATE]` markers in any output | four screenshots |
| TC-KB-13 | Over-cap selection (>16 KiB) to assist | **Rejected**, not silently truncated | error response |
| TC-KB-14 | Ask chat to edit the currently-open note | Note updates in the browser without reload when clean; when dirty, a toast with Reload and **no silent overwrite** | screen recording |
| TC-KB-15 | Path traversal: `../../etc/passwd` on note read, new, rename, delete | All rejected; error text leaks no absolute path | four responses |
| TC-KB-16 | Slash menu with the caret on the last line of a long note | Menu placed **within the viewport**, not below the fold | screenshot |
| TC-KB-17 | Scroll to the bottom of a long note and keep scrolling | Page does not scroll behind the shell; rail and context pane stay put | screen recording |
| TC-KB-18 | Export a showcase note to HTML / PDF / DOCX | Exports; font embedded in HTML/PDF; **degradations are the documented ones** (toggle loses summary text, colour marks lose the wrapper, callouts render `[!kind]` literally, resized image shows `\|420` in alt) | three files |
| TC-KB-19 | `rookery kb convert` a PDF, a DOCX and a scanned PDF into the vault | Notes created; **the scanned PDF's frontmatter carries a thin-extraction warning** | three notes |
| TC-KB-20 | Upload a >25 MiB file to the KB | Rejected at the `iolimit` cap, **not truncated** | error |

## B. Reminders and timezones (P0)

Timezone bugs are invisible when every tenant shares the server's zone. `Work` is on
`America/New_York` for this reason.

| ID | Charter | Expected | Evidence |
|---|---|---|---|
| TC-TZ-1 | In `Personal` (Europe/Skopje) create reminders by phrase: "in 10 minutes", "tomorrow at 3pm", "next Tuesday at noon" | All parse; stored instants correspond to **Skopje** wall-clock | rows + UI |
| TC-TZ-2 | Same three phrases in `Work` (America/New_York) | Resolve to **New York** wall-clock, not Skopje, not UTC | rows |
| TC-TZ-3 | Profile timezone unset on a fourth throwaway workspace | Falls back to UTC without error; no crash on free-text zone (`""`, `CEST`, `UTC+2`) | rows |
| TC-TZ-4 | Ask the designer for an agent that runs "every Monday at 08:00" in `Work` | Cron is `0 8 * * 1` **and** `agent_schedules.timezone` = `America/New_York` — the model must **not** pre-convert to server time | AGENT.md + DB row |
| TC-TZ-5 | The preserved fixture pair: empty-timezone schedule vs `Europe/Skopje` | Empty behaves as **host local** and its `next_run_at` is unchanged by the presence of the column | two rows before/after restart |
| TC-TZ-6 | Stop the server past a reminder's due time; restart | Delivered within seconds of boot; **>2h late relabels "⏰ Delayed reminder"** | inbox row |
| TC-TZ-7 | Stop the server across ≥3 slots of an hourly agent; restart | Runs **once**, not once per missed slot; cron phase not drifted | run rows |
| TC-TZ-8 | Kill the server mid-run; restart | The interrupted run is retried **exactly once** (`trigger='cron-retry'`); a second interruption is **not** retried again | run rows |
| TC-TZ-9 | Five overdue agents at once | At most 3 run concurrently; the rest are delayed, none dropped | run timestamps |
| TC-TZ-10 | Delete a reminder, then Undo within 5s | No DELETE is issued at all; row intact | server log absence |
| TC-TZ-11 | Delete a reminder and navigate away immediately | The pending delete **flushes**, not silently dropped | row gone |

## C. Chat (P0)

| ID | Charter | Expected | Evidence |
|---|---|---|---|
| TC-CHAT-1 | Ask "what can this platform do?" in a fresh chat | Answers about KB, agents, skills, reminders, connections, secrets, MCP, providers, coders, chat apps | transcript |
| TC-CHAT-2 | Ask chat to say nothing / be quiet | A short sentence. **Never `[SILENT]`, `[CHAT]`, `[STATE]` or any marker** | transcript |
| TC-CHAT-3 | Ask chat to *explain* the output protocol | Explains it; the code spans and prose survive intact — the cleaner is line-anchored and must not empty a code span | transcript |
| TC-CHAT-4 | Scan all `chat_messages` rows after the full book | **Zero** leaked markers in assistant rows | SQL count |
| TC-CHAT-5 | Chat creates a note, edits it, reads it back | File-only tools work; **no delete, no rename, no shell** | vault diff |
| TC-CHAT-6 | Ask chat for arbitrary compute ("run this python") | Declines / has no exec tool — chat is file-only by design | transcript |
| TC-CHAT-7 | Chat uses a connector (e.g. "what's my calendar tomorrow?") | Executes; mutating allowed (chat is not a build) | transcript |
| TC-CHAT-8 | Chat reaches an MCP tool | Works via the same `Execute` choke point | transcript |
| TC-CHAT-9 | Send a turn, navigate away, come back | Turn is durable; progress resumes; **timer does not restart at zero** | screen recording |
| TC-CHAT-10 | Two browser tabs on one chat | Both see all lines; neither steals the other's | two recordings |
| TC-CHAT-11 | 30-minute idle | Auto-stops; opening it **auto-resumes once**; a manual Stop afterwards sticks | rows |
| TC-CHAT-12 | Copy a message over plain HTTP on the LAN (`http://192.168.1.194:8080`) | Copy works via the `execCommand` fallback; **never a silent no-op** | manual |
| TC-CHAT-13 | Chat over a note in the KB with a table | Table-aware retrieval quotes the right rows with a header | transcript |

## D. Search and the browser (P1)

| ID | Charter | Expected | Evidence |
|---|---|---|---|
| TC-WEB-1 | `web_search` for a current-events query | Results with a provenance line naming the engine | transcript |
| TC-WEB-2 | Force the keyless cascade to be challenged (repeat rapidly) | Falls through engines; **browser provider runs LAST**, labelled "DuckDuckGo (browser)" | transcript |
| TC-WEB-3 | Exhaust every provider | Returns an **empty result, not an error** — an `error:` would block the tool loop | transcript |
| TC-WEB-4 | `web_fetch` a JS-rendered SPA | Returns a notice naming `browser_read`, because the response has almost no words | transcript |
| TC-WEB-5 | `browser_read` the same page | Returns real body text | transcript |
| TC-WEB-6 | `browser_open` a loopback/RFC1918 URL (e.g. the connector bridge) | **Refused at the CONNECT proxy** | transcript + log |
| TC-WEB-7 | A public hostname that resolves into private space; and a redirect into private space | Both refused — the guard is at dial, not URL inspection | transcript |
| TC-WEB-8 | `ROOKERY_BROWSER_ALLOW_PRIVATE=1`, read a LAN dashboard | Allowed; **startup logs a warning** | log line |
| TC-WEB-9 | While a browser run is live, inspect the process tree | Chromium is in the **sandboxed helper**, not the server process | `ps` output |
| TC-WEB-10 | `browser_fill` + `browser_click` + `browser_press` a form on a test site | Acts; gated by `browser.CheckAct` identically on API engine and CLI bridge | transcript |
| TC-WEB-11 | An agent **without** `browser_irreversible` asked to do something irreversible | Refused by the permission, not by luck | transcript |
| TC-WEB-12 | Temporarily hide the playwright cache | Browser tools are **not offered at all**; `/healthz` reports `browser: false`; **no warning is emitted** (deliberate) | healthz + transcript |

## E. Skills (P1)

| ID | Charter | Expected | Evidence |
|---|---|---|---|
| TC-SKILL-1 | List core skills in the UI | 22 render; **`requires` never `null`** on any of them (raw bytes) | response bytes |
| TC-SKILL-2 | Create a skill via `/skill` in chat and via the SPA | Both reach `StateVerifying` with the same FSM | two transcripts |
| TC-SKILL-3 | A skill whose script drives a CLI tool (list-form `subprocess`) | **Allowed** under `ProfileSkillScript` | build output |
| TC-SKILL-4 | A skill using `eval` / `os.system` / `shell=True` | **Blocked** by AST guardrails | build output |
| TC-SKILL-5 | A skill that reads `USER.md` and posts it to a remote host | **Vetter blocks the save**; user stays in `StateDesigning`; skill not written | transcript + vault |
| TC-SKILL-6 | A skill whose vetting output *echoes the option list* `✅ … \| ⚠️ … \| ❌ …` | Does **not** block — only a pure `❌` verdict blocks | transcript |
| TC-SKILL-7 | Name a skill after a core skill | Rejected (reserved) | error |
| TC-SKILL-8 | Save a skill with the same name twice | In-place overwrite, no duplicate row | rows |
| TC-SKILL-9 | Agent declares `# Skills: pdf, csv` | Exactly those two in `agent_skills`; not all, not none | rows |
| TC-SKILL-10 | Agent emits **no** skills header | `SelectSkills` fallback runs; failure is **closed** (empty + warning), not "all skills" | rows + log |
| TC-SKILL-11 | Agent emits `# Skills: none` | Respected; fallback does **not** run | rows |
| TC-SKILL-12 | Hand-curate skills on the agent page, then run an edit build | Curation survives — the header is rewritten from the DB before the coder sees it | rows |
| TC-SKILL-13 | Attach a skill requiring a missing binary; run the agent | `<skill_environment>` names the absolute path or tells it to install; no silent failure | run log |
| TC-SKILL-14 | Cancel a skill build mid-flight; check the vault | Staging dir `.staging-<name>` cleaned; live skills folder untouched | vault |

## F. Connections (P0)

| ID | Charter | Expected | Evidence |
|---|---|---|---|
| TC-CONN-1 | Connect a provider by OAuth end to end (Google) | Consent → callback → `ACTIVE`; the redirect URI shown in setup steps is **copyable code, not a link** | screenshots |
| TC-CONN-2 | Connect an API-key provider; connect a keyless one (Open-Meteo) | Key form labels come from the provider YAML (`key_label`/`key_hint`); keyless shows a bare Connect | two screenshots |
| TC-CONN-3 | Connect a self-hosted provider (AdGuard) at its LAN address | Works — connectors deliberately bypass the private-address guard | response |
| TC-CONN-4 | Browse the connections page | 136 providers, grouped by category, each with a logo; **no black or invisible marks** | screenshot |
| TC-CONN-5 | Execute a mutating action during a **build** | Refused by the build-phase guard | build log |
| TC-CONN-6 | Execute the same action during a **run** | Allowed | run log |
| TC-CONN-7 | An action returning a large payload (analytics/ads report) via the **CLI bridge** | Capped at 8 KiB; `truncated: true`; a note telling the model to narrow — **not** a JSON value cut mid-structure | response |
| TC-CONN-8 | Set a binding to `approval_mode=approve`; run an agent that publishes | Call **parked**, coder gets a queue ticket as a **success** (never `error:`); run finishes | run log + `pending_actions` |
| TC-CONN-9 | Resolve that parked action from chat (`/pending`, `/approve`) and from the web inbox | Both work; args re-rendered against a **fresh token** | two transcripts |
| TC-CONN-10 | Race an approve from chat and web simultaneously | Exactly one publish (`ClaimPendingAction` is the lock) | rows |
| TC-CONN-11 | Reject a parked action | No send; the parked result's wording explains the state drift | transcript |
| TC-CONN-12 | Force a refresh failure with a 500 and a 429 | Connection stays `ACTIVE` and **stays in the refresh loop** | rows |
| TC-CONN-13 | Force a definitive 401 | Flips to `NEEDS_REAUTH` **and** delivers an "⚠️ Action required" alert to **both** inbox and chat | inbox row + chat |
| TC-CONN-14 | With no chat platform connected, repeat TC-CONN-13 | Inbox row written **first and independently**; the failing chat send does not suppress it | inbox row |
| TC-CONN-15 | A provider whose base URL is a bare `.lan` host, on the connect screen | Soft warning / hard block per `redirect_policy`; **a policy without `verified: true` never hard-blocks** | screenshot |

## G. MCP (P1 — never exercised on this install)

Zero servers are configured, so this whole surface is untested here. Stand one up in
`Testing`. Recommendation: a self-hosted HTTP MCP server on the LAN with a static
bearer token — it is reachable (MCP deliberately bypasses the dial guard, like
connectors), it costs nothing, and its tool list can be changed on purpose to test
re-sync. A public third-party server is a weaker fixture because its tool list is not
yours to mutate.

| ID | Charter | Expected | Evidence |
|---|---|---|---|
| TC-MCP-1 | Add a server, Test & sync | `initialize` + `tools/list` succeed; the returned tool list is shown **for review before anything is enabled** | screenshot |
| TC-MCP-2 | First sync | Tools arrive **enabled** | rows |
| TC-MCP-3 | Add a tool server-side; re-sync | The new tool arrives **disabled** | rows |
| TC-MCP-4 | Remove a tool server-side; re-sync | Marked **missing**, not deleted; owner's `read_only`/approval/enabled columns survive | rows |
| TC-MCP-5 | A server advertising a dotted 128-char tool name alongside connector actions | Slugged to `mcp__<server>__<tool>`; **the connector tool list still works** (an illegal name would reject the whole list) | tool list |
| TC-MCP-6 | Server advertising >`MaxEnabledToolsPerServer` | UI **states how many were held back**; no silent truncation | screenshot |
| TC-MCP-7 | Tool returns `isError: true` ("date must be in the future") | Delivered **without** the `error:` prefix so the model self-corrects | transcript |
| TC-MCP-8 | Server returns 500 / times out | Gets the `error:` prefix; status `UNREACHABLE`, **not** `NEEDS_AUTH` | rows |
| TC-MCP-9 | Server returns 401 | `NEEDS_AUTH` | rows |
| TC-MCP-10 | Server down; run a bound agent | Tools still offered from cache with a **definitive error** — the agent must not silently lose capability and read as choosing not to act | run log |
| TC-MCP-11 | A server with `readOnlyHint: true` on a mutating tool, during a build | The **owner's `read_only` column** governs, not the server's hint | build log |
| TC-MCP-12 | Same MCP tool from the API engine and from a CLI coder | Identical behaviour through one `Execute` | two run logs |

## H. Chat apps, and parity with the UI (P0)

Parity is the point: anything doable in the SPA should be reachable from chat, and
what is deliberately *not* should be deliberate. Run the parity matrix per platform.

| ID | Charter | Expected | Evidence |
|---|---|---|---|
| TC-APP-1 | Connect Telegram in all three workspaces with distinct bots | Three independent DM loops; a message to one never reaches another | three transcripts |
| TC-APP-2 | Full command sweep per platform: `/agent`, `/skill`, `/run`, `/secret`, `/remind`, `/chat`, `/memory`, `/pending`, `/approve`, `/reject` | Each responds correctly | per-command screenshots |
| TC-APP-3 | Send an unknown command and malformed args | Helpful error, no crash | screenshots |
| TC-APP-4 | A reply containing markdown: bold, code fence, table, link, `_underscores_`, unbalanced brackets | Renders correctly on Telegram (MarkdownV2 escaping) and Discord (CommonMark passthrough) | screenshots |
| TC-APP-5 | A very long reply (> platform limit) | Split or truncated **gracefully**, never dropped | screenshot |
| TC-APP-6 | Send a document, image and PDF to the bot | Ingested under the 25 MiB cap; over-cap **rejected, not truncated** | notes |
| TC-APP-7 | Start an agent design in chat; open the SPA on the same workspace | SPA is a **read-only mirror**; it cannot drive the session | screenshot |
| TC-APP-8 | Start a design in the SPA; watch chat | The finished build is announced in the **browser**, not pushed to Telegram (`chat_suppressed=true`) | log grep |
| TC-APP-9 | Start in the SPA, close the browser, then `/agent cancel` in chat | Cancel is **unconditional** and works — the escape hatch | transcript |
| TC-APP-10 | From the SPA, cancel a chat-owned session | **Refused** (non-owner) | response |
| TC-APP-11 | Deliver an agent notification | Reaches the **inbox and every connected chat app** — all-or-nothing, no per-channel selection | inbox + chat |
| TC-APP-12 | Disconnect the chat app; run a notifying agent | Notification still lands in the inbox | inbox row |
| TC-APP-13 | Slack only: force a reconnect exhaustion | **Known gap** — inbound stops until re-save/restart; outbound still works. Record, do not treat as new | log |
| TC-APP-14 | Discord: a 64-bit snowflake id through agent state | Survives as an integer, not truncated at 2^53 | `state.md` |

## I. Cross-cutting: isolation, backup, health (P0)

| ID | Charter | Expected | Evidence |
|---|---|---|---|
| TC-ISO-1 | From a `Testing` agent, attempt to read `Personal`'s vault, `rookery.db`, `config.yaml`, `system.key` | All denied by Landlock | run log |
| TC-ISO-2 | Enter `Work` with `Personal`'s master password | Refused; re-prompted on **every** switch | screenshot |
| TC-ISO-3 | Global search in `Testing` | Returns **only** `Testing` content | screenshot |
| TC-ISO-4 | Guess another workspace's run id on `GET /api/v1/agents/:id/runs/:runID` | Refused — scoped through the agent | response |
| TC-BAK-1 | `rookery backup` then `verify` | Snapshot written, verified | file |
| TC-BAK-2 | Two snapshots within one second | Distinct names; neither overwrites the other | files |
| TC-BAK-3 | Restore with a wrong passphrase | `ErrBadPassphrase`, nothing changed | output |
| TC-BAK-4 | Restore via the CLI | Completes **in the same command** and says so; server start is *using* it, not performing it | output |
| TC-BAK-5 | Stage a restore from the UI, then `cancel-restore` | Not applied on next boot | boot log |
| TC-BAK-6 | Restore onto a moved data dir | Secrets still decrypt — `system.key` travels **inside** the snapshot | connections still `ACTIVE` |
| TC-BAK-7 | Attempt a restore while the server is running | Refused (pid flock) | output |
| TC-HLTH-1 | `/healthz` | version, commit, Landlock ABI, coder mode, host tools; **no paths**; no warnings on a healthy host | response |
| TC-HLTH-2 | Hide `python3`; `/healthz` and an agent-tool build | Warns; the AST guardrail **self-skips** — confirm this is visible, since generated scripts then run unchecked | response + build log |

## J. Interface polish for launch assets (P1)

Screenshot-blocking defects. Each is a real recorded failure class in this codebase.

| ID | Charter | Expected |
|---|---|---|
| TC-UI-1 | Every page at 1280, 1440, 1920 and 2560 wide | No dead 900 px gutter; wide content scrolls inside its own container, page body never scrolls sideways |
| TC-UI-2 | Light and dark, plus the OS "system" default with no explicit choice | All three correct; no borrowed background |
| TC-UI-3 | Hover every `<button>` | Pointer cursor everywhere, including KB search results |
| TC-UI-4 | Every dialog | Honours its own width; the KB icon picker's tab strip does not burst the modal |
| TC-UI-5 | Bubble toolbar: toggle Bold, then Italic, then a heading | Indicators **update** (not frozen at mount) |
| TC-UI-6 | Settings nav | lucide icons throughout, **no emoji** |
| TC-UI-7 | Resize the context pane; reload | Width persists; keyboard resize works; a corrupt stored value falls back to 256 px |
| TC-UI-8 | The 36 workspace presets at rail size | All legible at 20 px, hues distinguishable |
| TC-UI-9 | Run panel while a build streams, then scroll up | Action bar stays reachable — it renders **outside** the scroll container |
| TC-UI-10 | Agent run list with a silent run, a failed run and a normal run | Three visibly different rows; silent is not indistinguishable from broken |

---

## Test data and evidence layout

```
docs/testing/
├── baseline-2026-08-31.md        # perf numbers, recorded before any tuning
├── results-<date>.md             # per-charter pass/fail + evidence links
└── evidence/
    ├── screenshots/<TC-ID>.png
    └── logs/<TC-ID>.txt
```

Useful greps, since much of the evidence is in `logs/server.log`:

```bash
grep "build_id=<id>"            logs/server.log   # one designer build, end to end
grep "agentrunner: run finished" logs/server.log  # exit, chat_lines, silent, stop_reason, tokens
grep "chat: turn finished"       logs/server.log  # incl. the `empty` field
grep "chat_suppressed=true"      logs/server.log  # origin routing
```

## Sequencing

Run **A, B, I** first — they are P0 and independent of any external service, so they
find structural breakage before you spend tokens. Then **F, G, H**, which need the
human-gated prerequisites. **C, D, E** next. **J** last, immediately before capturing
launch assets, since it is the one that gates the screenshots. The agent-designer
charter runs in parallel from its own document — it is the long pole.

## Out of scope

Concurrency and load beyond single-owner use; container and package installs (both
already CI gates); cross-version upgrade; and the deliberately deferred surfaces
(MCP stdio and OAuth, resources and prompts, webhook chat platforms, per-workspace
restore) — those are known gaps, not test failures.
