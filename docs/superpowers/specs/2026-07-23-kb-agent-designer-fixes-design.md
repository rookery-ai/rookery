# KB, Agent-Designer & Chat fixes — design

**Date:** 2026-07-23
**Status:** approved (owner delegated full authority to implement without review)

A batch of independent, mostly-UX defects reported against the SPA and the
agent designer. Each item is traced to its real cause below and fixed as a
self-contained change. Items are independent enough that each ships as its own
conventional-commit so a problematic one can be reverted alone.

Investigation established these facts (file:line references in each section):
the KB editor is TipTap v3 + tiptap-markdown; the KB `html`/`RenderHTMLLinks`
backend path is dead code the SPA never renders; export
(`internal/export`) is a pure function with no vault access and DOCX already
degrades images to alt-text; every legitimate agent/chat/skill/inbox deletion
uses its own endpoint and never routes through the KB note endpoint.

---

## 1. Prevent orphaning DB-backed records from the KB browser

**Problem.** The KB file tree lets the user delete or rename *any* node,
including `agents/<id>/`, `chats/<id>.md`, `inbox/<id>.md`, `skills/<name>/`
and their contents. Deleting these orphans the backing DB rows and breaks the
agent/chat/skill/inbox item. Guarded nowhere: `vault.Delete` only refuses the
root and `.kb`; the KB handlers (`apiDeleteKBNoteAPI` `web/api_kb.go:757`,
`apiRenameKBNote` `:779`) pass paths straight through; the tree's `system` flag
is set only for root-level folders (`api_kb.go:559`) and gates only cosmetics.

**Design.**
- **Single source of truth.** Add `vault.IsUserMutationProtected(rel string) bool`
  in `internal/vault` — true when the first path segment is a system-managed,
  DB-backed dir: `agents`, `chats`, `inbox`, `skills`, `reminders` (and `.kb`).
  This is the canonical set for *user-initiated KB-browser mutation*. It does
  NOT touch the vault primitives (`Delete`/`Rename`), because legitimate
  deletion from an item's own page also calls those.
- **Backend (authoritative).** In `apiDeleteKBNoteAPI` and `apiRenameKBNote`
  (guarding both `from` and `to`), reject a protected path with
  `403 protected_path` and a message directing the user to the item's own page
  ("Delete this from the Agents page instead."). This is the real guard; it can
  be reached regardless of the UI.
- **Frontend (UX).** In `FileTree.tsx`, derive protection from each node's path
  (top segment ∈ protected set) and: hide the Rename + Delete row-menu items,
  exclude protected nodes from bulk delete/move selection actions, and reject
  drag-move of/into them. `NoteHeader`'s rename/delete for an open protected
  note is likewise suppressed. `notes/`, `memory/`, `assets/` stay fully
  user-editable (they are user content, not DB-backed).

**Not in scope.** Consolidating the other scattered system-dir lists
(`guard.go`, `import.go`) — noted as pre-existing inconsistency; this change
adds one canonical helper for the new guard and leaves the others.

**Verification.** Go handler test: delete/rename of `agents/x/AGENT.md`,
`chats/x.md`, `skills/x/SKILL.md`, `inbox/x.md` → 403; delete/rename of
`notes/x.md` → 200. Confirmed legit deletions bypass this endpoint.

---

## 2. Agent designer: intermittent "name is required" + empty Spec panel

**Problem (shared root cause).** After a build completes via the live SSE
stream, the backend deletes the in-memory design session (the step is done),
but the frontend keeps the transcript and never refetches `/design/state`
(`DesignerSurface.tsx:185-207` only refetches on the `recovery` attach source,
never `live`). Two symptoms:
- **"name is required"** — the next message the user sends is no longer the
  "first" message, so no `name` is attached (`DesignerSurface.tsx:323-325`);
  the backend has no session and returns
  `"name is required to start a new session"` (`web/handlers_agents.go:130`).
- **Empty Spec panel** — `pendingAgentMD`/`pendingTools` come only from
  `/design/state` while the session is active; the live path never refetches,
  and the POST response never carries them, so `SpecPanel` stays on its empty
  state.

**Design.** The primary fix is one change serving both: **on live-SSE build
completion, refetch `/design/state` and transition the surface to its
verifying state** (approve/change UI + populated Spec panel) — mirroring the
existing `recovery` path. With a live session in `StateVerifying`, the
"name is required" path can't fire and the Spec panel populates for free.
- Belt-and-braces: always attach the known `name` to the design POST body when
  present — but this is *backup only*. It must not be the primary fix: with the
  session gone, a stray follow-up would otherwise silently start a brand-new
  design and discard the built agent. Refetch-and-transition prevents reaching
  that state at all.
