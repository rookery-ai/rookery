# Agent Designer — feature test suite (weak-model stress test)

Purpose: exercise **every** agent-designer feature and see whether a less-capable model
(mistral-medium) survives the new hardening, then re-run the identical suite on Claude to
compare. The point of comparison is the weak-model hardening: **tier-default-to-1 forcing**,
**convergence discipline** (≤3 questions, no looping/re-asking), the **weak-backend verification
gate**, **non-technical UX** (no jargon leaking), and the **`# Skills:` header + `[TEST_OUTPUT]`
contract**.

No single agent can cover all features — several are mutually exclusive
(notifies ↔ `[SILENT]`, scheduled ↔ manual, Tier 1 ↔ Tier 2/3, create ↔ edit). So this is a
**3-scenario suite**: A (rich create), B (minimal create), C (edit).

---

## Prerequisites before running

1. **Coder = mistral-medium.** In the workspace's coder settings, set `coder_kind = api`,
   an OpenAI-compatible provider pointing at Mistral, model `mistral-medium`, and put the
   Mistral API key in the secret store (the `coder_api_key_secret` name). `internal/llm/openai.go`
   already handles Mistral's tool-call/rate-limit quirks, so the path is wired.
2. **Connect Telegram** (Settings → Connectors) so "notify me" resolves to a real platform. If
   you leave it unconnected the designer should still work but will point you to Connectors.
3. **Add the secrets the scenarios need** *before* approving, so the build-time end-to-end test
   can make real calls:
   - `OPENWEATHER_API_KEY` — free key from openweathermap.org (Scenario A).
   - A Composio connected account if you want the Composio branch of A to fully exercise
     (otherwise the designer should still route it correctly and the build test will note the
     missing connection rather than hardcode a key).
4. Run each scenario in a **fresh** design session (`/dashboard/agents/new`). Don't reuse one
   conversation across scenarios.

**How to use the answer key:** the designer's question order is non-deterministic and will differ
between mistral and Claude. Do **not** read a fixed script. Paste the opening message verbatim,
then answer whatever it actually asks using the answer key. Score against the rubric afterward.

---

## Scenario A — Rich create (the workhorse)

Covers: scheduled + notifies + skill (`# Skills:` header) + named secret + external service +
Composio routing + KB write + **Tier-2 with an embedded Tier-1 trap** + change-request-in-verify
+ forgiving approval + build-time E2E test.

### Opening message (paste verbatim)

> I want a helper that every weekday morning checks the weather for Skopje and also skims the
> latest headlines about my city, then figures out if there's anything I actually need to know
> about today — like a storm coming or big local news. If there is, message me a short friendly
> heads-up. Either way I want it to keep a running diary of each day's weather in my notes so I
> can look back later. Oh, and if it spots an important event it should add it to my Google
> Calendar.

### Answer key (respond only to what it asks)

- **Schedule / how often?** → "Every weekday morning around 7am."
- **Notifications?** → "Yes, message me — but only when there's something worth knowing. Skip the
  message on a boring normal day."
- **Which city / accounts?** → "Skopje, North Macedonia. Google Calendar is
  ilija.dimitrovski@kroute.ai."
- **Weather data / API key?** → "I can get a free OpenWeather key — tell me where to put it."
  (You have already stored `OPENWEATHER_API_KEY`.)
- **How to decide what's 'important'?** → "Use your judgment each day — I can't give you a fixed
  rule." (This deliberately tests the design-for-flexibility behavior; it must NOT force you to
  invent a rigid keyword list.)
- Anything else it asks → answer briefly and plausibly; don't volunteer extra tasks.

### At the Verify step (after it presents the plan and you'd normally approve)

**First** send a change request (do NOT approve yet):

> Actually make the diary entry include the day's high and low, not just a description.

Then, once it re-presents, approve with a **casual, non-exact** confirmation to test the forgiving
trigger:

> yeah that looks great, go for it

### What a correct run looks like (rubric — tick each)

