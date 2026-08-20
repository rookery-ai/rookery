# Chat leaks the agent output-protocol markers

**Date:** 2026-08-20
**Status:** accepted

## Problem

One-off chat displays the agent output-protocol markers — `[CHAT]`, `[/CHAT]`,
`[SILENT]`, `[STATE]…[/STATE]` — verbatim in the assistant's reply. It reads as a
platform defect, and the model compounds it: asked about the markers, it told the
owner *"Unfortunately, I can't remove it from my end — it's part of the platform's
protocol."*

Measured against the live database (192 assistant messages): **30 rows carry
markers, 10 are nothing but markers.**

It looks intermittent, which is what made it hard to place. It is not. Two
independent causes compose:

### Cause 1 — chat is told to emit them

`BuildChatSystemPrompt` injects `platformContextBlock(SurfaceChat, …)`, and that
block writes a `## Output protocol (how agents communicate)` section
unconditionally — `[CHAT] Message to send to the user`. Chat receives an agent's
output protocol as a standing instruction.

Whether a model obeys a system-prompt instruction is exactly the axis that varies
most across model family and strength, which is why switching coder models changed
the symptom. It also varies *per turn within one model*: the section sits deep in a
long system prompt and its weight falls as the conversation grows. One observed
session replied cleanly to "What is the purpose of this platform?" while the turns
either side of it were wrapped.

Asking for silence reproduces it on **every** model tested (deepseek-chat-v3.1 and
deepseek-v4-flash both), because the prompt defines `[SILENT]` as the way to say
nothing — so a request for silence is precisely what reaches for the protocol.

The API-engine kickoff was investigated and cleared: `chatToolsAPI` builds its own
request and calls `runToolLoop` directly, so chat never receives
`APIEngineKickoffMessage`. The system block is the whole prompt-side cause.

### Cause 2 — nothing strips them on any chat surface

`handleChatMessage` sends `result.Text` verbatim to the JSON response, to
`AddChatMessage` and to `MaybeAutoTitle`; `cmd/rookery/main.go` does
`send(result.Text)`. The SPA has no client-side stripping (verified).
`prompts.StripProtocolMarkers` exists but has exactly one caller — the KB assist
endpoint.

### Cause 2b — the history re-teaches the model

Both chat handlers pass `db.ListChatMessages(id)` **raw** into `coder.Chat`, so every
previously-leaked turn is few-shot evidence to keep going. One session shows the
escalation directly: clean early, then wrapped on nearly every turn after the first
leak. Stripping only the outgoing reply would fix new chats and leave existing ones
re-teaching themselves.

## What already works — deliberately untouched

Every other surface that displays coder output already strips, and none of it changes:

- **Agent runs** — `agentrunner.parseCoderOutput`, a full structural parse including
  stray `[/CHAT]`.
- **Dry-run review sample** — `dryRunOutput`.
- **Build review sample** — `generationPreviewFallback`.
- **KB assist** — `prompts.StripProtocolMarkers`.

Also untouched: inbox delivery, vault reflection, run logs (raw is correct — a debug
artifact), `parseBlockedOutput` / `parseTestOutput`, and raw `History` storage, which
the `[TECHNICAL SPEC]` extraction and the plan-ready signal both read from.

## Design

### Line-anchored, not substring

A marker that **opens a line** is protocol. A marker **inside a sentence, or in
backticks**, is the model legitimately explaining itself. Both shapes occur in the
live data:

    leak  →  \n\n[STATE]{"last_email_search": {…}}[/STATE]
    docs  →  - **`[STATE]{"key": "value"}[/STATE]`** — saves data into…

A substring replace cannot tell them apart. It was the first design, and on a real
reply enumerating the four markers it produced ``- **``** — saves data into…``.
Line-anchoring separates them cleanly, measured across every row in the database:

| | |
|---|---|
| rows carrying markers | 30 |
| residual leaks after cleaning | 0 |
| clean-prose rows mangled | 0 / 162 |
| marker-only rows | 10 |

### `chat.CleanReply`

New, in `internal/chat`. Rules, all line-anchored:

- `[CHAT]` / `[/CHAT]` opening a line (leading whitespace allowed — the live data has
  six-space-indented forms) are removed; the rest of the line is kept.
- A line that is only `[SILENT]` is dropped. Matched leniently (decoration, case,
  trailing punctuation) for the reason `agentrunner.isSilentMarker` documents: a
  missed marker is worse than a lenient match here.
- A line opening `[CALL:` is dropped.
- A `[STATE]…[/STATE]` block **opening a line** is removed **entirely, JSON included**,
  as is an orphan `[STATE]` opener.
- Blank-line runs are collapsed and the result trimmed.

**Marker-only replies get a neutral placeholder, never the raw text.** Ten rows are
nothing but `[SILENT]`. Falling back to raw would re-display the exact leak this
change exists to remove; rendering an empty bubble is the worst available outcome,
already recorded for `UserFacingDesignText`. So an emptied reply is replaced with a
short neutral line.

### Two strippers, deliberately

`prompts.StripProtocolMarkers` is **not** changed and **not** merged into this one.
Its input is a rewritten KB passage, where content between markers *is* the answer —
so it keeps `[STATE]` content, which its own test pins. `chat.CleanReply`'s input is a
conversational reply, where a leaked state block is machine memory the owner must
never see. Different policy for different input, not drift. Both carry a comment
saying so, because merging them is the obvious future "cleanup".

### Edges

**Chat**

1. `platformContextBlock` — the output-protocol section is emitted for `SurfaceAgent`
   only. `SurfaceChat` gets one line instead: it is not an agent run, replies are plain
   prose, never emit the markers.
2. History cleaned in-flight before `coder.Chat` (web + chat platforms).
   Non-destructive; no migration.
3. Reply cleaned **once into a variable**, then used for the response,
   `AddChatMessage` and `MaybeAutoTitle` alike.
4. `toAPIChatMessage` — the read path, which cleans the 30 existing rows on display.

**Designers** (same input shape, same cleaner)

5. `agentdesigner.UserFacingDesignText` — chained **after** `stripTechnicalSpec`.
6. `skilldesigner` live turn.

**Optional**

7. `dryRunOutput` drops a stray `[/CHAT]`, which it currently appends verbatim while
   the run it rehearses strips it.

## Ordering constraints

- In `UserFacingDesignText`, `stripTechnicalSpec` runs **first** and the cleaner
  **after** the `shown != ""` check. Reversed, the spec delimiters vanish, the block is
  no longer found, and the entire machine-facing spec is rendered to the user.
- Cleaning the reply happens once, before any consumer. Cleaning per consumer invites
  the three to disagree.

## Risks

- A chat reply whose *first line* begins with a marker token while genuinely
  discussing it loses that token. Judged acceptable: no such row exists in the live
  data, and every documentation-shaped occurrence is mid-line or fenced.
- The designers' `History` keeps raw text, unchanged, so the generator's brief and the
  plan-ready signal are unaffected.

## Verification

- `chat.CleanReply` unit tests use the real leaked rows as fixtures, with the
  four-marker explanation reply as the must-not-mangle golden case.
- The three packages the owner named as load-bearing — `agentdesigner`,
  `skilldesigner`, `agentrunner` — are baselined green before the change and must be
  green after, with no test edits.
- Full `make ci`.
