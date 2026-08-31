# Agent designer test charter

**Status:** proposed
**Date:** 2026-08-31
**Companions:** `2026-08-31-launch-testbed-reset-and-seed-design.md` (prerequisite),
`2026-08-31-feature-test-book-design.md`

## Why this is its own document

The agent designer is the product. Everything else is scaffolding around "describe
what you want in plain words and get a working, scheduled, connected agent."

It is also the surface with the longest list of defects that reached a real user:
an approval word that silently rebuilt instead of saving, a build button that offered
itself under a clarifying question, a blank assistant bubble that came back on every
reload, action buttons rendered below the fold while the composer sat locked, a
finished build announced in Telegram while the browser stayed empty, a review that
called the model's own prose "what a test run produces", an agent whose state file
was written one line below the fence so it re-derived its baseline forever at ~930k
tokens an hour, a header substring that granted a DNS watchdog the owner's payment
credentials, and a schedule that fired two hours early because the model helpfully
converted the timezone.

Every one of those passed the unit tests. They were found by building agents. So this
charter is organised around **building real agents**, with the FSM and interface
checks layered onto builds that are happening anyway.

## Cost model — read before running anything

A create build is expensive and has become more so: it now performs a **dry run**,
executing the finished agent once before showing it to you. One measured create build
exceeded **1.5M tokens**, and a build plus its dry run takes minutes.

So the charters are split by cost, and the order is not negotiable:

- **Free (F)** — no coder call. Interface, DOM, DB, log inspection. Run these first.
- **Cheap (C)** — a design *conversation* only, aborted before generation. Costs a few
  turns. Most FSM, transcript and button behaviour lives here.
- **Expensive (E)** — a full build to completion. **12 scenarios, budgeted.** Do not
  re-run an E charter to check an unrelated F or C property.

Run every F, then every C, then the E portfolio. A failure found in F that would
invalidate an E build must be fixed before spending the E budget.

Track spend: `agent_runs` carries prompt/completion/total tokens and cache stats, and
`agentrunner: run finished` logs `total_tokens`. Record the portfolio's total — it is
also a launch-copy fact worth knowing.

---

## Part 1 — The scenario portfolio (E)

Twelve builds spanning all three tiers, both coder backends, and every integration
surface. These are the real-world tasks named for this testbed, not toy prompts. Each
is described **the way a non-technical owner would describe it** — the designer's
jargon blocklist means the user should never need to say "script", "cron" or "vault",
and a scenario that only works when phrased technically is a finding.

| # | Scenario | Ask, in the owner's words | Expected tier | Surfaces exercised |
|---|---|---|---|---|
| S1 | **Page monitor** | "Watch this page and tell me when it changes." | 1 | `web_fetch`, `change-detection` skill, `state.md` baseline, `[SILENT]` on no change |
| S2 | **JS page monitor** | "Same, but for this site" (a SPA) | 1–2 | `web_fetch` thin-response notice → `browser_read` escalation |
| S3 | **Classified ads filter** | "Check pazar3 for VW Golf under €5000 in Skopje and message me only new ones." | 2 | browser or fetch, dedup via state, large-integer ids, `[CHAT]` delivery |
| S4 | **Email triage + draft** | "Every morning, sort my unread mail and draft replies to the ones that need one — don't send anything." | 2–3 | Gmail connector, `email-triage` skill, build-phase mutating guard, drafts not sends |
| S5 | **Gated email send** | "Send the weekly summary to my team, but let me approve it first." | 2 | `public_write`, `approval_mode=approve`, park → `/pending` → `/approve`, re-render on fresh token |
| S6 | **Morning brief** | "Every weekday at 8, tell me my calendar, the weather, and anything important in my notes." | 3 | multi-connector, KB read, timezone, scheduled delivery |
| S7 | **MCP-backed agent** | a task only the MCP server can do | 1–2 | MCP binding, slugging, error-channel mapping, bridge cap |
| S8 | **KB curation** | "Turn my chats into proper notes in my knowledge base each night." | 1 | `kb-curation`, vault write scope, live-editor adoption |
| S9 | **Utility bill watcher** | "Log into the water utility and tell me when a new bill is out." | 2 | secret injection, monthly cron in Europe/Skopje, browser act |
| S10 | **Spend report** | "How much did I spend last month, by category?" over fixture B1 | 1 | `kb_file_map` → `kb_table_query`; the recorded blind-paging failure |
| S11 | **Silent by design** | "Only tell me if the disk goes over 80% — otherwise stay quiet." | 1 | `[SILENT]` correctness; **must not notify on a normal run** |
| S12 | **Agent collaboration** | a parent that calls S1 | 1 | `[CALL]`, depth limit 3, cycle detection |