| # | Feature under test | Pass criteria |
|---|---|---|
| A1 | Convergence discipline | Asks **≤3 questions total**, never re-asks something you answered, moves forward each turn, then presents a plan. A weak model that loops or re-summarizes every turn **fails** here. |
| A2 | Non-technical UX / jargon block | The user-facing messages never say `AGENT.md`, `Python`, `script`, `cron`, `vault`, `JSON`, `webhook`, `API key` (unqualified), etc. "Run schedule" not "cron"; "your notes" not "vault". |
| A3 | Notification decision | Correctly captures **conditional** notification (message only when notable; silent otherwise) — not "message every run". |
| A4 | Schedule capture + auto-schedule | Plan says a weekday-morning schedule; generated `AGENT.md` first line is `# Suggested schedule:` with a valid 5-part cron (~`0 7 * * 1-5`) and a schedule row is created automatically. |
| A5 | Skill selection + `# Skills:` header | Generated `AGENT.md` contains a `# Skills:` line naming the actually-used skills (expect `web-search` and `google-workspace`; **not** the whole catalog). Names match installed skills exactly. |
| A6 | Secret guidance | Tells you to add `OPENWEATHER_API_KEY` to the secret store with clear where-to-get-it steps; **never** asks you to paste the value in chat. |
| A7 | KB write, not external | Routes the diary to **your notes** (built-in KB, e.g. `notes/weather-diary.md`), NOT Notion/Google Docs. |
| A8 | Composio / external routing | Calendar goes through Composio v3 REST (`backend.composio.dev/api/v3`, `x-api-key`) — not the deprecated SDK, no hardcoded `ak_...` key, no `/v1/`,`/v2/`. |
| A9 | **Tier correctness (the trap)** | Fetching weather + headlines = a real fetch → Tier 2 with a **focused** helper script. But the "decide what's important" and "write the diary sentence" parts must stay **reasoning (Tier 1)** — a weak model that writes a script to *judge importance* or to *write a single note* **fails**. The architecture-gate analysis should be visible in the build output. |
| A10 | Build-time E2E test | Build emits `[TEST_OUTPUT]…[/TEST_OUTPUT]` from a **real** run against live OpenWeather (secret injected), not a mock. |
| A11 | Test-artifact cleanup | After approval, the agent dir has only shipping source — no `_probe.py`, downloaded files, or scratch run-output left behind. |
| A12 | Guardrails don't false-trip | Legit `requests`/HTTP code is NOT blocked. (If it is, that's an over-broad AST rule, note it.) |
| A13 | Change-request keeps build | Your "include high and low" reply returns to design **without discarding** the generated agent, then re-generates with that change. |
| A14 | Forgiving approval | "yeah that looks great, go for it" is treated as approval (saves), not as another change request. |

---

## Scenario B — Minimal create (the negative paths)

Covers what A structurally can't: **Tier 1 only (zero code files)**, **`[SILENT]` / no notification**,
**manual / no schedule**. This is the purest tier-1-forcing test — a weak model's instinct is to
over-build.

### Opening message (paste verbatim)

> When I ask it to, I want something that takes whatever I've jotted in my daily notes and
> writes me a tidy one-paragraph summary back into my notes under a "Summaries" section. I don't
> need a message about it — just quietly update my notes. Only run it when I trigger it myself.

### Answer key

- **Schedule?** → "No schedule — only when I trigger it."
- **Notify you?** → "No, stay silent. Just update my notes."
- **Which notes?** → "My daily notes; put the summary under Summaries."
- Anything else → "Use your judgment."

Approve normally when the plan looks right (`approve`).

### Rubric

| # | Feature under test | Pass criteria |
|---|---|---|
| B1 | **Tier 1 forced** | **Zero** code files created. The build states "No helper code needed — reasoning only." Summarizing a note is pure reasoning — a helper script here is an automatic **fail**. |
| B2 | `[SILENT]` path | `AGENT.md` says it does not notify the user; on a run it emits `[SILENT]` and no `[CHAT]`. Rubric proof: a test run produces no user message and no "ran but produced no notification" warning. |
| B3 | No schedule | `AGENT.md` first line is `# Suggested schedule: none`; no schedule row created. |
| B4 | `# Skills: none` | If it needs no skill, the header is present as `# Skills: none` (the line is never omitted). |
| B5 | KB write scope | Writes to the user's notes under a Summaries area; reads the daily notes. Stays in the vault. |
| B6 | Convergence | ≤3 questions, no looping, quick to plan (this is a simple agent — it should not interrogate you). |

