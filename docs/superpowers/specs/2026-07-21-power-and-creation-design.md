# Power & Creation — Design

**Date:** 2026-07-21
**Status:** Approved (operator delegated design authority: "you have my approval to start implementing")
**Scope:** Sub-plan 9 of the post-redesign track. SP7 (agent files as documents) and SP8 (everyday feel) shipped 2026-07-20.

---

## 1. Problem

SP8 made the app pleasant for everyday use. SP9 is about the two things that remain hard: **creating an agent** and **moving around quickly**.

Two concrete gaps:

- **You approve a build you cannot see.** The agent designer streams a chat transcript and per-tool progress lines, then asks you to approve. But the artifact it produced — the AGENT.md, the `tools/*.py`, the schedule it chose, the skills and connections it declared — is not shown anywhere until after you save. `handleDesignState` (`web/handlers_agents.go:192`) returns history, state, progress flags, and nothing about the build itself, even though the session holds `PendingAgentMD` and `PendingTools` (`internal/agentdesigner/flow.go:85-86`). You are asked "does this look right?" about something you have not been shown.
- **Creating an agent starts from a blank field.** `AgentNewPage.tsx` (108 lines) asks for a name and a description and hands both to the designer. For a non-technical user that is a blank-page problem: the quality of the agent depends entirely on how well the first message is phrased, with no examples of what "well-phrased" looks like.

And two friction items:

- **The app has three keyboard shortcuts total** — ⌘K (palette), ⌘J (chat), ⌘S (save note). Seven rail destinations, all mouse-only.
- **⌘K has no memory and no scoping.** `CommandPalette.tsx` groups results by kind, but every invocation starts cold; there is no way to say "only notes" and no recall of what you just opened.

## 2. Goals

- Show the user what the designer actually built, before they approve it.
- Give agent creation a starting point instead of a blank field.
- Make the seven destinations and the primary lists keyboard-reachable, with a discoverable help overlay.
- Give ⌘K recents and scoping.

## 3. Non-goals — and two things cut from the original SP9 sketch

The post-redesign sketch listed six items for SP9. Two are cut, on measurement:

- **List virtualization — cut.** Measured against the operator's real vault: 8 agents, 20 chats, 249 notes across 102 directories, largest single directory 5 files. The KB tree is collapsible, so only expanded nodes render. Virtualizing lists of this size adds scroll-restoration bugs, breaks find-in-page, and complicates the keyboard navigation this same sub-plan introduces — in exchange for nothing measurable. Revisit if a real list crosses ~500 rows.
- **Slide-over stacking — cut.** There is exactly one slide-over caller in the entire SPA (`GlobalChatButton`). A stack manager for one user is speculative generality. The single-panel `Sheet` in `AppShell` stays as it is.

Also out of scope: editing `tools/*.py` from the spec panel (the designer is the sanctioned path because it re-tests); a full command/action system in ⌘K beyond what exists; templates that generate code.

## 4. Designer spec panel

### 4.1 Backend

`DesignSnapshot` (`internal/agentdesigner/flow.go:595`) gains two fields, populated from the session in `Snapshot()`:

- `PendingAgentMD string`
- `PendingTools map[string]string`

`handleDesignState` exposes them as `pending_agent_md` and `pending_tools`. Both are empty until a build completes — the panel simply has nothing to show before then, which is correct.

This is a read of state the session already holds. No new generation, no new storage.

### 4.2 Frontend

`DesignerSurface` gains a **Spec** view alongside the transcript, showing whatever the current build produced:

- **The brief** — AGENT.md rendered as markdown, not raw text. It is a document written for a person.
- **The files** — each `tools/*.py` in a read-only monospace view, collapsed by default, expandable per file. Reuses the read-only presentation established by SP7's KB code viewer rather than inventing a second one.
- **The schedule** — parsed from AGENT.md's `# Suggested schedule:` line and shown in plain language ("every 10 minutes"), because a cron expression is not an answer for a non-technical user.
- **Declared skills and connections** — parsed from the `# Skills:` / `# Connections:` header lines.

The panel is empty-stated before a build produces anything ("Nothing built yet — the spec appears here once the designer finishes"). It is available in both create and edit flows.

**Why this matters more than it looks:** the approval prompt currently asks for consent about an unseen artifact. This closes that gap, and it costs one backend field pair plus a view.

## 5. Agent templates

Templates seed the **conversation**, not the code. The designer generates everything; a template is a well-phrased opening message that gives it a strong starting brief.

