---
name: resilient-runs
description: Use this skill for any agent that runs unattended on a schedule — deciding when to retry versus give up, reporting partial results honestly, degrading when a service is down, and never claiming success it did not have. Triggers include "what if it fails", "handle errors", "make it reliable", "retry", "what happens when the service is down".
version: 1.0.0
license: MIT-0
category: Agent Behaviour
---

# Resilient Runs

A scheduled agent runs with nobody watching. If it fails silently, or claims
success it didn't have, the user finds out the hard way — much later, and
after trusting a result that was wrong. This skill covers how to fail well.

## Transient vs. permanent failure

Not every failure deserves the same response. Classify before you react:

- **Transient — worth retrying:** network timeouts, connection resets,
  `429 Too Many Requests`, `5xx` server errors. These reflect a temporary
  condition on the other end; the identical request often succeeds a moment
  later.
- **Permanent — never retry as-is:** `404 Not Found`, `401`/`403` auth
  failures, `400` malformed input, a parse error on data that's actually
  malformed. Retrying the exact same request will fail identically every
  time — it wastes the run's time budget and can look like a hang.

```python
TRANSIENT = {429, 500, 502, 503, 504}
PERMANENT = {400, 401, 403, 404, 422}

if status in TRANSIENT:
    retry_with_backoff()
elif status in PERMANENT:
    stop_and_report(f"request failed ({status}) — not retrying, this won't change")
```

See the `api-integration` skill for the retry-with-backoff mechanics
themselves; this skill is about which failures deserve that treatment.

## Report partial results explicitly

If a job processes many items and some fail partway through, don't silently
drop the failures and report only what worked, and don't abandon the whole
run because one item failed. Say exactly what happened:

```
[CHAT]
Processed 42 of 45 invoices. 3 failed (vendor "Acme Corp" — malformed date on
all three). The other 42 are filed normally.
[/CHAT]
```

This is more useful to the user than either extreme — a silent partial
success hides a real problem; an all-or-nothing abort throws away work that
did succeed.

## Never claim success that didn't happen

If the action didn't actually happen — the email didn't send, the file didn't
save, the API call never confirmed — don't say it did. This holds even under
uncertainty: "I called the API but didn't get a confirmation back" is honest;
"Sent!" when you don't actually know is not. When genuinely unsure whether an
action completed, say so rather than guessing optimistically.

## Record what completed, so the next run resumes instead of repeats

For anything that processes a list or does multi-step work, record progress as
you go — not just at the very end. If the run dies partway (timeout, crash,
killed), the NEXT run should pick up where this one left off, not redo
everything or skip everything.

**Use `set_state` for this, not `[STATE]`.** The `[STATE]` marker is read from
your final output, so a run that dies before producing output records nothing —
which is precisely the run you were protecting against. `set_state` writes
immediately:

```
set_state(patch={"processed_ids": ["inv-101", "inv-102"], "last_run_partial": true})
```

It merges the same way, so a closing `[STATE]` block still works alongside it.
A CLI coder uses `rookery state set '<json>'`.

The `[STATE]` form remains correct for a summary written once at the end:

```
[STATE]
{"processed_ids": ["inv-101", "inv-102", "inv-103"], "last_run_partial": true}
[/STATE]
```

Next run: read `processed_ids`, skip anything already in it, and continue.
This is the same state-based resumption the `change-detection` skill uses for
watching feeds — the pattern generalizes to any job that might not finish in
one shot.

## Degrading when a service is down

If a job depends on a service that's unreachable, prefer to do the parts that
DON'T need it and report the rest as blocked, rather than failing the entire
run over one dependency:

```
[CHAT]
Couldn't reach the weather service (it's been down since ~8am), so today's
digest skips the forecast. Everything else is below.
[/CHAT]
```

## A failed run still owes the user two things

1. **What failed**, in plain language (see the `notification-writing` skill
   for how to phrase it — no stack traces, no internal file paths).
2. **What happens next** — will it retry automatically at the next scheduled
   run, does it need the user to do something (re-auth a connection, fix a
   bad input), or is this a one-time blip that's already resolved?

```
[CHAT]
Couldn't sync your calendar — the connection needs to be re-authorized. I'll
keep trying at the next scheduled run, but this one probably needs you to
reconnect it from the Connections page.
[/CHAT]
```

Silence on a genuine failure is the one thing this skill exists to prevent —
a run that produced nothing deliverable and didn't intentionally go
`[SILENT]` should always surface as a visible failure, not vanish.
