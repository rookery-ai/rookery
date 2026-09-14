# Referenced-file context for chat turns

**Date:** 2026-09-14
**Status:** accepted

## The problem

A chat turn that names a knowledge-base file and asks for a change sometimes
answers `Done! Updated your note.` and writes nothing. It is intermittent, it
reports success, and until PR #316 it left no evidence in the log.

Two turns from one install, same model (`deepseek-v4-flash` via OpenRouter),
same file, same system prompt, minutes apart:

**Failed** — one turn, no prior reads:

```
user:      Update notes/probe-rich-formats.md — change Status: draft to Status: final.
assistant: Done! Updated notes/probe-rich-formats.md — changed Status: draft to Status: final.
```

The file was unchanged. `chat: turn finished … milestones=3` — two tool calls
happened; nothing recorded which.

**Worked** — two turns, the first of which read the file:

```
user:      Give me a short summary of my knowledge base file `notes/probe-rich-formats.md`…
assistant: …quotes the file's actual contents, including "Status: draft"…
user:      Change the status from draft to final
assistant: Done!   ← the file really changed, all rich formatting intact
```

The measured difference is **whether the file's content was already in the
conversation when the edit was asked for**. With the content in hand the model
produced a correct `edit_file`; having to read *and* edit inside one turn, it
did neither and said it had.

This also **rules out** the tidier explanation. `selectionEditPrompt` carries an
explicit *"apply the change to the file directly"* and its comment records this
failure being fixed once before by adding that sentence — but the working case
above used `chatPrompt`, which contains no such instruction. The extra wording
is not what made it work.

## What this change is, and what it is not

**It is a feature:** a file named in chat is loaded into the turn, giving the
chat page the parity with "Chat about this file" and "Edit with AI" that those
buttons get only as a side effect of their opening message. That is verifiable
by unit test.

**It is not a proven fix for the bug.** It recreates the condition that
separated the two turns above, so it should help. But `n=1` on each side, the
model is stochastic, and if PR #316's log later shows `edit_file×1(1 err)` then
the model was already trying and the tool refused — in which case this change
gives it a better `old_string` without guaranteeing the call lands. The honest
statement is that the bug is verifiable only empirically, after deploy.

## Approaches considered

**A — resolve referenced files host-side (chosen).** Deterministic, testable,
and it is what the KB buttons achieve by accident.

**B — strengthen the prompt.** Rejected. `BuildChatSystemPrompt` already says
*"When the user asks to add or change a note, use write_file (new note) or
edit_file (modify in place)"*. The instruction exists and was not followed;
more words would be a third guess.

**C — engine-level nudge when an edit request makes no mutating call.**
Rejected. `chat.CleanReply` binds because it is line-anchored and
deterministic; *"is this an edit request"* is not. A nudge misfiring on
conversational turns would be a worse bug than the one it chases.

## Design

### Where it lives

`internal/chat`, beside `BuildUserContext`. Both turn-assembly sites already
call that package:

- `web/handlers_misc.go` (the SPA)
- `cmd/rookery/main.go` (Telegram / Discord / Slack)

A helper in `web` would silently leave the chat platforms without it — exactly
the divergence the comment at the first call site warns about. `internal/chat`
already imports `internal/vault`, so this adds no new dependency and creates no
cycle (`internal/coder` does not import `internal/chat`).

### Public surface

```go
// internal/chat/references.go
func ReferencedFiles(v *vault.Vault, workspaceID, message string) string
```

Returns `""` when the message names nothing resolvable, so both call sites
append unconditionally.

### Detection

Narrow on purpose — a false positive spends context and can mislead:

1. **Explicit paths**, backticked or bare, containing `/` and ending in a file
   extension (`notes/trip.md`).
2. **Wikilinks**, `[[Target]]`, resolved through `vault.LinkIndex`. Gated on the
   message actually containing `[[`, because building the index walks the whole
   vault — the cost is paid only when a wikilink is present.

A bare title with no extension and no brackets is *not* matched: too fuzzy, and
the model can still `search_files`.

### Resolution and safety

Every candidate goes through `vault.Resolve` (the security primitive that
rejects `..` and absolute escapes) and is then stat'd. A path that does not
resolve or does not exist is **skipped silently** — the model still has
`search_files` and `glob`, and an error block would train it to distrust the
context. Capped at **3 files** per turn.

### Content vs. map

- Under `maxInlinedNote` (8 KiB): the file's content verbatim.
- Over it: `vault.MapFile(...).Render(...)` — the same shape summary
  `kb_file_map` returns.

8 KiB anchors to what `read_file` itself would have returned in one call;
inlining more than the tool would makes the block a worse version of the tool.

**Stated limitation:** a map gives shape, not an exact `old_string`, so an edit
to a large file can still fail. That is a known edge of the feature, not
something this design hides.

### Placement in the prompt

Appended to `sysCtx`, not prefixed to the user's message. Prefixing pollutes
what the model believes the user said and invites someone to persist it later.
The cacheability objection does not apply: `BuildUserContext` already calls
`time.Now()`, so `sysCtx` is rebuilt every turn regardless.

### Block wording

Load-bearing, and one hazard drives it. Handing the model the full text invites
it to reply with a rewritten version, or to call `write_file` with the whole
document — which would flatten exactly the rich-text constructs (callouts,
toggles, column grids, alignment) that a surgical `edit_file` preserves. The
block therefore:

- names the path and states this is the file's **current content on disk**;
- says that changing it means calling `edit_file` with a unique `old_string`;
- says that describing the change in the reply is not a change.

## Testing

- Detection: backticked, bare, wikilink, several references, none.
- Safety: `../` escapes and absolute paths outside the vault are refused;
  a non-existent path is skipped rather than erroring.
- Cap: a message naming five files yields three.
- Map path: a file over the inline cap renders a shape, not its content.
- Wording: the block tells the model to use `edit_file` — pinned, because a
  rewrite that drops that sentence reintroduces the whole-file-rewrite hazard.
- Parity: both turn-assembly sites call it. Pinned by a test, since divergence
  here is invisible in either file alone.

## Out of scope

- Whether a **whole-file rewrite** preserves rich-text constructs. The one
  clean sample covers only the surgical `edit_file` path.
- The KB editor dropping a note's trailing newline on save.
- Any change to the prompt's existing editing instructions.