Assignment: **S1–S3, S7, S10–S12 in `Testing`; S8, S9 in `Personal`; S4–S6 in `Work`.**
Run **S1 and S4 twice** — once on the API engine, once on a local CLI coder — for
backend parity. That is the only deliberate duplication in the budget.

### What "passed" means for a scenario build

A build is not judged by whether it finished. For each, assert:

1. **Tier is the simplest that solves it.** A `tools/*.py` written for S1 or S11 is a
   failure of `agentPhilosophyBlock`, not a stylistic quibble.
2. **The dry run really ran.** The review says what a test run produced *because
   something executed* — not the model's prose relabelled. For a Tier 1 agent, which
   authors no script and can never set `ScriptVerified`, the dry run is the **only**
   thing that can produce real output. Cross-check `script_ran` / `stop_reason` in the log.
3. **The dry run wrote nothing it shouldn't.** No note in the live KB, no memory edit,
   no outbound message. Diff the vault across the build.
4. **`state.md` is the build's, not the rehearsal's.** A change-detection agent whose
   saved state already says "seen" will stay silent forever on its first real run.
   Inspect the fence after save.
5. **Schedule matches the words**, in the owner's own timezone, with the zone stored.
6. **Bindings are exactly what's needed** — see TC-BIND-1 below.
7. **The first real run delivers.** Run it once manually and read the transcript.

---

## Part 2 — Conversation and FSM (C, aborted builds)

| ID | Charter | Expected |
|---|---|---|
| TC-FSM-1 | Answer three clarifying questions without approving | Stays in `StateDesigning`; **no build starts** |
| TC-FSM-2 | Say "ok", "yes", "sure" **while answering a design question** | Does **not** launch a build — `isApproval` is strict for a reason |
| TC-FSM-3 | Reach a settled plan | `[TECHNICAL SPEC]` is emitted **with the proposal**, `plan_ready=true`, Build button appears |
| TC-FSM-4 | Ask a follow-up question after a settled plan | `plan_ready` **retracts**; button withdraws. A latch-once-true flag is a worse defect than no flag |
| TC-FSM-5 | The `[TECHNICAL SPEC]` block itself | **Never rendered to the user**, live or on reload; both edges strip it |
| TC-FSM-6 | Provoke a spec-only reply (small correction to a settled plan) | **No blank bubble.** A substituted sentence pointing at View spec, and it survives reload |
| TC-FSM-7 | Press the Build button | The phrase it sends matches `isApproval` **exactly** — a renamed button that sends unmatched text silently does nothing |
| TC-FSM-8 | Model omits the spec marker but writes "Type approve and I'll build it" | `planInvitesApproval` fallback opens the gate |
| TC-FSM-9 | On the **skill** designer (no `gateBuildOnPlanReady`) | Build button still offered on every turn; composer **not** locked during ordinary Q&A |
| TC-FSM-10 | Reload mid-conversation | Transcript, state, plan-readiness all restored; `plan_ready` present on **raw bytes**, not absent-coerced-false |
| TC-FSM-11 | Start a design with no session (after completion elsewhere) | **409 `session_ended`** with an explanation, not a bare 400 |
| TC-FSM-12 | Every design turn's timestamps | Present on live turns and on resumed ones; a pre-timestamp draft omits the field rather than showing year 1 |

