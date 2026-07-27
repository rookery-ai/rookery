# Chat message UX + KB new-note flow — design

Date: 2026-07-27
Status: approved (user asked for design → plan → implementation in one pass)

Two unrelated UX defects, grouped because both are small, both live in the SPA,
and both are "the app did the work but didn't take me where I expected".

---

## Part A — Chat

### A1. Clicking a stopped chat resumes it and focuses the composer

Today `ChatsPage` sets `?chat=<id>` and nothing else. A stopped chat opens with
a "Stopped" chip and a Resume button the user must find and press. Sending
actually works regardless (`handleChatMessage` in `web/handlers_misc.go` never
checks `chat.active`), so the stopped state is presentational — which makes the
manual Resume click pure friction.

**Behaviour.** Opening a chat that is not active fires one `resume` action for
that chat, once per open. Implemented in `ChatWindow`, guarded by a ref, so:

- it fires for every surface that opens a chat (list click, deep link, ⌘K
  search hit), not just the list;
- pressing **Stop** afterwards does not immediately re-resume (the ref is
  already spent for this mount, and `ChatWindow` is keyed by `chatId`, so the
  ref resets only when a *different* chat is opened);
- `GlobalChatPanel` is unaffected — it only ever mounts `ChatWindow` for a chat
  it filtered as `active` or just created, so no resume POST fires there.

The composer takes focus whenever a chat is opened. This reverses an earlier
deliberate choice (documented in `ChatWindow`'s `autoFocus` comment and
`ChatsPage`'s `createdId` comment: browsing history should not pop a mobile
keyboard). The user has overridden it; both comments get rewritten rather than
left contradicting the code. `createdId` and the conditional `autoFocus` in
`ChatsPage` disappear — every selection is now a "type now" gesture, so the
distinction they existed to preserve is gone. `GlobalChatPanel` keeps its own
opt-in `autoFocus` as-is (a slide-over opening on its own is still not a typing
gesture).

### A2. Send button gets an icon

The `Send` lucide glyph replaces the word. The button keeps the accessible name
`"Send"` (`aria-label`), so `getByRole("button", { name: "Send" })` in
`chat.test.tsx` / `globalchat.test.tsx` / `attachments.test.tsx` / the designer
suites keeps matching. Square icon button, same disabled semantics as today.

### A3. Per-message footer: timestamp + copy

Each bubble gets a footer line underneath it showing **`Day HH:MM`** (e.g.
`Sun 14:32`) and a copy-to-clipboard button. Both are small (10px), muted, and
hidden until the bubble is hovered or something inside it is focused.

Three constraints drive the implementation:

1. **Text selection must not break.** The footer is *always rendered* and only
   its opacity toggles (`opacity-0 group-hover:opacity-100
   focus-within:opacity-100`). Conditionally mounting it on hover would reflow
   the bubble under the cursor and kill an in-progress drag-select. `select-none`
   goes on the footer row only — never on the message body.
2. **Keyboard reachability.** The copy button carries
   `focus-visible:opacity-100` and a real `aria-label`, so tabbing to it makes
   it visible.
3. **Timezone.** The label is rendered in the workspace profile's timezone when
   one is set, else browser-local.

**Timezone plumbing.** `profile.Timezone` is a free-text settings field, so it
can hold `""`, `"CEST"`, or `"UTC+2"` — none of which
`Intl.DateTimeFormat(locale, { timeZone })` accepts (it throws `RangeError`).
An unhandled throw during render would blank every bubble. So:

- `GET /api/v1/auth/session` gains a top-level `"timezone"` field, populated
  from `profile.Load(db, workspace.ID).Timezone` when a workspace is entered
  (empty string otherwise). The session query is already loaded once by the
  SPA and cached — no new request, and no reaching for `/api/v1/settings`
  (which does filesystem coder-detection on every call).
- A `formatMessageTime(iso, tz)` helper in `lib/utils.ts` validates the zone
  inside a `try/catch` and silently falls back to browser-local. This mirrors
  Go's `profile.LoadLocation`, which already falls back to UTC on an
  unparseable zone.

**Optimistic messages.** `sendTurn` pushes `{role, content}` into `pending`
with no timestamp, so a just-sent bubble would show no time until the refetch
lands. It now stamps `created_at: new Date().toISOString()` at push time.
`reconcilePending` keys on `role::content` only, so dedupe is unaffected.

