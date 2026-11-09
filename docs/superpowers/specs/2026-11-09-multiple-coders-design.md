# Multiple coders per workspace — design

Status: draft for review. Ships **second**, after agent budgets
(`2026-11-09-agent-budgets-design.md`), which resolves "which coder does this
agent run on" through a single function that this project replaces. **Plan this
project only after budgets has merged**, against its real schema, its
limit-cancel cause and that resolver function — not against the budgets spec.

## Why

A workspace already holds keys for several providers, but can only use **one**
coder. The owner wants to match the coder to the job: e.g. a CLI coder (Claude
Code) for a hard agent that writes scripts and processes data, and a cheap API
model (e.g. Mistral) for simple agents. Which coder goes where is always the
owner's decision.

## Goals

- A workspace has **many named coders**. Each is configured exactly as the single
  coder is today: API provider + model (+ optional base URL, key secret), or a CLI
  binary + model, + timeout.
- Exactly **one Default coder** at all times. The owner can make any coder the
  Default. With one coder, it is the Default.
- The Default runs **everything that is not an agent run**: one-off chat and
  saved chats (web and chat apps), chat auto-naming, reminder parsing, KB assist,
  and the like.
- Each agent can be **assigned** a coder. An **unassigned agent runs on the
  Default** — by design, no alert.
- The **agent designer asks** which coder the agent should use and assigns it.
- **Fallback:** if an agent's assigned coder can't be used — deleted, missing key,
  won't start, or fails during the run (out of credits, rate limited, provider
  down) — the run falls back to the Default and the owner gets an **inbox alert**
  saying that agent fell back and used the Default.
- Agent limits are **the agent's**, unchanged by switching coders (see budgets
  spec): active on an API coder, kept-but-inactive on a CLI coder.

## Non-goals

- Per-coder budgets or spend dashboards.
- Load balancing or automatic coder selection. The owner chooses.
- Changing what a coder *is* (no new engines, no new providers).

## Anti-goals

- A fallback that gets around a budget. A run ended by a per-run limit, or an
  agent paused by a period limit, is **never** handed to another coder.
- A fallback that silently re-does side effects (see "Fallback mid-run").
- Existing installs changing behaviour on upgrade.
- Deleting the Default, or reaching a state with zero coders.

## What exists today (verified 2026-11-09 against `main` @ 96e45dfc)

- Coder config is **inlined on `workspaces`** (`coder_kind`, `coder_bin`,
  `coder_timeout_s`, `coder_backend_type`, `coder_provider`, `coder_model`,
  `coder_api_key_secret`, `coder_base_url`). CLAUDE.md states "there is no
  `coders` table" — this project reverses that and must update the statement.
- `coder.ForWorkspace(w *db.Workspace, …)` (`internal/coder/forworkspace.go`)
  builds a coder from those fields. Callers reach it through per-workspace
  factories: `coderFor(workspaceID)` in `cmd/rookery/main.go` (passed to both
  designers, chat-app turns, reminder parsing), `Runner.WithCoderFactory` /
  `coderForWorkspace` in `internal/agentrunner/runner.go`, and the web server's
  `coderForWorkspace` (8 call sites). About 12 non-test Go files read the coder
  fields, including `web/coder_ready.go` (`workspaceCoderReady`, which drives the
  onboarding Done screen) and `web/api_settings.go`.
- API keys are stored as encrypted secrets (`CODER_KEY_<PROVIDER>`), and the
  settings page already has per-provider key cards
  (`web/ui/src/pages/settings/ProviderCards.tsx`) — several coders on the same
  provider share one key naturally.
- Settings UI: `web/ui/src/pages/settings/CoderSection.tsx`; the setup wizard
  reuses it.
- The designer's prompts and build gates depend on the coder's backend:
  `BackendType` selects capability blocks, and on the API engine builds are
  capped at TIER 1 (`apiTierCeilingViolation`). A runtime TIER 2 agent built by a
  CLI coder keeps working on an API coder.

## The design (decided with the owner)

### Coders

- New per-workspace coder records: name + the same fields as today.
- **Migration:** each workspace's existing inlined config becomes its first coder,
  marked Default. No agent is assigned, so every agent keeps running on exactly
  what it ran on before. *Builder:* decide whether the inlined columns are dropped
  or kept as a deprecated mirror; record which.