## Part 3 — The review gate (C + E)

This is where the worst defects lived.

| ID | Charter | Expected |
|---|---|---|
| TC-REV-1 | At `StateVerifying`, reply **`"Approved"`** (past tense) | **Saves.** It must not fall through to a change request — which drops to `StateDesigning`, and the *next* `approve` launches a **second full build** with nothing reporting it |
| TC-REV-2 | Sweep the forgiving vocabulary: `yes`, `save`, `ok`, `looks good`, `confirm`, `go`, `do it`, `ship it`, `lgtm`, `perfect`, `great`, `accepted`, `yep`, `sure`, `sounds good`, `go for it`, `all good` | All save |
| TC-REV-3 | Negative cues: `not yet`, `don't`, `change`, `wait`, `instead` | All read as change requests |
| TC-REV-4 | Same sweep on the **skill** designer | Identical vocabulary — the two share one `DesignerSurface`, and a word that saves an agent but rebuilds a skill is the kind of inconsistency nobody finds until it costs a build |
| TC-REV-5 | Request a change at `StateVerifying`, then approve | The generated agent is **kept in memory**, not discarded; the change is applied on top |
| TC-REV-6 | **Scroll up during a build**, then let it finish | The action bar is reachable — it renders **outside** `ChatScroll`. This was diagnosed twice as logic and was layout |
| TC-REV-7 | In every reachable state | A button is visible **or** the composer is usable. Never neither |
| TC-REV-8 | A turn that **fails** after the dry run lands | The finished build is still visible with Save and Request changes — a failing turn must not hide it |
| TC-REV-9 | Open the Spec tab **before** a build | Shows the `[TECHNICAL SPEC]`, incl. `Connections:`, `Skills:` and `MCP servers:` lines |
| TC-REV-10 | Open it **after** | Shows generated `AGENT.md` + tools; the meta row parses `# MCP:` as well as `# Skills:`/`# Connections:` |
| TC-REV-11 | Cancel a running build | Stops; the draft dir survives for resume |

## Part 4 — Build integrity (E, observed on the portfolio)

| ID | Charter | Expected |
|---|---|---|
| TC-BLD-1 | A build that exhausts its turn budget | `stop_reason` = `budget`/`unproductive`; a **caveat is prepended** — the truncation must not depend on the model remembering to emit `[BLOCKED]` |
| TC-BLD-2 | A Tier 1 build (S1, S11) | Review sample comes from the **dry run**; if nothing executed the message **says so** rather than calling prose a test run |
| TC-BLD-3 | A Tier 2 build whose script fails | `verifyFinishNudge` drives run/inspect/fix, or reports the failure in plain language — it does not declare success |
| TC-BLD-4 | Vault diff across every build | **No writes to the live KB.** `dryRunPrompt` passes no `VaultRoot`, and that omission is the only thing keeping a rehearsal out of the owner's notes |
| TC-BLD-5 | Any build with an outbound-capable connector or secret (S4, S5, S9) | **Nothing is sent.** Check the provider's own sent/outbox |
| TC-BLD-6 | `state.md` after a create build | The build's state, not the rehearsal's; heading carries the real agent name, never blank |
| TC-BLD-7 | Agent dir after save | No test artefacts — no downloads, `_probe.py`, scratch `.json` |
| TC-BLD-8 | Guardrails: ask for an agent that deletes files / uses `eval` | Blocked, with a message the owner can act on |
| TC-BLD-9 | Create-build draft dir | Named `draft_<slug>` from the agent's **name**, recognisable in the KB browser; promoted on save; removed after |
| TC-BLD-10 | Kill the server mid-build; restart; resume the draft | Recovered from disk, not silently lost |
| TC-BLD-11 | `grep build_id=<id> logs/server.log` | Reconstructs one build end to end: start, coder returned, decision, outcome, finished + duration |
| TC-BLD-12 | Watch the build live | Per-tool-call `🔧 …` milestones stream; **not** a frozen "Coder is building your agent…" |
| TC-BLD-13 | Three completion signals | `event: done` on SSE; `onError` refetches state; the 5 s poll covers a proxy that swallows the stream. Test each by disabling the others |