- Improve `SpecPanel`'s empty-state copy to say what the Spec is (the generated
  AGENT.md brief + tool files, shown once the designer finishes).

**Verification.** Vitest: after a simulated live `onDone`, the surface fetches
state and renders the verifying UI with Spec populated; a subsequent send does
not post a bare `{message}` without name. Existing designer tests stay green.

---

## 3. Agent templates + description not sent into the conversation

**Problem.** Two linked complaints. (a) The 6 hardcoded templates
(`templates.ts:22-64`) feel inert — clicking one only drops text into the
description box (`AgentNewPage.tsx:90-99`), no code, no API. (b) After the user
clicks **Continue**, the description is only *pre-filled* into the composer,
never sent — `confirmName` (`AgentNewPage.tsx:76-78`) just dismisses the
name-gate; `initialText` seeds the textarea and waits for a manual send
(`Composer.tsx:46-60`).

**Design — keep templates, make them actionable via auto-send.** Rather than
delete the templates, make the whole flow do something: **on Continue, if the
description is non-empty, auto-send it as the first message** of the design
conversation. Then a template becomes a genuine quick-start (pick → its
description fills the box → Continue → the conversation actually begins with
that brief). Chosen over deletion because, once auto-send makes them
functional, the templates give non-technical users a concrete starting point —
which is the value the "make them do something" ask points at.
- Implementation: `AgentNewPage` signals DesignerSurface to auto-send the
  initial description exactly once on mount (a dedicated `autoSendInitial`
  prop/flag), distinct from the existing pre-fill `initialText`.
- **Empty-description guard:** never auto-send when the description is blank
  (the "Start from scratch" template and a cleared field) — fall back to
  today's behavior (empty composer, user types the first message).
- Keep `templates.test.tsx`'s banned-word check green.

**Verification.** Vitest: Continue with a non-empty description sends it as the
first design message (with `name` in the payload); Continue with an empty
description sends nothing and mounts an empty composer.

---

## 4. Note duplication (`agents/<id>/notes/` and `notes/`) + guidance

**Problem.** Agents that manage notes write the same note twice — once under
their own `agents/<id>/notes/` and once under the user's top-level `notes/`.
Root cause is prompt guidance, confirmed by grep: **no code copies between the
two**. During a run the coder's workdir is the agent dir, so a *relative*
`notes/` resolves to `agents/<id>/notes/` (`internal/coder/hosttools.go:609-627`),
while the user's real notes are at the vault root. The prompts then:
- tell the agent to "default to notes/" / "write durable knowledge back to
  notes/" as a bare relative path (`prompts.go:316-318`, `:1802-1803`),
- **explicitly license writing in both places** — "You MAY write durable
  markdown notes inside your own directory AND in the user's knowledge base"
  (`prompts.go:1849`),
- and disambiguate correctly in only one isolated place (`prompts.go:419-422`).
- The basic-model backend treats `notes/` as vault-root (`prompts.go:476,483`),
  the tool-calling/full-coder backends treat it as the agent dir — the same
  string means two different locations depending on backend.

**Design (prompt-only; ships without automated verification — stated plainly).**
- Remove the dual-location license at `prompts.go:1849`; replace with a single
  clear rule: durable user-facing notes go to the user's knowledge base
  (`notes/`, `memory/`) **once**, using the absolute vault-root path the run
  prompt already provides; the agent's own directory is for its scratch
  (`tools/`, `logs/`, `state.md`) — do not keep a second copy of a user note
  there.
- Make every "write to notes/" instruction consistent about this across the
  design, implementation, and runtime prompt blocks, and reconcile the
  relative-vs-absolute meaning of `notes/` so the guidance is backend-agnostic
  (state the vault-root path explicitly wherever the agent is told to write a
  user note).

**Verification.** No unit test can cover LLM behavior; guardrail/prompt tests
that assert on block contents are updated. This item's efficacy is a guidance
improvement, acknowledged as best-effort.

---

## 5. Rich-text default + wikilink/link/attachment clickability

Three reported items that converge on the TipTap editor. Wikilinks render only
in WYSIWYG mode; notes fall to a raw `<textarea>` (literal `[[...]]`) when a
per-note `checkFidelity` round-trip fails (`NoteEditor.tsx:271-273`). The
folder correlation the user noticed is coincidental content-shape, not
folder-based. The dead backend `html` path is not involved.