**Shared component blast radius.** `ChatMessageBubble` is also used by
`DesignerSurface` (agent/skill design conversations), which has no timestamps.
`createdAt` is optional — the time span is omitted when absent, the copy button
still appears. Designer bubbles therefore gain a copy button; that is a
strict improvement, but `designer.test.tsx` / `specpanel.test.tsx` must be run,
not just the chat suites.

---

## Part B — KB "New note"

The ⌘K palette's **New note** action navigates to `/kb?new=note`. Three
separate defects compound there.

### B1. The resume-last-note effect races the new-note intent

`KBPage` has two effects. The first consumes `?new=note` and opens the dialog.
The second — "landing on /kb with no path opens the most recently viewed
file" — fires as soon as `useRecentFiles` finishes loading (which is *after*
first render, because it waits for the session query's workspace id) and calls
`setParams({ path: topRecent.path })`.

So arriving via the palette opens an unrelated note *behind* the dialog. If
that recents entry is stale — deleted or renamed outside this UI —
`useKBNote` errors and `NoteEditor` renders **"Couldn't load this note."**,
which is the reported symptom. (`handleMissing` only rescues a 404 that
`NoteEditor` surfaces as `onMissing`; a generic error shape lands on the error
screen.)

**Fix:** suppress the auto-open while the new-note intent is live. Creating is
not resuming. The suppression is latched in a ref set by the same effect that
consumes `?new=note`, because that effect strips the param from the URL — a
check against `params.get("new")` alone would stop suppressing one render
later, which is exactly when the recents arrive.

### B2. `NewEntryDialog` wipes the name field mid-typing

Its reset effect is keyed `[open, dirPath]` and calls `setName("")`. `dirPath`
is `KBPage`'s `currentDir`, derived from `path` — which changes when B1's
auto-open lands *after* the dialog opened. The user's typed name is cleared
under them, and `Create` then hits `if (!n) return;` and silently does nothing.

**Fix:** reset on the `open` *transition* only (a ref holding the previous
`open` value), not on every `dirPath` change. `dirPath` stays a dependency for
seeding `location` on open, but a change while open no longer clears input.
Fixing B1 removes the trigger; fixing B2 removes the trap for any other
mid-dialog `dirPath` change.

### B3. Creating a note doesn't open it

The actual ask. `NewEntryDialog` gains an optional
`onCreated?: (path: string, isDir: boolean) => void`, invoked with the exact
path the server will have created — including the `.md` the client appends —
after the mutation resolves. Both call sites wire it:

- `KBPage`'s pane-header dialog → `openPath(path, false)`, which sets
  `?path=<new>` (no `dir=1`, so it opens as a document) **and** records the
  note in recents. A `.md` path routes to `NoteEditor`, the rich text editor,
  per `KBPage`'s existing `isMarkdown` rule. Creating a *folder* navigates with
  `dir=1` to the new folder page.
- `FileTree`'s per-folder context-menu dialogs → the same `onSelect` the tree
  already uses for a click, so creating from the tree behaves identically.

Client and server must agree on the created path or the navigation 404s. The
client appends `.md` when the name has no `.md` suffix; the server
(`apiNewKBNote`) appends `.md` only when the basename contains *no dot at all*.
For a plain name (`ideas` → `ideas.md`) they agree. For a dotted name the
client has already appended `.md`, so the server's condition is false and it
writes exactly what was sent — they agree there too. The client's computed path
is therefore always the created path, with no extra round trip.

Folder creation seeds `<dir>/.keep`; the navigation target is the directory
itself, which `FolderPage` renders.

---

## Testing

Vitest, alongside the existing suites:

- `chats.test.tsx` — opening a stopped chat POSTs `resume` exactly once and
  focuses the composer; opening an active chat POSTs nothing; pressing Stop
  after an auto-resume does not re-resume.
- `chat.test.tsx` / a new `messagemeta.test.tsx` — the footer renders for every
  bubble (not hover-gated in the DOM), the time respects a supplied IANA zone,
  an invalid zone falls back instead of throwing, copy writes the raw markdown
  to the clipboard, and the message body carries no `select-none`.
- `utils` unit tests for `formatMessageTime` (valid zone, invalid zone, empty
  zone, unparseable date).
- `kbpage.test.tsx` — with a populated `sa.kb.recent.<wsid>` in localStorage
  and a resolving session (the real timing), `/kb?new=note` must not open the
  recent note; creating a note navigates to it and the rich text editor mounts;
  a `dirPath` change while the dialog is open preserves the typed name.
- Go: `web` package test asserting the session payload carries `timezone`.

## Out of scope

Message-level delete/edit/regenerate, grouping bubbles by day, a relative
"2m ago" mode, and a per-message permalink. Not asked for.