## Part 5 — Bindings, skills, schedule (E, observed on the portfolio)

| ID | Charter | Expected |
|---|---|---|
| TC-BIND-1 | **The overbind fixture.** Build a DNS/uptime watchdog in `Testing`, where a Stripe connection has `account_identity = 'test'`, and let the model write a header containing the word `test` | Stripe is **NOT** bound. A binding is a grant of live payment credentials; the match requires an `@`-bearing identity unique to one connection |
| TC-BIND-2 | An agent named for one Google service, with several `google_*` connections sharing one address | Only the named service binds — a family identity must not bind the whole family |
| TC-BIND-3 | Model omits `# Connections:` entirely | **Auto-bind** to exactly the connections the build's tool calls used. Never all |
| TC-BIND-4 | Explicit header present | It **wins** over auto-bind |
| TC-BIND-5 | Attach a connection by checkbox on the agent page | Persists; survives an edit build |
| TC-BIND-6 | Run vs build exposure | A **build** sees every workspace connection; a **run** sees only bound ones |
| TC-BIND-MCP-1 | S7's MCP binding via `# MCP:` header, and with the header absent | Header honoured; absent falls back to the build's used servers; explicit `none` respected |
| TC-SKD-1 | Skills declared, `none`, and omitted | See feature book TC-SKILL-9/10/11 — verify here on real builds |
| TC-SCH-1 | "Every Monday at 8" in `Work` (America/New_York) | `0 8 * * 1` + `timezone=America/New_York`. **The model must not pre-convert** — this fired two hours early, twice |
| TC-SCH-2 | "Every 10 minutes" | Auto-scheduled from the `# Suggested schedule:` header on save |
| TC-SCH-3 | An agent that should not be scheduled | `none` accepted; no schedule row |
| TC-SCH-4 | Edit an agent's schedule via a build | The existing schedule row's id is **reused** — no duplicate row, no double-firing |

## Part 6 — Editing an existing agent (E, 3 builds)

| ID | Charter | Expected |
|---|---|---|
| TC-EDIT-1 | Open edit on S3 | The designer surface mounts **immediately** with a typing indicator — no full-width pre-screen that jumps layout on first reply |
| TC-EDIT-2 | Report a real bug ("it messaged me about an ad I'd already seen") | **Diagnoses the root cause in plain English first**, proposes a fix, waits for approval — not a superficial patch |
| TC-EDIT-3 | Approve the fix | Targeted change only; re-tested; the original bug demonstrably no longer occurs |
| TC-EDIT-4 | Vault during an edit build | Live agent dir **untouched**; work happens in `<agentID>-edit-staging` |
| TC-EDIT-5 | Hand-curated skills and connections before the edit | Both survive |
| TC-EDIT-6 | With an unrelated create session open in the same workspace | The edit page **vetoes** the recovered session — the design session is a per-workspace singleton and adopting it would offer to save the wrong agent |
| TC-EDIT-7 | Open an edit page and navigate away without typing | **No cancel POST** — it would kill a stranger's in-flight build |

## Part 7 — Post-save runtime (E, on the portfolio's agents)

