# Post-v0.3.11 bug-fix wave — design

**Date:** 2026-08-20
**Status:** approved (standing authorization)

Seven items found while verifying the v0.3.11 agent-state work on a live install.
Two are user-visible defects, two are fidelity gaps in the build dry run, and three
are recorded limitations that need a semantic decision rather than a patch.

Two items in the original list were **wrong as stated** and are corrected here by
investigation; the corrections are kept in the record because each one changes what
the fix should be.

---

## Guiding constraint

Every change below must be **behaviour-preserving for installs that have not opted
in**. Three of these fixes touch code that decides *when an agent runs* or *what a
run reports*, and the failure mode of getting them wrong is silent: an agent fires
at the wrong hour, or a warning stops being counted. Where a fix could move
existing behaviour, the default is pinned to today's behaviour and a test asserts
it.

---

## Group 1 — provider classification and run observability

### 1.1 OpenRouter 404 kills the no-tool fallback

`internal/llm/openai.go` classifies "this model does not support tools" only when
the provider answers **400**:

```go
if code == 400 && len(req.Tools) > 0 {
    lower := strings.ToLower(string(respBody))
    if strings.Contains(lower, "tool") || strings.Contains(lower, "function") {
        return nil, ErrToolsUnsupported
    }
}
```

OpenRouter answers **404**:

```
{"error":{"message":"No endpoints found that support tool use.
  Try disabling \"read_file\".","code":404}}
```

So `ErrToolsUnsupported` is never returned, and the degradation in
`internal/coder/api_engine.go` — *"A model that rejects the tools field degrades to
a single no-tool reasoning turn rather than failing the run"* — never fires. The
run dies with `exit=-1`.

Observed live: `meta-llama/llama-3.1-8b-instruct` via OpenRouter, run
`e9ecf3db-c935-430b-8b1f-9409bad03cb5`.

**Decision.** Widen the status set to `400, 404, 422` and KEEP the body guard.

The body guard is what makes widening safe, and it is the reason this is not simply
"treat 404 as unsupported". A 404 is also what a genuinely wrong model slug returns,
and turning that into a silent no-tool retry would hide a configuration error behind
a degraded answer. Requiring the body to mention `tool` or `function` separates
"this endpoint cannot do tools" from "this endpoint does not exist". `422` is
included because it is the other status OpenAI-compatible gateways use for a
rejected request shape; it costs nothing and is guarded identically.

**Not doing:** a provider-specific branch for OpenRouter. The condition is a
property of OpenAI-compatible gateways generally, and `internal/llm` deliberately
knows nothing about which vendor sits behind a base URL.

### 1.2 Two delivery-phase warnings are never counted

`agentrunner: run finished` logs `len(rctx.warnings)`. Two of the nine append sites
run *after* that log:

- `"no [CHAT] marker emitted; delivered prose as fallback"`
- `"no deliverable prose (markers only, or tool-call scaffolding) — nothing sent"`

The other seven live in `runCoderTurns`, which is *called* before the log, and are
counted correctly — a `[CALL:]` warning was observed logging `warnings=1` live.

The two that are missed are precisely the ones that explain a **silent** run, which
is the case the log line was added for. Observed live: three runs logged
`warnings=0` while carrying that exact text in `agent_runs.stderr`, and diagnosing
them required opening the database — the outcome the log exists to prevent.

**Decision.** Emit the line once, after the delivery phase, on both the success and
the `runErr != nil` paths.

The log must keep reporting for a failed run; the naive fix of moving the statement
down the function would drop it for the error branch, which returns earlier. The
count is therefore captured at a single exit point that both branches pass through.

**Not doing:** a second log line. Two lines per run reporting overlapping fields is
how the counts drift apart again.

---

## Group 2 — dry-run fidelity

### 2.1 `UsedConnectionIDs` is discarded (confirmed)

Auto-bind (`agentdesigner.AutoBindTargets`) binds exactly the connections a build's
tool calls actually used, carried on `coder.Result.UsedConnectionIDs`. The dry run
runs the finished agent for real and consumes its result as
`dryRunOutput(res.Text)` only — so a connection the rehearsal exercised, and which
the build itself did not, is not bound.

**Decision.** `dryRun` returns the used connection ids alongside the sample, and the
caller merges them into the same set the build's ids feed.

Merge, never replace: the build's evidence is not less valid because a rehearsal
also ran, and an explicit `# Connections:` header still wins over both.

### 2.2 The dry run omits SKILLS (corrected — connections were already wired)

The original item claimed the dry run omits skills *and* connections. Connections
and MCP servers **are** wired (`dryrun.go`, `WithConnectors` / `WithMCP`). Only
skills are missing: `dryRunPrompt` composes AGENT.md, backend type, runtime context
and chat apps, with no `<skill_instructions>` block.

This matters because the dry run's entire purpose is to show what a real run
produces. An agent that declares a skill rehearses without it, so the sample shown
at review is produced under different conditions than the first real run.

**Decision.** Inject the declared skills into `dryRunPrompt`, resolved the same way
`agentrunner` resolves them for a real run.

