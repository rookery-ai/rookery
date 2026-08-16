# The knowledge base ↔ chat round trip

Three defects in the KB editor's AI affordances, two of which look like KB bugs
and are not.

1. The quick actions — Improve, Proofread, Explain, Reformat — return text
   wrapped in a stray `[CHAT]` marker.
2. "Edit with AI" opens the chat, the model rewrites the note on disk, and the
   open editor shows the old text until the page is reloaded.
3. "Edit with AI" pre-fills the composer with a quoted passage and waits, rather
   than opening the conversation.

## 1. `[CHAT]` is leaking out of the API engine, not out of the KB handler

`web/api_kb_assist.go` returns `strings.TrimSpace(result.Text)` verbatim, and
`prompts.BuildKBAssistPrompt` says *"Return only the rewritten passage, with no
preamble, no explanation and no code fence."* Neither is the source.

`Coder.Generate` on the API engine (`runAPI`) sends the caller's prompt as the
**system** message and a fixed **user** message:

```go
const APIEngineKickoffMessage = "Proceed with your task now, following the " +
  "system instructions above. Emit your final result using the output " +
  "protocol ([CHAT], [STATE], [SILENT])."
```

Every `Generate` call on the API engine is therefore instructed to emit agent
protocol markers, whether or not the caller wants them. A well-behaved model
does exactly as told.

**The blast radius is wider than the reported symptom.** Every `WithNoTools()`
`Generate` caller wants plain content and is being told to wrap it:

| Caller | Wants |
|---|---|
| `web/api_kb_assist.go` | a replacement passage (the reported bug) |
| `prompts.BuildSkillMetaPrompt` (`cmd/rookery`) | a bare JSON object |
| `prompts.BuildReminderParsePrompt` (`web/handlers_misc.go`) | a bare JSON object |
| `Coder.Ping` | the single word `PONG` |

The two JSON callers are the interesting ones: a `[CHAT]`-wrapped body fails to
parse, and both degrade silently to a fallback rather than reporting anything.
That is why this went unnoticed for so long.

**It is API-engine-only.** A CLI coder's `Generate` never sees this message, so
the same install produces the bug or not depending on `coder_kind` — which is
exactly the shape of thing that gets misdiagnosed as flaky.

### Fix

`runAPI` selects the kickoff by whether tools are offered:

- **With tools** (agent builds and runs) — unchanged. The protocol is how a run
  reports back, and `agentrunner.parseCoderOutput` depends on it.
- **With `noTools`** — `APIEngineTextKickoffMessage`: *"Proceed with your task
  now, following the system instructions above. Reply with the requested content
  only — no protocol markers, no preamble and no code fence."*

Keyed on `noTools` rather than a new option because the two coincide exactly
today and every `WithNoTools` caller was audited above. A future caller wanting
protocol markers **without** tools would need an explicit opt-in; the test names
that so the next person meets the decision rather than the accident.

A **defence-in-depth strip** goes in `api_kb_assist.go` regardless
(`prompts.StripProtocolMarkers`, reusing the marker list
`generationPreviewFallback` already knows). Prompts steer, they do not
guarantee, and a weak model will re-emit a marker it has seen a thousand times.
The prompt change is the fix; the strip is what makes the endpoint's contract
true.

## 2. The editor never adopts an external change

`useKBNote` keys on `["kb-note", path]`, so an invalidation refetches — but
`NoteEditor`'s seeding effect is guarded by `initializedRef`, which latches on
first load and ignores every subsequent `data`. Nothing in the browser tells it
the file changed.

### Where the invalidation goes

`ChatWindow.sendTurn` is the single point at which any chat turn completes on
any surface — the slide-over, the chats page, "Chat about this file", "Edit with
AI". It already invalidates `["chat", id]` and `["chats"]`. It gains
`["kb-note"]` and `["kb-tree"]`.

Invalidating the whole `kb-note` prefix rather than one path is deliberate: the
chat has `Write`/`Edit` over the entire vault and the browser has no idea which
file it touched. React Query refetches only **active** queries, so in practice
this is one request for the open note and nothing else. `kb-tree` is included
because a chat turn can create a note, and a tree that does not show it is the
same bug one level up.

This is a chat-turn hook, not a panel-close hook. Closing the slide-over would
miss the reported flow entirely — the user watches the reply land and expects
the note to follow, without closing anything.

### What the editor does with it

A new effect compares `data.content` against `lastSyncedRef` — the exact bytes
last loaded or last successfully PUT. That reference, not "any new data", is
what distinguishes an external write from the editor's own echo:
`useSaveNote.onSuccess` invalidates `["kb-note", path]`, so **every autosave
already causes a refetch**, and a naive comparison would fire on every keystroke
pause.

Then, per the agreed policy:

- **Clean** (`!dirtyRef.current && !savingRef.current`) — adopt silently.
  Re-split the frontmatter, re-run `checkFidelity`, reset `rawText`, and bump
  `editorKey` to remount `WysiwygEditor` (TipTap's `useEditor` reads `content`
  only at creation). **The caret is lost.** That is a real cost, accepted: the
  alternative is a `setContent` whose position mapping across an arbitrary
  external rewrite is not meaningfully better, and the user was reading the
  chat, not typing.
- **Dirty** — adopt nothing. Show a toast: *"This note was changed by chat."*
  with a **Reload** action that performs the same adoption, discarding local
  edits. This file's recorded data-loss history around `dirtyRef` is the whole
  reason the clean/dirty split exists rather than an unconditional swap.

Two guards on the toast: it fires once per external revision (keyed on the
content it is about, so a second refetch of the same bytes does not stack a
second toast), and it never fires while `vanished` is set — a deleted note has
its own notice and does not need a second.

`lastSyncedRef` is updated at three points and nowhere else: initial load,
`flush`'s `onSuccess` (with the exact `content` snapshot that call sent, the
same value that branch already compares against), and adoption.

## 3. "Edit with AI" opens the conversation instead of pre-filling it

`ChatWindow` already has `autoSend` (built for the setup wizard's closing
action), with both guards that matter — a per-mount ref and an empty-history
check. `GlobalChatPanel` simply does not forward it. Adding the pass-through and
setting it from `AIActions.openChat` is the entire mechanism.

The message itself has to change, because auto-sending the current one would be
worse than the pre-fill. `selectionChatPrompt` ends in a blank line — it is a
citation waiting for an instruction, and sent alone it asks the model nothing.
A second builder, `selectionEditPrompt`, states the request:

```
In my knowledge base file `notes/ci.md`, I've selected this passage:

> the pipeline runs on merge

Help me edit it. Ask me what I want changed if it isn't obvious, then apply
the change to the file directly.
```

`selectionChatPrompt` stays as it is for "Chat about this file", which is not an
edit request and should still park in the composer.

"apply the change to the file directly" is what makes §2 observable — without
it the model proposes a rewrite in chat and writes nothing, and there is no
external change to pick up.

## Files

| File | Change |
|---|---|
| `internal/prompts/prompts.go` | `APIEngineTextKickoffMessage`; `StripProtocolMarkers` |
| `internal/coder/api_engine.go` | `runAPI` selects the kickoff on `noTools` |
| `web/api_kb_assist.go` | strip markers from the result |
| `web/ui/src/pages/chats/ChatWindow.tsx` | invalidate `kb-note`/`kb-tree` in `sendTurn` |
| `web/ui/src/components/chat/GlobalChatButton.tsx` | forward `autoSend` |
| `web/ui/src/pages/kb/NoteEditor.tsx` | `lastSyncedRef`, external-change effect, `editorKey` |
| `web/ui/src/pages/kb/ChatAboutFileButton.tsx` | `selectionEditPrompt` |
| `web/ui/src/pages/kb/AIActions.tsx` | `openChat` auto-sends the edit prompt |

## Testing

Go:

- The no-tools kickoff does **not** mention `[CHAT]`/`[STATE]`/`[SILENT]`; the
  tools kickoff still does. Both pinned, because deleting the protocol clause
  outright would break every agent run and look like a tidy-up.
- `runAPI` sends the text kickoff when `noTools` and the protocol kickoff
  otherwise, asserted through a fake provider that records the request.
- `StripProtocolMarkers` on a `[CHAT]`-wrapped passage, an unterminated marker,
  and clean prose (unchanged, byte-for-byte — a strip that rewrites innocent
  text is worse than the leak).
- The assist endpoint returns a stripped result.

Vitest:

- `sendTurn` invalidates `kb-note` and `kb-tree` on a successful turn, and not
  on a failed one.
- Clean editor + changed server content ⇒ adopted, no toast.
- Dirty editor + changed server content ⇒ **not** adopted, toast shown, local
  text intact; Reload adopts.
- An autosave's own refetch (same bytes back) adopts nothing and shows no toast
  — the regression that a naive implementation ships.
- `GlobalChatPanel` forwards `autoSend`; `AIActions.openChat` passes it with
  `selectionEditPrompt`; "Chat about this file" still does **not** auto-send.

## Not doing

- **A server-side change signal** (SSE/polling for vault writes). The chat turn
  is a perfectly good trigger and needs no new endpoint. A file changed by
  something outside this browser — an agent run, another tab — is still only
  picked up on the next load. Real, and out of scope.
- **Merging local edits with an external rewrite.** Reload-or-keep is the whole
  offer. A three-way merge of prose is not a thing to build on the strength of
  this bug.
- **Stripping markers on every coder call site.** `api_kb_assist` gets the
  defensive strip because it is user-visible prose. The JSON callers are fixed
  by the prompt change; giving each its own strip would spread the same rule
  over four files.
