# Agent run reliability: turn budget, honest exhaustion, delivery contract

**Status:** design, awaiting implementation
**Scope:** `internal/coder/api_engine.go`, `internal/agentrunner/runner.go`

## The incident

A scheduled agent (`test-new-1`, DeepSeek v4 Flash via OpenRouter) delivered this
to its owner's Telegram:

```
🤖 test-new-1

<｜DSML｜tool_calls>
<｜DSML｜invoke name="adguard_query_log">
<｜DSML｜parameter name="limit" string="false">10</｜DSML｜parameter>
</｜DSML｜invoke>
</｜DSML｜tool_calls>
```

That is DeepSeek's own tool-call markup, leaked as prose. The run recorded
`exit 0` and did no work: no query, no state written, no notification worth the
name.

The run record names the delivery path exactly:

```
stdout: <｜DSML｜tool_calls> … </｜DSML｜tool_calls>
stderr: no [CHAT] marker emitted; delivered prose as fallback
```

## Root cause

Four steps, three of them ours.

1. **The agent is expensive.** It enumerates blocked domains, looks each new one
   up on the web, writes a knowledge-base note and appends to a spreadsheet. On
   the observed run that is 44 domains — far more than the turn budget allows.

2. **It exhausts `maxAPITurns` (25).** Measured against agents that work fine on
   the same model:

   | agent | prompt tokens | outcome |
   |---|---|---|
   | `check-wheader` | 26k – 61k | fine |
   | `amazon watcher` | 71k – 387k | fine |
   | `test-new-1` | **916k – 926k** | broke |

   Completion tokens tell the same story: ~10–13k for `test-new-1` against ~900
   for `check-wheader` — roughly 25 assistant turns against 2–3. The model is not
   the differentiator; the turn count is.

3. **The grace turn removes the structured channel.**
   `graceTurnOnBudgetExhausted` sets `req.Tools = nil` — "the model literally
   cannot request another tool call" — and asks for a prose wrap-up. But the model
   still had work queued. Removing the tools field removes the *well-formed way to
   express the intent*, not the intent. So it expressed it as text, in its native
   markup.

4. **Nothing validated the result.** `len(resp.ToolCalls) == 0` is the engine's
   only test for "the model is done" (`api_engine.go:121`), so the markup became
   the final answer. `parseCoderOutput` then found no `[CHAT]` and no `[SILENT]`,
   and the prose fallback — which exists to rescue a forgotten marker — forwarded
   it verbatim.

**The 25-turn cap is ours**, not a provider limit. Providers cap single requests,
not agentic loops. `maxBuildAPITurns` was already raised to 40 because "25 was
routinely insufficient for a multi-action agent"; runs kept the tighter bound, and
`test-new-1` is exactly a multi-action agent at run time.

Step 3 is the one worth dwelling on: the malformed output is close to a
*predictable* consequence of our own bail-out, not random model weakness. Which is
why pattern-matching provider dialects is the wrong primary fix — it would be
matching a symptom we cause, at a point where we know precisely what we asked for.

## Design

Three changes. Each stands alone; together they mean a run either does its work,
or fails in a way that says so.

### 1. Turn budget: progress-based, not fixed

A fixed cap conflates two situations that look identical by turn count and are
completely different by behaviour: a runaway loop, and legitimately long work.
Budget on progress instead.

| Constant | Now | Proposed |
|---|---|---|
| `maxAPITurns` (runs, chat) | 25 | **30** base |
| `maxBuildAPITurns` | 40 | **50** base |
| hard ceiling (never extended) | — | **150** turns |
| unproductive streak | — | stop at **6** |

A turn is **productive** when it issued at least one tool call that executed
successfully and was not a repeat of a recently failed call. A productive turn does
not count against the base budget; an unproductive one does.

Consequences, stated plainly:

- An agent making 80 genuine tool calls finishes instead of dying at turn 25.
- A model spinning on one failing call stops at **6 consecutive unproductive
  turns** — sooner than today, which waits for the whole budget.
- The 150 ceiling is pure runaway protection and is never extended.

The oscillation guard already tracks repeats and consecutive failures
(`recentFails`, `consecutiveFails`), so "was this turn productive?" is mostly
answerable from existing state.

### 2. Exhaustion must not depend on the model

