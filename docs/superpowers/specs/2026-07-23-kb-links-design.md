# KB links: clickable externals + backlink correctness (Theme E) — design

**Date:** 2026-07-23
**Status:** approved (design, self-authorized under user delegation)
**Scope:** two link bugs — (#8) external links aren't clickable in the editor,
and (#9) "Linked from" backlinks show on agent notes but not notes-folder notes,
plus link noise from inbox/agent-run logs should be removed.

---

## #8 — external links clickable

**Root cause (found in code):** the WYSIWYG editor uses
`Link.configure({ openOnClick: false })` (`editor.ts`), so links are inert by
design — a deliberate choice so a click in the editor edits text rather than
navigating away. The rendered-HTML path (`renderMarkdown` → `data.html`) is
never used by the SPA, and its wikilink rewrite targets a **dead**
`/dashboard/kb/view` route (all `/dashboard/*` HTML routes were removed except
the OAuth callback).

**Design:**
- Keep `openOnClick: false` (don't hijack plain edit-clicks), but make external
  links reachable:
  - **Cmd/Ctrl-click** on a link opens it in a **new tab** (`window.open(url,
    "_blank", "noopener")`), handled in the editor's `handleClickOn`
    (`NoteEditor.tsx`) alongside the existing wikilink handler. Only `http(s):`
    (and `mailto:`) targets; a vault-relative/wikilink target routes internally.
  - **Hover affordance:** a small link tooltip/popover showing the URL with an
    "Open ↗" button (so a non-power-user who doesn't know the Cmd-click gesture
    can still open it). Lightweight; reuses the existing tooltip primitive.
- **Raw mode** stays a textarea (links not clickable there) — acceptable; raw is
  the escape hatch, and the fidelity work keeps most notes in WYSIWYG.
- `Link.configure` gets `HTMLAttributes: { rel: "noopener nofollow", target:
  "_blank" }` and an `isAllowedUri` guard so only safe schemes become links
  (no `javascript:`), reusing TipTap's URL sanitization.

## #9a — backlinks on notes-folder notes

**Likely cause to verify first (systematic-debugging):** `vault.Backlinks`
already walks **all** `.md` files including `notes/`, so backlinks are computed
for user notes too. The suspected real bug is in **resolution**:
`LinkIndex.byName` maps a lowercased basename → the **first-seen** path in a
sorted walk. When a basename collides (e.g. an agent writes `Foo.md` in its own
dir and the user has `notes/Foo.md`), a `[[Foo]]` link resolves to whichever
path sorts first — so the *other* note never registers the backlink. `agents/`
sorts before `notes/`, so the user's `notes/Foo.md` loses and shows no
"Linked from."

**Design (after confirming the cause):**
- **Prefer user content in name resolution.** When indexing, resolve a bare name
  to a **user-authored** location (`notes/`, `memory/`, root) over a
  system-generated one (`agents/`, `chats/`, `inbox/`, `reminders/`) on
  collision. Concretely: order/priority the walk so user dirs win `byName`, or
  track candidates and pick the highest-priority. Exact-path links
  (`[[notes/Foo]]`) are unaffected (they hit `byPath`).
- This makes `[[Foo]]` from anywhere resolve to the user's note, so its
  BacklinksStrip populates.
- Verify with a golden test reproducing the collision.

## #9b — remove link noise from inbox & agent-run logs

Inbox notifications and agent run logs are machine-generated transcripts; their
`[[wikilinks]]` (and their appearance as backlink *sources*) clutter the graph
and every note's "Linked from."

**Design:**
- **Exclude system-generated note classes from the backlink SCAN as sources.**
  `vault.Backlinks` skips files under `inbox/` and `agents/*/logs/` (and
  optionally `chats/`) when collecting who-links-here, so a user note's "Linked
  from" lists only meaningful, user-facing references — not every run log that
  echoed the note's name. (Same skip set applied in `BuildLinkIndex`'s
  *source* consideration; targets are unaffected.)
- **Suppress the BacklinksStrip UI on system notes themselves.** When the open
  note lives under `inbox/`, `agents/*/logs/`, or `chats/`, don't render the
  "Linked from" strip (`NoteEditor.tsx` / `FileViewer`) — those are logs, not
  knowledge with a link graph.
- Net effect: backlinks become a **user-knowledge** feature (notes ↔ memory ↔
  user files), free of machine-log spam, and correctly populated for
  notes-folder notes via #9a.

## Testing
- **Frontend:** Cmd/Ctrl-click opens external link in new tab (mock
  `window.open`); plain click doesn't navigate; wikilink click still routes
  internally; link hover tooltip renders; BacklinksStrip hidden for an
  `inbox/`/`agents/*/logs/` path.
- **Backend (Go):** name-collision resolution prefers the user note (reproduces
  the bug, then asserts the fix); `Backlinks` excludes inbox/log sources;
  exact-path links unaffected; existing link tests stay green.

## Non-goals
- A full outgoing-links panel (only "Linked from" is in scope; outgoing links
  are already visible inline).
- Backlinks in raw mode.
- Reworking the wikilink syntax or index storage.
