# Chat quality-of-life — design

**Date:** 2026-07-23
**Status:** approved (design)
**Scope:** three independent chat improvements. The KB rich-text editor fidelity
work (originally item 2 of the same batch) is split into its own spec —
`2026-07-23-kb-editor-fidelity-design.md` — because it touches a different
subsystem and carries silent-corruption risk that should not ride alongside
these three small fixes.

This spec covers:

1. **Auto-title** chats from their content (keep the timestamp as smaller text).
2. **Image OCR** on upload — tesseract when present, otherwise save as file.
3. **New chat opens on first click** (currently needs a second click).

---

## Item 1 — Auto-title chats from content

### Problem

A new chat is named `Chat <timestamp>` (`web/api_chats.go` `apiCreateChat`:
`name = "Chat " + time.Now().In(loc).Format("2006-01-02 15:04")`). Nothing ever
renames it, so the chat list is a wall of identical timestamped rows.

### Goal

After the first real exchange, rename the chat to a concise topic derived from
the conversation, while keeping the creation timestamp visible as smaller
secondary text.

### Design

**Trigger (shared, both surfaces).** A new helper in `internal/chat`:

```go
// MaybeAutoTitle renames a chat from its first real exchange, once.
func MaybeAutoTitle(db *db.DB, coderFor func(workspaceID string) *coder.Coder,
    chat *db.Chat, firstUserMsg, firstReply string)
```

It is called right after the assistant turn is persisted, at **both** persist
sites so web and every chat app behave identically (platform-parity rule):

- `web/handlers_misc.go` — right after `s.db.AddChatMessage(id, "assistant", …)`.
- `internal/gateway/router.go` — right after `r.db.AddChatMessage(activeChat.ID, "assistant", …)`.

**When it fires.** Exactly once per chat, gated by two conditions:

- The chat's current `name` still matches the **default** `Chat <timestamp>`
  pattern (a helper `isDefaultChatName(name)` — matches `^Chat \d{4}-\d{2}-\d{2} \d{2}:\d{2}$`).
  Once renamed, it is never auto-renamed again, so a user's manual rename or a
  prior auto-title is never clobbered.
- The user message that produced this turn is a **real user message**, not an
  attachment-confirmation turn. The `📎 Attached **X** to my knowledge base…`
  message is a real coder turn that lands in `chat_messages` (see
  `attachFiles` in `web/ui/src/pages/chats/ChatWindow.tsx`), so a naive
  "message count == 2" trigger would title a chat `Attached invoice.pdf`.
  The helper skips when `firstUserMsg` begins with the attachment-confirmation
  sentinel prefix (`📎 Attached`). This keeps the trigger semantically "first
  substantive user turn answered," not "first row written."

  Rationale for a prefix check over a count: the attachment confirmation is the
  only non-user-authored message that reaches this path, and it always carries
  that exact prefix (`ChatWindow.tsx` constructs it). A prefix guard is precise
  and needs no new column. If a chat's *entire* first exchange is an attachment,
  the chat simply keeps its default name until the first typed message — which
  is the correct behaviour.

**How the title is generated.** A background goroutine (so the user's reply is
never delayed) runs a **text-only** completion through the workspace's own
configured coder:

```go
c := coderFor(chat.WorkspaceID).WithNoTools()
title, err := c.Chat(ctx, chat.WorkspaceID, prompt, systemPrompt)
```

- `systemPrompt`: a new `prompts.BuildChatTitlePrompt()` — instructs a 3–6 word
  Title-Case topic, no quotes, no trailing punctuation, no preamble.
- `prompt`: the first user message and the assistant reply, truncated to a bound
  (e.g. 2 KiB each) so a long turn doesn't blow the budget.
- The result is sanitized: trim whitespace/quotes/trailing punctuation, collapse
  to a single line, cap at ~60 chars. If sanitization yields an empty string,
  no rename happens.

**Persistence.** A new `db.UpdateChatName(chatID, name string) error`
(`UPDATE chats SET name=? WHERE id=?`). The UI's existing `["chats"]` /
`["chat", id]` query invalidation on the next poll/refetch surfaces the new
name; no push needed.

**Failure = silent no-op.** Any error (coder error, usage limit, empty title)
leaves the default name in place. Auto-titling is a nicety, never a blocker;
it must never surface an error to the user or fail the turn.

**Model cost.** One short completion per chat, once, on the workspace's existing
coder — no new provider/config. For an `api` coder this is one cheap call; for a
CLI coder it is one `WithNoTools` invocation. Acceptable.

### Display — "timestamp as smaller text"

`name` becomes the topic; the literal creation timestamp renders as small, dim
secondary text. No schema change — `chats.created_at` already exists and is
surfaced on the DTO (`web/api_chats.go` `toAPIChat`; add `created_at` to the DTO
if not already present).

- **List row** (`web/ui/src/pages/chats/ChatsPage.tsx`): the row already shows
  `c.name` plus `timeAgo(c.updated_at)`. Keep the topic name prominent; show the
  absolute `created_at` (formatted short, e.g. `Jul 23, 15:04`) as the small dim
  line. `updated_at`/`timeAgo` may remain as the activity indicator, but the
  creation timestamp is what "keep the timestamp" refers to and must be present.
- **Window header** (`web/ui/src/pages/chats/ChatWindow.tsx`): `<h2>{chat.name}</h2>`
  gets a small dim `created_at` beside/under it.

Formatting helper: reuse or add a small `formatShortDate` in `lib/utils` — no new
dependency.