---

## Scenario C — Edit (diagnose-before-fix)

Covers the whole `IsEdit` FSM branch: plain-English **diagnosis** → **confirm** → **approve** →
targeted fix + re-test-proves-bug-gone, staging-dir edit (live agent untouched until approve),
schedule-row reconcile (no duplicate).

Run this **on the agent you built in Scenario A.** Open it and choose Edit.

### Opening message (paste verbatim)

> Something's off — this morning it messaged me even though nothing important was happening, it
> was just a normal sunny day. It should only ping me when there's actually something I need to
> know. Can you fix that?

### Answer key

- If it asks what "important" means → "You decide each day; I just don't want a message on an
  ordinary day."
- If it asks to confirm the fix → "Yes, that's exactly it."
- Approve the fix with `approve` when asked.

### Rubric

| # | Feature under test | Pass criteria |
|---|---|---|
| C1 | Diagnosis first | It reads the existing instructions/scripts and states the **actual cause** in plain English ("it's sending a message every run instead of only when notable") **before** proposing anything. It does NOT ask you to paste the scripts — it already has them. |
| C2 | No jargon in diagnosis | Explanation avoids file names / code terms. |
| C3 | Confirm-then-approve | Describes the surgical change in plain English, asks you to confirm, waits for `approve`. |
| C4 | Surgical, not rewrite | Proposes changing only the notify condition — not "rewrite the agent". |
| C5 | Re-test proves fix | Build re-tests and demonstrates the old bug (message on a boring day) no longer occurs; emits `[TEST_OUTPUT]`. |
| C6 | Staging isolation | The live agent dir is untouched until you approve (edits happen in a `-edit-staging` sibling). |
| C7 | Schedule reconcile | After save, there is still exactly **one** schedule row for the agent (no duplicate / double-fire). |

---

## Scoring the mistral-vs-Claude comparison

Fill this in for each model. The rows that matter most for "can a weak model do it" are the
**bolded** ones — they're the hardening the new changes added.

| Feature | mistral-medium | claude | Notes |
|---|---|---|---|
| **A1 convergence (≤3 Q, no loop)** | ☐ | ☐ | |
| **A2 no jargon** | ☐ | ☐ | |
| A3 conditional notify | ☐ | ☐ | |
| A4 auto-schedule | ☐ | ☐ | |
| A5 `# Skills:` correct subset | ☐ | ☐ | |
| A6 secret guidance | ☐ | ☐ | |
| A7 KB not external | ☐ | ☐ | |
| A8 Composio v3 correct | ☐ | ☐ | |
| **A9 tier correct (trap)** | ☐ | ☐ | |
| A10 real E2E `[TEST_OUTPUT]` | ☐ | ☐ | |
| A11 artifact cleanup | ☐ | ☐ | |
| A12 guardrails no false-trip | ☐ | ☐ | |
| A13 change keeps build | ☐ | ☐ | |
| A14 forgiving approval | ☐ | ☐ | |
| **B1 Tier-1 forced (zero code)** | ☐ | ☐ | |
| B2 `[SILENT]` | ☐ | ☐ | |
| B3 no schedule | ☐ | ☐ | |
| B4 `# Skills: none` | ☐ | ☐ | |
| **C1 diagnosis first** | ☐ | ☐ | |
| C3 confirm-then-approve | ☐ | ☐ | |
| C5 re-test proves fix | ☐ | ☐ | |
| C6 staging isolation | ☐ | ☐ | |
| C7 schedule reconcile | ☐ | ☐ | |

**Interpretation.** Expect mistral to be weakest on the bolded rows: it will want to over-build
(A9/B1), and it may loop or leak jargon (A1/A2). If the hardening works, those hold anyway.
Claude should pass essentially all rows; where it doesn't, the feature itself (not the model) is
the suspect.
