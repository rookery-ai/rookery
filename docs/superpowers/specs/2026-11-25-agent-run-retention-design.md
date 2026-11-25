# Agent run retention and per-run scratch — design

Status: approved 2026-11-25. Ships as a `fix(...)` change.

## Why

An agent's run history grows without bound in three places, and a run's scratch files in a
fourth:

- `agents/<id>/logs/run_<ts>.md` — one note per run, forever.
- `.kb/db-export/agent_runs/<run>.json` — the note's sidecar, forever.
- `agent_runs.transcript` / `stdout` / `stderr` — up to 64 KiB of transcript per row, forever.
- `<workspace home>/tmp` — the single `TMPDIR` every run of every agent in a workspace shares,
  never emptied.

The first two live in the knowledge base, so they ride every backup and every knowledge-base
search. A shell-scraping agent that saves fetched pages under its own `logs/` adds untrusted
third-party HTML to the search results chat and every other agent read. Nobody — including an
agent edit, which diagnoses from the most recent runs — needs more than the last 15 runs.

## Goals

1. **A hard cap of the last 15 runs per agent.** Not configurable, by anyone, anywhere.
2. **Every run gets its own scratch folder**, deleted when the run ends however it ends.
3. **A `run-hygiene` core skill** telling agents where scratch, memory and output belong.

## Non-goals and anti-goals

- No per-agent or per-workspace setting. A setting is a second number to keep correct.
- The platform never deletes folders a model invented (`logs/tmp`, `cache/`, …). Guessing at
  names a model chose is how something someone wanted gets deleted.
- Not a token-cost fix. A run's cost is its turn count; this changes what is kept afterwards.
- **Anti-goal: switching spending limits off.** Day and month limits sum `agent_runs` usage
  since the period start (`db.AgentSpendSince`). Deleting old rows would make an agent that runs
  every few minutes forget what it spent this month.

## Design

### Retention (runner, mandatory)

`agentrunner.RunHistoryKeep = 15`. After every run — success, silent, failure, stop, limit,
timeout, produced-nothing — a deferred call prunes that agent's history:

- **Database:** every finished row older than the agent's newest 15 (by `started_at`, `rowid`
  breaking ties) keeps its id, timestamps, status columns and usage, and loses `transcript`,
  `stdout` and `stderr`. That is where nearly all of a row's size is, and it keeps the spend
  limits exact. A row still in progress (`finished_at IS NULL`) is never touched.
- **Knowledge base:** `agents/<id>/logs/run_*.md` beyond the newest 15 by file name (the name is
  a UTC timestamp, so lexical order is chronological) are deleted, and so is every
  `agent_runs` sidecar of this agent whose run is not among the 15 kept.

The slim rows need no hiding: run history lists at most 10 rows and the Home feed takes a
workspace's 10 newest, and a pruned row by definition has 15 newer runs of its own agent.

A one-time pass at `serve` startup applies the cap to every agent, so an agent that rarely
runs is cleaned up too. It is idempotent and best-effort; a failure is logged, never fatal.

### Per-run scratch

The runner creates `<workspace home>/tmp/run-<run id>/` (0700) right after the run row and
hands it to the coder through `Coder.WithScratchDir`. Every subprocess the run starts gets
`TMPDIR`/`TMP`/`TEMP` pointing there: the CLI coder (`buildEnv`) and the API engine's `bash`
and `run_script`. A deferred `os.RemoveAll` deletes it on every exit path. It sits inside the
workspace home, which the sandbox already grants read-write, so confinement is unchanged.

A `[CALL:]`ed child agent is its own run and gets its own folder. A server killed mid-run leaves
its folder behind, so `serve` sweeps every `run-*` directory under each workspace's `tmp` at
startup, before the scheduler starts — no run can be in progress then. Callers that never set a
scratch dir (chat, builds, dry runs) keep today's `<home>/tmp`.

### Telling the agent

The `<skill_environment>` block every run already receives stops printing `<home>/tmp` as the
scratch path and says instead that `$TMPDIR` is private to this run and deleted afterwards, that
fetched pages and parse output never go into the knowledge base or `logs/`, and that `logs/`
keeps only the last 15 run notes. That reaches every agent, whatever skills it declares.

The `run-hygiene` core skill (SKILL.md only, like every core skill) carries the fuller guidance:
scratch in `$TMPDIR`, anything the next run needs in state, durable findings in the owner's
notes, never fetched pages in the knowledge base, `logs/` belongs to the platform.

## Approaches considered

- **A skill only.** Rejected: weak models do not reliably follow written rules, and a skill
  reaches only agents that declare it.
- **Delete old rows outright, with a separate spend counter.** Rejected: a second place spend is
  counted, and a new table, for no benefit the slim row does not already give.
- **Retention in days.** Rejected by the owner in favour of a run count.
- **Configurable cap (agent page / designer).** Considered and rejected by the owner: a hard 15.
- **Chosen:** platform-enforced cap of 15 with slim rows, per-run scratch, prompt line + skill.

## Left to the builder

File placement of the new helpers, test layout, and the skill's exact wording, within the rules
above.