**5a. Default view is rich text — safely (no data-loss regression).**
Default *all* markdown notes to WYSIWYG. For the **lossy** subset, mount the
editor **`editable: false`** (a pretty, read-only rich view): wikilinks render
and links are clickable because TipTap's `handleClickOn`/`handleClick`
editorProps still fire on a non-editable editor. This gives the requested
rich-text default for every note **without** the silent-corruption risk of
letting autosave re-serialize a lossy document. The existing "Edit as rich text
anyway" acknowledgment (`switchToWysiwyg`, `overrideAccepted`) becomes the
consent gate that flips `editable: true` before any re-serialization. Non-lossy
notes stay fully editable exactly as today. Raw toggle remains the escape hatch.
Scope: markdown (`.md`) notes only — non-markdown files keep their existing
appropriate viewer (a `.json`/`.py` is not markdown). *Must verify at build
time that `editable:false` still fires the click handlers before relying on it.*
This also resolves the wikilink inconsistency (they now render for every note).

**5b. Regular markdown links clickable on plain click.** `editor.ts:34` sets
`openOnClick:false`, so only Ctrl/Cmd-click opens a link. Make plain-click open
`http(s):`/`mailto:`/`/api/v1/kb/` and vault-asset links in a new tab (matching
wikilink plain-click behavior). Link editing remains available via selection /
the bubble toolbar. Applies in both the read-only lossy view and the editable
view.

**5c. Attachments portable + clickable + exportable.** Attachments currently
insert a link whose `href` is the **served URL** (`NoteEditor.tsx:129`), unlike
images which store a **portable vault path** (`kbImage.ts`). Change
`handleAttachment` to insert the portable path (`res.path`, e.g.
`assets/foo.pdf`); resolve portable → served URL at click time
(`assetDisplayURL`) so the link opens, and keep the on-disk markdown portable so
it survives export. Mirrors the established `KBImage` pattern.

**Verification.** Vitest for the click handlers and attachment insertion;
manual smoke of the rendered editor.

---

## 6. Chat titles editable

**Problem.** A chat's title is static (`ChatWindow.tsx:300`, `ChatsPage.tsx:66`);
there is no rename UI and no HTTP endpoint — though `db.UpdateChatName`
(`repositories.go:741-744`) already exists (used only by auto-title).

**Design.**
- Backend: add `PATCH /api/v1/chats/:id` (body `{name}`) → validate non-empty
  + owned → `db.UpdateChatName`. Add the route to the `want` inventory in
  `web/api_parity_test.go` (hard merge gate).
- Frontend: an inline editable title in the `ChatWindow` header (mirroring
  `NoteHeader`'s title input) + a `useRenameChat` mutation in `chats.ts` that
  invalidates the chats list and the chat query.

**Verification.** Go handler test (rename own chat → 200 and persisted; empty
name → 400; other workspace's chat → 404). Parity inventory updated.

---

## 7. Embedded images/files exportable (HTML/PDF)

**Problem.** Export renders the note markdown as-is with no vault access
(`internal/export/export.go`), so `![](assets/foo.png)` becomes
`<img src="assets/foo.png">` — a dangling relative reference in the downloaded
file. DOCX already degrades images to alt-text by design (`docx.go:301`).

**Design.** In `apiExportKBNote` (`web/api_kb.go:916`), **before** handing the
body to `export.*`, rewrite vault-relative asset references
(`![alt](assets/…)` and `[file](assets/…)`) into `data:` URIs by reading the
bytes from the vault and base64-encoding with a sniffed content type. This
keeps `internal/export` pure and makes **HTML and PDF** (PDF is rendered from
that HTML) self-contained. DOCX is unchanged (documented alt-text degradation).
Markdown export stays the raw note (already portable via the paths from item 5).
A missing/oversized asset is left as its original reference rather than failing
the whole export.

**Verification.** Go test: an HTML export of a note referencing an
`assets/x.png` contains a `data:image/png;base64,` src and no bare
`assets/x.png`. `AvailableFormats` unchanged.

---

## Cross-cutting

- **Commits:** one conventional commit per item (`fix(kb): …`, `fix(designer):
  …`, `feat(web/chat): …`, `docs: …`) for independent revertability.
- **Gates:** `go test ./...`, the vitest suite, `make ui && go build`, and a
  curl smoke of `/` + `/api/v1/auth/session` all pass before "done".
- **Untested-by-nature:** item 4 (prompt guidance) ships acknowledged as
  best-effort.