At exhaustion we already know every fact worth reporting: turns used, which tools
ran, which succeeded, what the last state was. We do not need — and after this
incident, cannot trust — the model to narrate its own failure.

- Compose the exhaustion message **deterministically** from run facts.
- The grace turn becomes **best-effort garnish**: if its reply satisfies the output
  contract (below), use it; otherwise discard it silently and send ours.

This removes the model from the failure path, which is what makes the fix hold
across every model and provider rather than every dialect we have seen.

Shape of the generated message:

> Ran out of steps after 30 tool calls. Completed: fetched the query log,
> identified 12 of 44 new domains. Did not finish: the spreadsheet update.

### 3. Output-contract check at delivery

The last line of defence, and deliberately **dialect-agnostic**: rather than asking
"does this look like a DeepSeek tool call?" — an unwinnable blacklist across
providers — assert what a valid agent message *is*.

A run's user-facing output must be one of:

- content parsed from a `[CHAT]` block, or
- prose that carries no protocol or tool-call scaffolding.

Anything else is **not delivered**. The run is recorded with a warning, and the
user receives the honest empty-run message that already exists rather than raw
markup.

The prose fallback keeps its purpose — rescuing a genuinely forgotten `[CHAT]` —
but gains a floor: it may forward *prose*, never scaffolding.

**Detection uses our own tool registry, not a dialect list.** We know exactly which
tool names were offered to that model on that run. A candidate message is refused
when either holds:

1. It names one of the run's own offered tools (`adguard_query_log`, `write_file`,
   …) inside a markup-like construct — a tag, a bracketed directive, or a
   key/value block. We do not need to recognise DeepSeek's `｜DSML｜`, OpenAI's
   JSON, or whatever ships next; we recognise *our own tool names appearing where
   prose would not put them*.
2. It is predominantly markup rather than sentences — tag-like tokens making up a
   large share of the content after our own protocol markers are stripped.

Rule 1 is the precise one and does the real work; rule 2 is the backstop for
scaffolding that names no tool. Neither enumerates a provider, so neither goes
stale when a new model arrives.

When in doubt it withholds: a suppressed message costs the user a warning they can
act on, while a forwarded one costs their trust in the channel.

## Explicitly not in scope

**History compaction.** `req.Messages` grows monotonically — nothing in
`api_engine.go` trims, elides or summarises it. Deferred by decision, with the
consequence recorded:

```
tool result cap        8 KiB      ≈ 2k tokens
per turn               assistant turn + tool result ≈ 2.5k tokens
150 turns              ≈ 375k tokens of history, plus a large system prompt
```

So on a 128k-context model the request breaks around **turn 45–50**, and around 75
at 200k. **The 150 hard ceiling is therefore not reachable in practice on most
models**, and the raised base budgets of 30/50 sit closer to that wall than the old
25 did. A context overflow surfaces as a generic provider error through
`mapProviderErr`, which is honest but opaque.

Compaction is the prerequisite for the turn ceilings to mean what they say. It is
the natural next piece of work, and it needs care: eliding the wrong turn makes an
agent repeat work it has already done.

**Cumulative token ceiling.** Considered and rejected. It measures the wrong
quantity: `test-new-1`'s 926k was the *sum* across ~25 requests, averaging ~37k
each — comfortably inside any modern context window. The quantity that actually
fails is per-request size, and that is compaction's job.

## Testing

- **Budget** — a productive loop runs past the base budget to completion; an
  unproductive one stops at 6 consecutive dead turns; neither exceeds 150.
- **Exhaustion** — a loop that exhausts its budget produces the deterministic
  message; a grace-turn reply that violates the contract is discarded, and one that
  satisfies it is used.
- **Delivery contract** — the exact DSML payload from this incident is never
  delivered; a genuinely forgotten `[CHAT]` still is. Both as table-driven cases,
  including markup from other providers so the rule is not fitted to one dialect.

## What this does not fix

A weak model still fails at complex agents; it just fails honestly. Nothing here
makes DeepSeek v4 Flash good at 44-domain workloads, and an agent that needs 80
tool calls remains expensive on any model. Choosing a stronger model, or designing
a cheaper agent, remains the answer to that — this work only guarantees that the
failure is visible, bounded and never delivered as garbage.
