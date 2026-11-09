# Agent budgets and limits — design

Status: draft for review. Ships **first**; multiple coders per workspace
(`2026-11-09-multiple-coders-design.md`) follows it.

## Why

An agent can burn money in two ways that nothing currently stops:

1. **One run goes wrong.** A model gets stuck and keeps calling tools until the
   API engine's `maxHardTurns` (150) ceiling. That ceiling is global and tuned for
   the worst case; no single agent can ask for less.
2. **Many runs add up.** An agent scheduled every 10 seconds or 10 minutes can be
   cheap per run and expensive per day.

No prompt or guard can cover every way a model gets stuck, so the owner needs a
hard, per-agent ceiling on spend.

## Goals

- Per agent, any combination of three optional limits: **per run**, **per day**,
  **per month**.
- A per-run limit **ends that run**, flags it in the run history, and **notifies**
  the owner (inbox + chat).
- A daily or monthly limit **pauses the agent** until the period resets, notifies
  once, and resumes on its own.
- While paused, **scheduled runs and the manual Run button are both blocked**.
- Limits are set **in the designer conversation** (the designer asks and suggests)
  and **on the agent page** at any time after.
- The state is visible: a flag on the limited run, a paused banner on the agent
  page, and flags on Home's **Agents at a glance** and **Needs attention** panels.

## Non-goals

- **CLI coders.** Limits are enforced only when the agent runs on an **API
  coder**. CLI coders have different billing (subscriptions, proxies, list-price
  estimates) and showing or enforcing cost for them is explicitly out of scope.
  (Note for the record: `claude -p --output-format json` does emit
  `total_cost_usd` and `usage`, and Rookery discards them. Do not build on it in
  this project.)
- **The designer.** Limits never affect the design conversation, the agent build,
  or the create-build dry run (`internal/agentdesigner/dryrun.go`).
- Workspace-wide or per-coder budgets. Limits belong to the agent.

## Anti-goals (would make this a failure even if it "works")

- Pausing by setting `agents.active = 0`. That is the owner's own on/off switch;
  auto-resume on the 1st would switch back on an agent the owner turned off.
- Pausing by editing the schedule row. Since #417 a schedule change also rewrites
  AGENT.md's header, quoted crons and the derived description — far too much
  side effect for a temporary pause.
- A limit-stopped run that looks like a failure, or like an owner stop that sends
  nothing. The owner must be told.
- Limits silently lost when the agent's coder changes (see "Coder kind" below).
- Treating "provider did not report cost" as `$0.00` and so never tripping a dollar
  limit.
- A price table or any token→dollar estimate.

## What already exists (verified 2026-11-09 against `main` @ 96e45dfc)

- **Stopping a run** (#410): `internal/agentrunner/stop.go` registers a
  `context.WithCancelCause` per run in the runner — the one layer every trigger
  (cron, manual, chat, `[CALL]`) passes through. A stop is recognised from the
  cancel's **cause** (`ErrRunStopped`), not from the coder's error, and recorded
  in `agent_runs.stopped` (migration 023). An owner-stopped run sends nothing
  further.
- **Per-run usage** is accumulated in the runner (`rctx.usage`, `addUsage`) and
  stored on `agent_runs` — tokens since 003, `cached_tokens`/`cost_usd` with
  `_reported` flags since 017. Cost is what the provider reports (OpenRouter
  reports it on every response; many providers report tokens only).
- **The API tool loop** (`internal/coder/api_engine.go` `runToolLoop`, budgets in
  `turnbudget.go`) sees each turn's `Usage` as it arrives.
- `agent_runs.silent` / `stopped` are the precedent for "a column, not an
  inference" run flags rendered as chips in the run list.
- Home's `AgentsAtAGlanceCard` (`web/ui/src/pages/home/cards.tsx`) and the
  "Needs attention" section (`web/ui/src/pages/home/HomePage.tsx`).

## The design (decided with the owner)

### Limits

Stored per agent. Each of per-run / per-day / per-month is optional.

- **Units (decided with the owner): tokens are the base; dollars are a bonus.**
  Every limit can be set in **total tokens**, on every API provider. **Dollar**
  limits are offered *additionally* only where the provider reports cost in its
  responses. Rookery does **not** keep a price table and does not estimate dollars
  from tokens — pricing is the owner's business.
- Which providers report cost (checked 2026-11-09): **OpenRouter** (`usage.cost`,
  already parsed in `internal/llm/openai.go`) and **xAI** (`usage.cost_in_usd_ticks`,
  1 USD = 10^10 ticks — **not yet parsed; add it**). Perplexity has a nested
  `usage.cost.total_cost` but it has been reported to vanish from responses
  without notice — do **not** offer dollar limits there. OpenAI, Anthropic,
  Mistral, Gemini, DeepSeek, local servers etc. report tokens only. "Reports
  cost" is a static per-provider capability flag in the catalog
  (`coder.APIProviders()`), not inferred from the last run.
- If a run on a dollar-limited agent nonetheless comes back with
  `cost_reported = false`, the dollar limit cannot be evaluated: the token limit
  (if any) still applies; record it on the run and surface it once. Never treat
  unreported cost as zero.
- A **per-run turn cap** is also offered (must be below 150). It is an extra
  ceiling *on top of* the existing turn budgets in `turnbudget.go` (it replaces
  neither the base budget nor `maxHardTurns`). Hitting it ends the run exactly
  like any other limit — cancel with the limit cause, **Limited** chip,
  notification — **not** the grace-turn/exhaustion-summary path (my call: the
  owner asked that a limit ends the run and tells them).
- Day and month boundaries are in the **workspace's profile timezone**
  (`profile.ScheduleZone`/`LoadLocation` semantics; empty → host local, matching
  how schedules treat an empty zone).
- Period spend = sum over this agent's own `agent_runs` in the period. **A child
  run an agent `[CALL]`s counts against the child's limits, not the parent's**
  (my call: child runs have no parent link in `agent_runs`, and adding one buys
  little). A parent's limits do not reach into a child.

### Per-run limit → end the run

- Checked **mid-run** on the API engine after each turn's usage arrives. Reuse
  #410's cancel with a new cause (e.g. `ErrRunLimited`), not a new mechanism.
- The effective per-run ceiling is `min(per-run limit, remaining day budget,
  remaining month budget)`, so a run cannot overshoot the period cap either.
- Recorded as its own column on `agent_runs` (e.g. `limited` + which limit),
  rendered as a **"Limited"** chip in run history, distinct from Stopped / Failed /
  Silent. The transcript gets a closing line naming the limit.
- **Notifies** the owner via inbox (+ chat), unlike an owner stop. Throttled (my
  call): at most **one per-run-limit notification per agent per day**, so a
  10-second agent hitting its limit every run cannot flood the inbox; every
  limited run still gets its chip. "Needs attention" follows the same rule.
- Not counted as a failure on Home's recent activity (as Stopped isn't).