- **A workspace with no working coder** (the setup wizard's Skip, or a never
  configured install) still has a Default: a **"not configured"** coder record.
  Resolving the Default therefore never fails structurally;
  `workspaceCoderReady` keeps its meaning by checking that Default's fields, and
  a run on it fails with the same configuration message as today (my call).
- **Changing the Default moves every unassigned agent** to the new Default.
  "Make default" therefore shows a confirmation **listing the unassigned agents
  that will move** (and notes any whose limits become active/inactive because
  they cross API↔CLI). Existing agents are **not** pinned at upgrade (my call:
  matches the owner's model that unassigned means "use the Default").
- The Default can't be deleted. Deleting a non-default coder unassigns the agents
  using it (they then run on the Default — and since that is now "unassigned",
  my call: send one inbox note at delete time listing the affected agents rather
  than a fallback alert on each later run).

### Assigning

- Agent page: a **Coder** card/selector showing the assigned coder, or "Default
  (<name>)" when unassigned.
- Designer: asks which coder the agent should use (only when the workspace has
  more than one), and records it with the agent on approve. *My call:* the
  **build runs on the chosen coder** — the tier ceiling and capability prompts
  depend on it, so building on the Default and running on another coder would
  produce an agent shaped for the wrong engine. The design *conversation* itself
  stays on the Default.
- **Edits** build on the agent's **assigned** coder (or the Default if
  unassigned), and the designer does not re-ask unless the owner wants to change
  it (my call). If the edit needs to add or change scripts and that coder is an
  API coder, the tier ceiling refuses it as today; the designer says so in plain
  terms and suggests assigning a CLI coder for the edit.
- Skill designer: stays on the Default (my call; skills aren't assigned coders).
- When an agent is moved from an API coder to a CLI coder, the agent page says its
  limits are now inactive (they are kept).

### Fallback

Triggers: assigned coder deleted/misconfigured/missing key/binary not found
(`ErrCoderUnreachable`, `ErrAPIAuth`), or failing during the run with a provider
error (`ErrUsageLimit`, `ErrRateLimited`, provider unreachable).

- The run retries on the Default and the owner gets an inbox alert naming the
  agent, the coder that failed, and why.
- **Never** for a budget stop or pause, an owner stop, or a timeout.
- If the Default is the assigned coder (or the agent is unassigned), there is
  nothing to fall back to: the run fails as today.
- **Fallback mid-run (agreed with the owner):** a run that fails after it has
  already executed tool calls may have had side effects (sent a message, written
  a note, published), and retrying from the top could do them twice. So fall back
  automatically **only when the assigned coder failed before executing any tool
  call**; otherwise fail the run and say in the alert that it was not retried,
  and why.
  - **API engine:** "before any tool call" is known exactly (tool-call count on
    the result/error path).
  - **CLI coder:** Rookery cannot see what a subprocess did, so it falls back
    **only when the process never started** (`ErrCoderUnreachable`: missing
    binary, launch failure). A CLI usage limit (`ErrUsageLimit`) does **not**
    fall back — accepted by the owner.
- Budgets: a fallback run is the same agent run for limit purposes. If the Default
  is a CLI coder, limits are inactive for that run (consistent with the budgets
  spec). The run record must say which coder actually ran it.
- Throttle the alert (my call): at most one fallback alert per agent per
  coder-failure reason per day, so a 10-second agent can't flood the inbox.

### Where it shows

- **Settings → Coders:** list of coders, Default badge, add / edit / delete /
  make default, per-coder Test. Replaces the single coder form. The setup wizard
  creates the first coder (Default).
- **Agent page:** Coder selector.
- **Run history:** which coder ran each run, and a "fell back" marker.
- **Inbox:** fallback alerts.

## Left to the builder

- Schema (`coders` table + `agents.coder_id` nullable is the obvious shape),
  migration numbering, API routes.
- Replacing the per-workspace coder factories with per-purpose resolution
  (`default coder for workspace` vs `coder for agent`) across the call sites
  listed above, including `workspaceCoderReady`.
- Whether `/healthz` or onboarding reports change.

## Documentation obligation

Reverses a documented CLAUDE.md fact ("no coders table", "Per-workspace coder"
section), adds routes and a migration → `docs-sync` across README, CLAUDE.md and
rookery-web.
