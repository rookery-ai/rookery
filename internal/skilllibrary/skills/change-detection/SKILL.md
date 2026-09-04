---
name: change-detection
description: Use this skill for any scheduled agent that should report only what is NEW since its last run — watching a feed, a page, an inbox, a listing or a dataset. Covers storing seen IDs and cursors in state, comparing before reporting, and staying silent when nothing changed. Triggers include "notify me when", "watch for new", "alert me if", "check for updates", "only tell me what changed".
version: 1.0.0
license: MIT-0
category: Agent Behaviour
---

# Change Detection

A scheduled agent that watches something (a feed, an inbox, a listing, an API)
must remember what it has already seen. Without memory, every run re-reports
everything — the user gets spammed with "new" items they've already been told
about. This skill covers how to remember correctly using the `[STATE]` protocol.

## The state protocol

`[STATE]{...}[/STATE]` in your output is JSON that gets merged into `state.md`'s
```` ```json ```` fence — not replaced, merged: keys you omit are left alone,
a key set to `null` is deleted. Read the CURRENT state at the start of every run
(it's given to you in context / readable from `state.md`), compare against it,
then emit a `[STATE]` block with the updated values.

```
[STATE]
{"seen_ids": ["1042", "1043", "1044"], "last_checked": "2026-07-21T09:00:00Z"}
[/STATE]
```

Never hand-edit the json fence directly with a file write — always go through
the `[STATE]` marker so the merge semantics (and any concurrent state) are
respected. `state.md` also has a `## Notes` section for human-facing prose about
the agent's own behavior — that's fine to extend directly, it's separate from
the json fence.

### Reading and writing state mid-run

`[STATE]` is emitted at the END of a run, which is too late when you need to
record progress as you go — a run that dies halfway then repeats work it had
already done. Two tools do the same merge at any point:

```
get_state()
set_state(patch={"seen_ids": ["1042", "1043"], "cursor": "2026-07-21T09:00:00Z"})
```

`set_state` merges exactly as `[STATE]` does — omitted keys are left alone, a
key set to `null` is deleted — so the two are interchangeable and safe to use in
the same run. Prefer them over `[STATE]` when you are working through a batch:
record each item as you finish it, and an interrupted run resumes instead of
starting over.

A CLI coder reaches the same thing as `rookery state get` and
`rookery state set '<json>'`.

## What to store: seen IDs vs a cursor

Pick based on what the source gives you:

- **Ordered source (has a timestamp, sequence number, or "next page" token):**
  store a **cursor** — the last-seen timestamp or ID. Next run, ask the source
  for everything after that cursor. This is cheap and doesn't grow.

  ```json
  {"last_seen_id": 8842, "last_checked": "2026-07-21T09:00:00Z"}
  ```

- **Unordered source (a set of items that can appear/disappear, no reliable
  order):** store a **bounded list of seen IDs**. Compare the current set
  against it; anything not in the stored set is new.

  ```json
  {"seen_ids": ["a1f", "b22", "c9d"]}
  ```

**Bound the list.** Don't let `seen_ids` grow forever — that bloats `state.md`
(which gets read every run) and eventually every context it's injected into.
Cap it (e.g. keep the most recent 200–500 IDs) and drop the oldest when you add
new ones. If the source is naturally ordered even loosely (IDs increase, items
have dates), prefer a cursor instead — it never grows at all.

## Compare before reporting

The core loop, every run:

1. Read the stored state (cursor or seen-set).
2. Fetch the current items from the source.
3. Compute the diff: items not covered by the cursor / not in the seen-set.
4. If the diff is non-empty: report ONLY the diff (never the whole listing),
   then update state to include everything you just fetched.
5. If the diff is empty: update `last_checked` if you track it, and go
   `[SILENT]`.

```
[STATE]
{"seen_ids": ["a1f", "b22", "c9d", "d77"]}
[/STATE]
[CHAT]
2 new items since last check:
- ...
- ...
[/CHAT]
```

Nothing new:

```
[STATE]
{"last_checked": "2026-07-21T09:00:00Z"}
[/STATE]
[SILENT]
```

`[SILENT]` alone as the last line suppresses the fallback that would otherwise
deliver your reasoning as a message — use it deliberately whenever there's
genuinely nothing to tell the user. Don't send "no new items" chat messages on
a schedule; that's the exact spam this skill exists to prevent.

## The first-run problem

On the very first run, stored state is empty. If you treat "empty state" as
"nothing seen yet" and report the diff against nothing, you will report
EVERY existing item as new — dumping the entire current listing on the user in
one message. That's almost never what "notify me about new items" means.

**Detect a first run** (state has no cursor / no seen-set for this watch) and
handle it specially: record the current items as the baseline, do NOT report
them, and go `[SILENT]`. Only start reporting diffs from the SECOND run onward.

```
[STATE]
{"seen_ids": ["a1f", "b22", "c9d"], "baseline_set": true}
[/STATE]
[SILENT]
```

If the agent's job description implies the user DOES want an initial summary
("show me what's currently open, then notify me of new ones"), that's a
one-time exception — state it explicitly in the agent's own instructions, and
still only do the full dump once, on the first run, with the baseline
established immediately after.

## Common mistakes

- Storing the full fetched payload as state instead of just IDs/cursor — bloats
  `state.md` for no benefit; store the minimum needed to compute the next diff.
- Comparing against a re-fetch instead of the stored state (racy, and defeats
  the point of persisting anything).
- Forgetting to update state after a successful report — the next run will
  re-report the same items.
- Updating state BEFORE confirming the report was delivered — if the run fails
  partway, prefer to only commit the state update for items you actually
  reported (see the `resilient-runs` skill for partial-failure handling).