`AgentNewPage` presents a small set of starting points above the free-text field. Picking one fills the description with an editable, specific brief; the user can edit it or ignore templates entirely and type their own. Nothing is locked in.

Six templates, chosen to span the three-tier agent taxonomy the prompts already encode (reasoning-only / one script / multi-file) rather than to cover every use case:

1. **Daily digest** — summarise something each morning and message me.
2. **Watch for changes** — check a page or feed and tell me only when something changed.
3. **Inbox triage** — look through new mail and surface what needs me.
4. **Scheduled report** — pull numbers on a schedule and write them into a note.
5. **Reminder with context** — nudge me about something, with the current state attached.
6. **Start from scratch** — the current blank field, kept as an explicit choice.

Each template's text is written in the same non-technical register the designer prompts demand: it says what the user wants, never how to build it (no mention of scripts, cron, or files). Templates are static strings in the frontend — not DB rows, not a backend concept.

## 6. Keyboard model

### 6.1 Shortcuts

- **⌘1–⌘7 / Ctrl+1–7** — jump to the seven rail destinations in their existing order: Home, Knowledge Base, Agents, Skills, Connections, Chats, Secrets.
- **`j` / `k`** — move down/up the active context-pane list (inbox, chats, agents, KB tree). `Enter` opens the highlighted row.
- **`?`** — open a shortcuts help overlay listing everything, including the pre-existing ⌘K, ⌘J, and ⌘S.
- **`Esc`** — close the overlay, the palette, or the slide-over (existing behaviour, documented in the overlay).

### 6.2 The rule that keeps this from being infuriating

**Single-key shortcuts (`j`, `k`, `?`) must never fire while the user is typing.** They are suppressed when the event target is an `input`, `textarea`, `[contenteditable]`, or inside the ⌘K palette. This is the difference between a keyboard model and a bug report: the app has a WYSIWYG note editor, a chat composer, and a designer conversation — all places where `j` means the letter j.

Modifier shortcuts (⌘1–7) are safe in inputs and stay active there.

The help overlay is the discoverability mechanism; a shortcut nobody can find is a shortcut nobody uses.

## 7. ⌘K scoping and recents

`CommandPalette` (`components/search/CommandPalette.tsx`) keeps its current result grouping and gains:

- **Recents** — the last 8 opened results, persisted in `localStorage` under one key, shown when the input is empty. Mirrors the `theme.tsx` / `usePaneWidth` persistence pattern already used twice in this codebase. A corrupt or unparseable stored value falls back to an empty list rather than throwing.
- **Scoping** — typing a prefix (`>` agents, `#` notes, `@` chats) filters to that kind, with the active scope shown as a badge in the input row. `Backspace` on an empty input clears the scope. The prefixes are listed in the `?` overlay.

Recents store the id, kind, label, and URL — never note content, which would put user data in `localStorage` for no benefit.

## 8. Testing

- **Spec panel:** the state endpoint returns `pending_agent_md`/`pending_tools` after a build and empty before one (Go); the panel renders the brief, lists tool files, shows a plain-language schedule, and empty-states before a build (frontend).
- **Templates:** picking a template fills the description; the text is editable afterward; "Start from scratch" leaves it blank.
- **Keyboard:** ⌘1–7 navigate; `j`/`k` move the highlight and `Enter` opens; `?` opens the overlay; **and the suppression test — `j` typed into an input inserts a `j` and does not navigate.** That last one is the test that matters.
- **Palette:** recents appear when the input is empty and persist across a remount; a corrupt stored value falls back cleanly; each scope prefix filters correctly; `Backspace` clears the scope.

Every behaviour must be pinned by a test that fails without its implementation. The frontend suite (456 tests at SP8's merge) and the Go suite are the gates.

## 9. Risks

| Risk | Mitigation |
|---|---|
| Single-key shortcuts fire while typing | Suppressed on input/textarea/contenteditable/palette targets; pinned by test |
| ⌘1–7 collides with browser tab-switching | Browsers reserve ⌘1–9 for tabs on some platforms; verify on the operator's Firefox/Chrome and fall back to a non-conflicting modifier if it is swallowed |
| Spec panel leaks half-built state | Fields are empty until a build completes; the panel empty-states rather than showing a partial artifact |
| Templates read as technical | Written in the designer's non-technical register; no mention of scripts, cron, or files |
| Recents grow unbounded or store user content | Capped at 8; stores id/kind/label/URL only |
| `j`/`k` fight the KB tree's own navigation | The tree is a list like any other; if its existing interaction conflicts, the tree keeps its behaviour and is excluded |