### Testing

- `internal/chat`: `isDefaultChatName` matches the default pattern and rejects a
  renamed chat; attachment-confirmation `firstUserMsg` is skipped; title
  sanitization (quotes/punctuation/length/empty) is covered with a stubbed coder.
- Title generation is invoked through an injected `coderFor` so tests use a fake
  coder returning a canned title — no network.
- Frontend: `ChatsPage`/`ChatWindow` render the creation timestamp as secondary
  text.

---

## Item 3 — Image OCR on upload

### Problem

`internal/convert` handles docx/pdf/csv/etc., but the **image** path is a stub:
it returns `"(image file, N bytes — no text was extracted; OCR is not available)"`
with a warning `"image content is not searchable: no OCR"` (`convert.go`). So an
uploaded screenshot/scan imports as an unsearchable placeholder note.

### Goal

Extract text from uploaded images when possible, without adding a heavy or
non-pure-Go build dependency, and degrade gracefully when the tool is absent.

### Design — tesseract-if-present (mirrors the pdftotext pattern)

`internal/convert/pdf.go` already prefers `pdftotext -layout` when it is on PATH
and falls back to a pure-Go path otherwise, warning when extraction looks thin.
The image path adopts the same shape:

- On an image kind, if a `tesseract` binary is on PATH, shell out:
  `tesseract <tmpfile-or-stdin> stdout` (image bytes written to a temp file in
  `$TMPDIR`, or piped via `stdin` if the installed tesseract supports it — a
  temp file is the portable choice), capture stdout as the extracted text.
- Wrap the extracted text as the note markdown. If the OCR output is empty or
  whitespace-only, emit a warning that no text was found (image may be
  non-textual) — the same "declare a thin extraction" discipline `pdf.go` uses.
- If `tesseract` is **not** on PATH, keep the current stub behaviour unchanged
  (save-as-file with the existing "no OCR" warning). No error.
- `convert` stays a **pure function**: no network, no LLM. Shelling to a local
  CLI that is present-or-not is consistent with the existing `pdftotext`
  precedent and keeps the package testable (the CLI branch self-skips when the
  binary is absent, like the AST guardrail tests self-skip without `python3`).

**Explicitly out of scope:** model/vision OCR. `internal/llm` has no multimodal
message support and the chat coder only ever receives extracted *text*, never
the raw image; adding vision to the transport + API engine is a materially
larger change and was deferred by decision.

**Relationship to the `image-ocr` core skill:** unrelated. That skill is the
*agent* path (an agent decides to OCR something during a run). This item is the
*upload/convert* path (a file dropped into chat/KB is converted at ingest). They
do not overlap; both may end up wanting tesseract on PATH, but this spec touches
only `internal/convert`.

### Parity

Web attach (`ChatWindow.attachFiles` → KB import → `convert`) and chat-app
`file_share` attachments (recent Telegram/Discord/Slack commits) both route
through the same `convert` entrypoint, so tesseract-in-convert lands on all
surfaces at once (platform-parity rule) with no per-surface work.

### Enablement note

`tesseract` is **not installed** on the current host. The convert change is
inert until it is: `sudo dnf install -y tesseract` (Fedora). The spec ships the
code path; enabling OCR is a one-line install. Until then, images continue to
save-as-file exactly as today — no regression.

### Testing

- `internal/convert`: a golden test that OCRs a small known image fixture and
  asserts the extracted text **only when `tesseract` is on PATH**, self-skipping
  otherwise (mirrors the `python3`/`pdftotext` self-skip convention). Assert the
  no-OCR fallback path (binary absent → stub markdown + warning) via a forced
  "not found" branch so it is covered on every host.

---

## Item 4 — New chat opens on the first click

### Problem (confirmed from code)

`web/ui/src/lib/chats.ts` `useCreateChat` only invalidates `["chats"]` on
success (no optimistic insert). `ChatsPage.handleNew` creates the chat and calls
`setParams({ chat: chat.id })`. But `ChatsPage`'s `useEffect` (guards against a
dead selection after a delete) runs:

```ts
if (selected && data && !chats.some((c) => c.id === selected)) {
  setParams({}, { replace: true });
}
```

For the brief window before the invalidated `["chats"]` refetch completes,
`data` is present but does **not** contain the just-created chat, so the guard
fires and clears the selection. The window closes; the user must click the row
in the list (which appears after the refetch lands) to open it again.

### Fix

Optimistically insert the created chat into the `["chats"]` cache in
`useCreateChat.onSuccess` (the POST already returns the created chat DTO), then
invalidate:

```ts
onSuccess: (chat) => {
  qc.setQueryData<{ chats: apiChat[] }>(["chats"], (old) =>
    old ? { ...old, chats: [chat, ...old.chats] } : { chats: [chat] });
  qc.invalidateQueries({ queryKey: ["chats"] });
}
```

Now the list contains the new chat synchronously, the `useEffect` guard sees the
selected id present, and the window opens on the first click. The subsequent
refetch reconciles (idempotent — same id, no duplicate).

### Testing

- `chats.test.tsx`: creating a chat selects it and the window renders without a
  second click (the selection is not cleared by the dead-selection guard).

---

## Non-goals

- Model/vision OCR (deferred; `internal/llm` has no multimodal support).
- Renaming a chat that the user (or a prior auto-title) already named.
- Continuous re-titling as the topic evolves — auto-title fires once.
- Any change to the KB rich-text editor — that is the separate fidelity spec.