**Explicitly NOT doing:** passing `VaultRoot`. `dryrun.go` records at length that
this omission is the only thing keeping a rehearsal of an unapproved agent out of
the user's live knowledge base. Skills change what the agent *knows*; `VaultRoot`
changes what it can *write*. Adding skills does not weaken that boundary, and this
spec does not touch it.

---

## Group 3 — state hardening

### 3.1 Unbounded read at the choke point

`agentstate.read` calls a bare `os.ReadFile` on the runner's per-turn hot path,
over a file an agent may grow without limit through `## Notes`. `MaxStateSize`
(64 KiB) bounds the state **body being written**, never the **file being read**.

**Decision.** Cap the read at **1 MiB** and **reject** rather than truncate,
naming the file and the remedy.

Three parts, each deliberate:

- **Reject, not truncate**, matching `internal/iolimit`: a truncated state.md would
  be parsed, would lose its closing fence, and would land in the recovery path as a
  *damaged* file — converting a size problem into a data-loss problem.
- **1 MiB, not `MaxStateSize`.** The read cap governs a different thing from the
  write cap: the document legitimately contains prose the write cap never counts.
  16× headroom keeps every plausible real file working while still bounding memory.
- **An error, not a warning.** `ReadState` already reports an unparseable file as an
  error, and the runner's `stateReadOK` guard then declines to overwrite it. A file
  too large to read takes the same protective path for free: refuse to write rather
  than replace hand-recoverable content.

### 3.2 The running-agent guard is PUT-only (scope corrected)

The original item also called the guard a check-then-write **race**. Investigation
shows the practical harm is much smaller than stated: `applyAndSaveState` reads the
file **from disk** at end-of-turn via `GetDetail` and merges the run's patch into
what it finds. A hand edit made during a run therefore survives unless the run
writes the same keys. The guard's message — *"its state.md will be overwritten when
the run finishes"* — overstates the risk.

The genuine gap is coverage: only `PUT /api/v1/kb/note` is guarded. A **delete** or
**rename** of a running agent's `state.md` is unguarded.

**Decision.** Extend the existing guard to the delete and rename paths, and correct
the message to describe what actually happens.

**Not doing:** a mutex. A lock held for the duration of a write cannot prevent an
overwrite that happens minutes later at run end, so it would add contention and
fix nothing. The merge-from-disk behaviour is the real protection and it already
exists.

---

## Group 4 — cron timezone

`scheduler.tick` takes a bare `time.Now()` and hands it to `schedule.Next`, so every
cron expression is evaluated in whatever zone the host runs in. `agent_schedules`
has no timezone column. The prompt already instructs the model to write schedules in
the USER's local time, so the two agree only while the host's zone matches the
owner's.

**Decision.** Add a nullable `timezone` column (migration 014). When set, evaluate
in that location. When empty, fall back to **`time.Local`**.

The fallback is the entire safety of this change. `profile.LoadLocation` returns
**UTC** when a workspace has no timezone set, and reusing it here would silently
re-time every agent on every install that never filled in a profile — a two-hour
shift, arriving with no error and no log line, on schedules that had been correct
for months. `time.Local` reproduces today's behaviour exactly, so an install that
does not opt in sees no change whatsoever.

Population: the column is written from the workspace profile's timezone when a
schedule is created or updated. Existing rows stay NULL until they are next written,
and keep host-local evaluation until then.

**Not doing:** a backfill. Backfilling would change firing times for existing
schedules — the exact outcome the fallback is designed to avoid. Opt-in on write is
slower but cannot surprise anyone.

**Known residual:** DST. `schedule.Next` in a named location handles transitions as
the cron library defines them; a schedule at an hour that does not exist on a spring
transition behaves per that library. Documented, not solved.

---

## Testing

Each fix gets a regression test that fails against the current code:

| Fix | Test |
|---|---|
| 1.1 | 404 + tool-mentioning body → `ErrToolsUnsupported`; 404 + unrelated body → plain error (the guard that keeps a bad slug visible) |
| 1.2 | A run whose only warning is delivery-phase logs a non-zero count, on both the success and error paths |
| 2.1 | Dry-run connection ids reach the auto-bind set and merge with the build's |
| 2.2 | `dryRunPrompt` carries a declared skill's instructions |
| 3.1 | An over-cap state.md is rejected, and the runner declines to overwrite it |
| 3.2 | Delete and rename of a running agent's state.md return 409 |
| 4 | Empty timezone evaluates identically to today (`time.Local`); a set timezone evaluates in that zone; a stored expression fires at the same wall-clock instant either way when the zones coincide |

The 4 test matters most: it is the one that pins "an install that has not opted in
sees no change", which is the property that makes this change safe to ship.

---

## Sequencing

Four PRs, smallest blast radius first:

1. Group 1 — two contained fixes, immediate user value
2. Group 2 — one subsystem
3. Group 3 — one subsystem
4. Group 4 — schema migration, shipped alone so a regression is unambiguous
