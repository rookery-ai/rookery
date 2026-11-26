---
name: run-hygiene
description: Use this skill for any agent that downloads pages or files, parses data, or keeps notes between runs — deciding where scratch files, memory and results belong so a run leaves nothing behind but what the owner asked for. Triggers include "scrape", "download", "parse", "clean up", "temporary files", "logs", "keep my knowledge base tidy", "don't store junk".
version: 1.0.0
license: MIT-0
category: Agent Behaviour
---

# Run Hygiene

Every run produces three kinds of material, and each has exactly one home. Put
it anywhere else and it either vanishes when you need it or piles up forever in
the owner's knowledge base.

| What it is | Where it goes | How long it lives |
|---|---|---|
| Downloaded pages, API responses, files you parse, intermediate output | `$TMPDIR` | This run only |
| What the next run needs to know (ids already seen, last values, cursors) | Your state (`[STATE]`, `set_state`, or `rookery state set`) | Until you change it |
| What the owner asked for (findings, reports, lists) | The owner's notes, in `notes/` | Kept |

## Scratch goes in `$TMPDIR`

`$TMPDIR` is a folder private to this run. The platform creates it when the run
starts and **deletes it when the run ends** — whether the run succeeded, failed,
was stopped or ran out of budget. So:

- Save every download and every intermediate file there:
  `curl -s -o "$TMPDIR/listing.html" "$URL"`.
- Never use `/tmp`: the sandbox does not grant it, and the error reads like an
  unrelated failure.
- Never rely on a scratch file surviving to the next run. If the next run needs
  something from it, extract the few values that matter and put them in state.

## Never store fetched content in the knowledge base

Do not write downloaded pages, raw API responses or parse output into the
knowledge base — not into `notes/`, not into your own folder, not into a
`cache/` or `tmp/` folder you make up there. Everything in the knowledge base is
backed up every night and shows up in search for chat and every other agent.
Raw third-party pages are noise at best, and at worst they carry text written to
manipulate whoever reads them next.

## `logs/` belongs to the platform

`logs/run_<time>.md` is the platform's record of each run. It keeps the **last 15
runs** of every agent and deletes older ones automatically. Do not write your own
files into `logs/`, and do not rely on an old run note existing.

## Keep state small

State is re-read at the start of every run and costs tokens every time. Store
identifiers and the few numbers you compare against, not page text. When a list
of "already seen" ids keeps growing, drop entries that have been gone from the
source for several runs.

## Cleaning up when the owner asks

If your instructions ask you to tidy up after yourself — for example "delete the
draft files you no longer need" — delete only files you created, only inside your
own agent folder or the owner's notes you were told to manage, and say in your
message what you removed. Never delete `AGENT.md`, `state.md`, `tools/`, run
notes in `logs/`, or anything belonging to another agent.