| ID | Charter | Expected |
|---|---|---|
| TC-RUN-1 | Run S11 when the condition is not met | Silent. **No notification.** A missed `[SILENT]` marker means rule 4 fires and a correctly-behaving agent notifies its owner on every run, forever |
| TC-RUN-2 | Decorated silent markers: `**[SILENT]**`, `` `[SILENT]` ``, `[silent]`, `[SILENT].`, `[/SILENT]`, bare `SILENT` | All recognised |
| TC-RUN-3 | A marker mentioned **inside a sentence** | **Not** matched — swallowing a real message is the worse failure |
| TC-RUN-4 | An agent that forgets `[CHAT]` and writes prose | Prose delivered with a warning recorded |
| TC-RUN-5 | An agent whose model emits tool scaffolding as raw text | **Suppressed** — never forwarded to the owner's phone |
| TC-RUN-6 | An agent producing zero bytes | Recorded `exit -1` and reported as a **failed** run, not a quiet one |
| TC-RUN-7 | A run that succeeds but produces nothing deliverable, without `[SILENT]` | The owner gets the "⚠️ ran but produced no notification" message |
| TC-RUN-8 | Run S3 twice with no new ads | Second run silent; state dedup works; no duplicate notification |
| TC-RUN-9 | Corrupt an agent's `state.md` (JSON one line **below** the fence) and run | **Recovered**, file re-rendered canonically, prose in `## Notes` preserved. This exact shape burned ~930k tokens an hour undetected |
| TC-RUN-10 | Hand-edit `state.md` in the KB browser while the agent runs | `PUT` → **409 `agent_running`** |
| TC-RUN-11 | Open a finished run's transcript | Ordered interleaving of milestones and coder turns; `## Tool calls` in the vault run note |
| TC-RUN-12 | A **cron** run nobody watched | Transcript captured — the collector is in the runner, not the web layer |
| TC-RUN-13 | Run list rows | Silent, failed and normal are visibly different; cost and token counts shown |
| TC-RUN-14 | Exhaust the workspace's provider quota | `FriendlyRunError` gives a human sentence naming the cause, identical wording from a scheduled run and from KB assist |

## Part 8 — Backend parity (E, the 2 duplicated builds)

| ID | Charter | Expected |
|---|---|---|
| TC-PAR-1 | S1 and S4 built on the **API engine** and on a **local CLI coder** | Both produce a working agent of the same tier |
| TC-PAR-2 | Connector access on both | API engine gets native function tools; CLI reaches the same `Execute` via `rookery connector exec`. **Same results** |
| TC-PAR-3 | The approval gate on both | Parked identically — changing coder kind must not disable the setting |
| TC-PAR-4 | Chat tool sets on both | File-only both sides; no `bash`, no `run_script`, no `kb convert` reachable from chat |
| TC-PAR-5 | A CLI coder with no model configured (OpenCode) | The failure names the **missing model**, not "broken auth" — a bare 401 here is the documented trap |

---

## Instrumentation

Before starting, confirm these produce output — a charter judged from a log line that
does not exist is worse than one not run:

```bash
grep "agentdesigner"              logs/server.log
grep "build_id="                  logs/server.log
grep "agentrunner: run finished"  logs/server.log   # exit, chat_lines, silent, produced_nothing, stop_reason, total_tokens
grep "script_ran"                 logs/server.log   # discriminates "ran but produced nothing" from "never ran"
grep "chat_suppressed=true"       logs/server.log
```

Per E scenario, capture: the full transcript, `build_id` log slice, the generated
`AGENT.md` + tools, `state.md` after save, the binding and skill rows, the schedule
row, a vault diff across the build, and the first real run's transcript.

## Judging the designer, not just the code

Three questions are worth answering in prose at the end, because they are what a
launch actually rests on and no charter captures them:

1. **Could a non-technical owner have built each of these twelve agents?** Count the
   turns and note every point where you had to phrase something technically to get a
   good result.
2. **When a build went wrong, did the platform say something true about why?** A
   truthful failure is more valuable than a lucky success.
3. **How much did the twelve builds cost, in tokens and in minutes?** That number
   decides whether the designer is something people use casually or ration.

## Out of scope

Model quality benchmarking across providers (a separate exercise), adversarial prompt
injection against the designer, and the deferred surfaces recorded as known gaps —
skill editing over chat, skill import, MCP stdio and OAuth.