### Daily / monthly limit → pause the agent

- Its own state (e.g. `agents.budget_paused_until` + reason), **not** `active`
  and **not** the schedule. Checked **in the runner**, so it blocks **every
  trigger**: schedule, the manual Run button, chat-app `/run`, and `[CALL]` from
  another agent (my call — the owner said scheduled and manual runs; anything
  less leaves a bypass). The scheduler also skips paused agents so they don't
  record a refused run every slot; skipped, not queued.
- Resumes automatically when the period resets (the stored timestamp passes).
  No catch-up burst for slots skipped while paused — skipped, not deferred.
- **One** notification when the pause starts, not one per skipped slot.
- Manual Run is **blocked** while paused; the button explains why and when it
  resumes. Raising or removing the limit on the agent page lifts the pause
  immediately (my call — otherwise the owner has no way out short of waiting).
- **Overlap:** the runner's registry allows the same agent to run concurrently
  (cron every 10s with slow runs). A check made only at run start can overshoot.
  The builder must make the period check account for in-flight runs (e.g. the
  mid-run ceiling above reading live in-flight spend, or refusing to start a
  second run of a budgeted agent). Pick one and state it.

### Coder kind

- Enforced only while the agent runs on an **API** coder.
- On a CLI coder the limits are **kept, shown greyed out, not enforced** — never
  deleted. Switching back to an API coder makes them active again unchanged.
- Today the coder is per workspace. Resolve "which coder does this agent run on"
  through **one function** so the multi-coder project only replaces that function.

### Setting limits

- **Designer:** asks whether the owner wants limits for this agent, suggests
  sensible values for what the agent does and how often it runs, and puts the
  agreed limits in the plan the owner approves (a `Limits:` line in the
  `[TECHNICAL SPEC]`, like `Schedule:`). They are saved with the agent on approve.
  Only asked when the agent will run on an API coder. Because tokens are the base
  unit, the designer explains them in plain terms (what a token roughly is, what
  this agent's runs are likely to use) and offers dollars only when the coder's
  provider reports cost. Add "token" to what the design prompt may say; keep the
  rest of the jargon rules.
  *Builder:* decide whether limits are persisted from the spec, from an AGENT.md
  header (`# Limits:` like `# Suggested schedule:`), or both — note that a header
  is the pattern edits already reconcile (`loadAgentForEdit`).
- **Agent page:** a **Limits** card (alongside Schedule) to set, change or clear
  any limit at any time, showing spend so far this day / month.

### Where it shows

- Run history: **Limited** chip.
- Agent page: banner "Paused — monthly budget reached. Resumes 1 Dec." with
  current spend vs limit.
- Home → **Agents at a glance**: a flag on the agent.
- Home → **Needs attention**: an entry for a paused agent (and, my call, for a
  recently limited run).
- Inbox + chat: the notifications above.

## Left to the builder

- Exact schema (columns on `agents` vs a `agent_limits` table), API routes, and
  migration number (next is 024).
- How the mid-run check is plumbed into `runToolLoop` (a callback/option on the
  coder, mirroring `WithProgress`).
- Notification wording and the inbox `source` value.
- Whether chat-app commands (`/agent …`) expose limits — not required.

## Documentation obligation

New `/api/v1` routes and a migration → `docs-sync` applies (README, CLAUDE.md,
rookery-web). Add a CLAUDE.md section recording the anti-goals above (why not
`active`, why not the schedule, why not CLI).
