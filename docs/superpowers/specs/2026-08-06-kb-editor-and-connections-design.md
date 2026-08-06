# KB editor formatting, AI selection actions, and connections-page cleanup

**Date:** 2026-08-06
**Status:** Approved design

Seven requested changes across three subsystems: the knowledge-base rich text editor,
a new AI-assist endpoint driven from a text selection, and two misleading elements on
the connections page — plus a scroll-containment bug in the app shell that lets a long
note scroll the icon rail and file tree out of view.

---

## 1. List rendering — the "/" slash-menu bug

### Diagnosis

Bulleted and numbered lists are reported as not working from the slash menu. The
commands are correct. `slashItems.ts` runs `toggleBulletList()` / `toggleOrderedList()`,
the ProseMirror document gains a real `bulletList`/`orderedList` node, and
`tiptap-markdown` serializes it to correct markdown on save.

The defect is CSS. `web/ui/src/pages/kb/editor.css` styles headings, inline code, code
blocks, blockquotes, task lists, tables, images and links — but has no rule for `ul`,
`ol` or `li`. Tailwind v4's Preflight resets those to `list-style: none; margin: 0;
padding: 0`. So a list renders as flat, unmarked, unindented lines and is
indistinguishable from consecutive paragraphs.

Every other markdown-rendering surface in the app re-adds the styling explicitly —
`components/chat/Bubbles.tsx`, `components/designer/SpecPanel.tsx`,
`pages/skills/SkillView.tsx` all carry `[&_ul]:list-disc [&_ul]:pl-5` and the `ol`
equivalent. The editor is the one surface that never did. The bubble toolbar's
"Bullet list" button has the same symptom for the same reason.

### Fix

Add `ul` / `ol` / `li` rules to `editor.css`:

- `ul` → `list-style: disc`, `ol` → `list-style: decimal`, both `padding-left: 1.5em`
- nested `ul ul` → `circle`, `ol ol` → `lower-alpha`
- `li > p` margin collapsed so a list item is not double-spaced against
  `.tiptap > * + *`'s `margin-top`

The existing `ul[data-type="taskList"] { list-style: none; padding-left: 0 }` rule must
continue to win. It is already more specific (attribute selector), so ordering alone is
not relied on — but a test asserts task lists stay unmarkered.

### Test

`editor.css` is a stylesheet, and jsdom has no layout engine, so a behavioural test is
not possible in vitest. A `liststyles.test.ts` reads the stylesheet source and asserts
it declares `list-style` for both `ul` and `ol` under `.tiptap`, mirroring the existing
`density.test.ts` precedent of failing the build on a stylesheet property rather than a
rendered result. This catches the regression class — a future Preflight or reset change
silently removing markers again.

---

## 2. New formatting constructs

Five constructs. Each is defined by its **markdown representation first**, because
`checkFidelity()` (`pages/kb/editor.ts`) decides whether a note opens editable or as a
read-only rich view: a construct that does not survive markdown → doc → markdown
unchanged would push every note containing it into read-only. Each construct ships with
a round-trip case in `editor.test.ts` and does not ship without one.

| Construct | Markdown on disk | Mechanism |
|---|---|---|
| Underline | `<u>text</u>` | Mark already bundled in StarterKit v3 (`editor.ts:27` notes this); needs only a toolbar control and a markdown serializer |
| Text colour | `<span style="color:#ef4444">text</span>` | New `KBTextColor` mark |
| Background colour | `<span style="background-color:#fef08a;color:#18181b">text</span>` | New `KBBgColor` mark, separate from the above so the two compose |
| Callout | `> [!note]` + body, 5 kinds | New `Callout` node + a markdown-it parse rule |
| Toggle list | `<details><summary>Title</summary>` + body + `</details>` | New `Toggle` node |

Inline HTML in a note is already an accepted property of this editor:
`Markdown.configure({ html: true })` was adopted deliberately (`editor.ts:45-55`) and
its fidelity contract is recorded there — a note with no HTML tags serializes
identically either way. These constructs extend that, they do not introduce it.

### Serialization mechanism

`tiptap-markdown` renders in one direction by walking the ProseMirror doc, and parses in
the other by running markdown-it to HTML and letting TipTap parse that HTML. So:

- **Marks** (underline, colours) need a `markdown: { serialize: { open, close } }` entry
  for the write direction, and a `parseHTML` rule for the read direction. Because
  `html: true` already passes `<span style=…>` and `<u>` through markdown-it untouched,
  the read direction needs no markdown-it work at all.
- **Toggle** is the same shape — `<details>`/`<summary>` is block HTML that markdown-it
  passes through with `html: true`, and the node's `parseHTML` claims it.
- **Callout** is the only one needing a markdown-it rule, because `> [!note]` is
  blockquote syntax that markdown-it parses into a plain blockquote containing the
  literal text `[!note]`.

### Callout: risk and fallback

The callout parse rule registers through `tiptap-markdown`'s per-extension
`parse.setup(markdownit)` hook. This API is **not yet verified against the installed
`tiptap-markdown@0.9`** and must be confirmed in the first implementation step, before
anything is built on top of it.

If the hook does not hold, the fallback is to represent a callout as block HTML —
`<div data-callout="note">…</div>` — which `html: true` already parses today and which
needs no markdown-it work. The cost is that Obsidian renders it as a plain div rather
than a native callout. The Obsidian form is preferred because the vault is explicitly an
Obsidian-style knowledge base; it is not worth blocking the feature on.

### Callout kinds

Five: `note`, `tip`, `info`, `warning`, `danger`. Each is one slash-menu item, so
typing `/warn` reaches it directly. Colours map to existing design tokens rather than
new ones — `--accent` for note and info, `--ok` for tip, `--warn` for warning,
`--danger` for danger — so the existing `contrast.test.ts` guarantees already cover
them and a palette edit cannot silently break a callout.

### Colour palette

Fixed swatch grid, no colour picker. Presented in the bubble toolbar behind a single
trigger, as two rows of eight: text colours above, background tints below, plus a
"none" control on each row that removes the mark.

**Text colours** (8), chosen to hold contrast in *both* themes against
`--background: #ffffff` (light) and `#191919` (dark):

| Name | Hex | Light | Dark |
|---|---|---|---|
| red | `#ef4444` | 3.76 | 4.67 |
| orange | `#ea580c` | 3.56 | 4.94 |
| green | `#059669` | 3.77 | 4.67 |
| teal | `#0d9488` | 3.74 | 4.70 |
| blue | `#3b82f6` | 3.68 | 4.78 |
| violet | `#8b5cf6` | 4.23 | 4.15 |
| pink | `#ec4899` | 3.53 | 4.98 |
| grey | `#71717a` | 4.83 | 3.64 |

**Floor: 3.53:1 in both themes.** This is WCAG AA for large text and for non-text
contrast, and it is short of the 4.5:1 AA body-text threshold the project's own
`contrast.test.ts` enforces on its tokens. That gap is a consequence of the chosen
storage format, and it is deliberate: a single fixed hex in the file is what makes a
coloured note render correctly in Obsidian and in HTML export, and no fixed hex can
reach 4.5:1 against both `#ffffff` and `#191919` — the best any colour achieves is
4.15 (violet-500), measured across the whole Tailwind mid-range ramp. Yellow and amber
are excluded from the text set entirely (2.9–3.2 on white) and appear only as
highlight backgrounds, where they belong.

**Background tints** (8): `#fef08a` yellow, `#fed7aa` orange, `#fecaca` red,
`#bbf7d0` green, `#99f6e4` teal, `#bfdbfe` blue, `#e9d5ff` purple, `#fbcfe8` pink.

A highlight span always carries an explicit foreground of `#18181b` alongside its
background. Without it, a pale tint on the dark theme inherits the near-white
`--foreground` and becomes white-on-yellow. With it, every tint measures **≥12.2:1** for
its text and is equally legible in both themes, because the pair is self-contained and
never inherits from the page.

`kbPalette.test.ts` computes real WCAG ratios for every entry — text colours against
both themes' `--background`, highlight foreground against each tint — and fails below
the recorded floors (3.5 and 4.5 respectively). It follows `contrast.test.ts`'s approach
of computing from values rather than trusting them, so a future palette edit cannot
quietly drop below the line.

### Surfaces

- **Bubble toolbar** gains underline and the colour trigger. Underline is an inline
  mark and belongs here, not in the slash menu.
- **Slash menu** gains six items: five callouts and Toggle list. `slashItems.ts`'s
  array order is the display contract (`filterSlashItems` never reorders), so they are
  inserted next to related block types rather than appended.
- **`SlashMenu.tsx`'s `ICONS` map** must gain an entry per new item; a missing entry
  renders the row with no icon rather than failing, so a test asserts every
  `slashItems` title has an icon.

---

## 3. Image resize

### Behaviour

Clicking an image selects it and reveals a drag handle at its bottom-right corner.
Dragging resizes width; height follows the natural aspect ratio. Width is clamped
between 80px and the editor column width and snapped to whole pixels. Natural size
remains the default and writes no width at all.

### Storage

`![alt|420](assets/foo.png)` — the Obsidian pipe-width convention, matching the vault's
Obsidian-style model. Outside Obsidian the width lands in the alt text, which is not
displayed on an image that loads, so the degradation is invisible.

`KBImage` (`pages/kb/kbImage.ts`) gains a `width` attribute. The parse direction splits
the alt text on its **last** `|` and takes the trailing segment only when it is a bare
integer, so an alt genuinely containing a pipe is not corrupted. The render direction
rejoins. `src` is untouched by all of this and keeps its existing behaviour exactly —
stored as the portable vault path, rendered through `/api/v1/kb/raw`.

An image with no width serializes byte-for-byte as it does today. This is asserted, not
assumed: an existing-note round-trip case covers it.

### Implementation note

The drag handle needs a ProseMirror NodeView, which `kbImage.ts` does not currently
have. The NodeView renders the `<img>` plus the handle and owns the pointer-move maths;
`editor.css` styles the handle and the selected-image ring. jsdom cannot exercise a
drag, so the width-clamping and aspect maths are extracted into a pure exported function
tested directly — the same tactic `placeMenu` in `SlashMenu.tsx` already uses for
exactly this reason.

---

## 4. AI actions on a selection

### Endpoint

`POST /api/v1/kb/assist`, on the workspace-scoped group (`requireOwnerAPI` +
`requireActiveWorkspaceAPI` + `requireSetupCompleteAPI`), in a new `web/api_kb_assist.go`.

```
{ "action": "improve" | "proofread" | "explain" | "reformat",
  "path":   "notes/ci.md",
  "selection": "<markdown of the selected slice>" }

→ 200 { "action": "improve", "result": "…" }
```

Behaviour:

All error responses use the existing `jsonErr(c, status, code, message)` helper, which
is the shape every other handler in `web/api_kb.go` already returns and which the SPA's
`errorMessage()` helper already understands.

- `action` is validated against the closed set; anything else is
  `jsonErr(400, "invalid_request", …)`. The set is a Go constant slice so the handler
  and its tests spell the values once.
- `selection` is capped at **16 KiB** and rejected above it rather than truncated. This
  is a small explicit constant, deliberately *not* `internal/iolimit`'s 25 MiB — that
  cap governs ingest doors (uploads, attachments, KB bridge), and reusing it here would
  admit a payload no LLM call should carry. Both an over-cap and an empty selection
  return `jsonErr(400, "invalid_request", …)` with distinct messages.
- `path` is resolved through the vault's `Resolve` safety primitive — the security
  primitive every KB read/write path uses — so a traversal cannot reach outside the
  workspace's vault even though the path is used only as prompt context. A rejected
  path is `jsonErr(400, "invalid_path", …)`, matching `web/api_kb.go:551`.
- The prompt is built by a new `prompts.BuildKBAssistPrompt(action, path, selection)` in
  `internal/prompts`. No prompt text lives outside that package — this is a standing
  repo rule and this endpoint does not get an exception.
- Execution is `s.coderForWorkspace(w.ID).WithNoTools().Generate(ctx, w.ID, prompt)` —
  the same text-only path `buildLLMTimeParser` (`web/handlers_misc.go:194`) already
  uses. No tools, no vault access, no subprocess for the API engine.
- `coder.ErrUsageLimit`, `ErrRateLimited` and `ErrAPIAuth` map to the same user-facing
  wording agent runs already produce, returned as
  `jsonErr(503, "coder_unavailable", <friendly sentence>)`. A workspace out of quota
  gets a sentence, not a stack trace. Any other coder failure is
  `jsonErr(500, "internal", …)`.

  That wording currently lives in `agentrunner.friendlyRunError`
  (`internal/agentrunner/runner.go:690`), which is **unexported**. Rather than
  duplicating the three messages, export it as `agentrunner.FriendlyRunError` and call
  it from the handler. `web` already imports `agentrunner` (`web/server.go:13`), so
  this forms no new dependency and no import cycle. Duplicating instead would mean a
  workspace out of quota gets one sentence from a scheduled run and a different one
  from the editor.

The route is added to the `want` table in `web/api_parity_test.go`, which is the merge
gate asserting the registered route set matches the planned one.

### Frontend

`BubbleToolbar.tsx` gains a second row: **Improve · Proofread · Explain · Reformat ·
Edit with AI**.

Clicking one of the first four swaps the toolbar's contents for a result panel in the
same floating container — a spinner while the request is in flight, then the returned
text, then `Discard` / `Accept`. Accept replaces the selection; Discard closes the panel
and leaves the note untouched. **Explain** renders the identical panel with `Copy`
instead of `Accept`, so all four read as one family and the one non-rewrite action
cannot accidentally modify the note.

The panel is a new `pages/kb/AIActions.tsx`; the request is a react-query mutation in
`lib/kbAssist.ts`. `BubbleToolbar.tsx` is already at the size where adding five controls
plus a result state would make it do too much, so the panel is a sibling component and
the toolbar only decides which of the two to render.

**What travels.** The selection is sent as the **markdown of the selected slice**
(`editor.storage.markdown.serializer.serialize(slice.content)`), not plain text, so
Reformat can see structure. An accepted result is parsed back through
`tiptap-markdown`'s parser before insertion, so a returned list becomes a real list
rather than literal `- ` characters.

**Selection survival.** The existing toolbar buttons already use `onMouseDown` with
`preventDefault` because a plain click steals focus and collapses the selection
(`BubbleToolbar.tsx:36-38`). The AI panel has a harder version of the same problem: the
selection must survive an async round trip and a re-render. The selection range is
therefore captured as `{from, to}` at click time and the accept path applies to that
stored range, never to whatever the live selection happens to be when the response
lands.

### Edit with AI

Opens the same slide-over `ChatAboutFileButton` already uses — `GlobalChatPanel` with
`forceNew` — prefilled with a prompt naming the file path *and* quoting the selection.

`ChatAboutFileButton.tsx`'s exported `chatPrompt(path)` gains a sibling
`selectionChatPrompt(path, selection)`. Both stay in that file and both stay exported:
the exact wording is the contract between the button and the coder's ability to resolve
the path, which is why the existing one is exported for direct unit testing.

The selection is quoted into the prompt rather than the whole file, for the reason
already recorded in that file: the chat coder runs rooted at the vault with file tools
and its system prompt names the vault root, so a path is all it needs to open the file
itself. Inlining the file would blow context and hand the model a snapshot that goes
stale the moment either side edits.

### Tests

Go (`web/api_kb_assist_test.go`): action validation, the selection cap boundary
(16 KiB passes, 16 KiB + 1 rejects), empty selection, path traversal rejection, and
coder-error mapping for each of the three sentinel errors.
Vitest: the panel's three states (loading, result, error), Accept applying to the
*stored* range rather than the live selection, Discard leaving the doc unchanged,
Explain rendering no Accept control, and both prompt builders' exact wording.

---

## 5. Connections page

### Primary chat-app picker

`ConnectionsPage.tsx:596` renders the "Where should agent runs and reminders go?"
heading with a radio per linked app whenever `linkedApps.length > 0`. With a single
linked app this is a one-option radio group asking a question that has no alternative
answer.

Gate the block on `linkedApps.length > 1`. With exactly one linked app, render the
existing "Delivered to X" sentence alone, without the heading or the radio — the
information is useful; the choice is not. With zero linked apps, render nothing, which
is already the behaviour.

The defensive `?? linkedApps[0].label` fallback in that block stays. Its reasoning is
recorded in place (`:616-626`) and is unrelated to this change: the two reads behind the
list endpoint are not transactional, so a linked set can briefly return with no primary,
and the SPA has no ErrorBoundary.

### Instance-URL summary banner

`ConnectionsPage.tsx:643-658` renders "`<base_url>` works with N of M sign-in services.
A public domain name over https unlocks the rest."

Two problems. It is **misleading** — M counts only OAuth-kind providers, around 34 of
the 91 registered services (`internal/connectors/providers/` holds 91 YAML files, of
which ~35 are neither `api_key` nor `none`). Presented without that qualifier it reads
as though the whole catalogue is 34 services and most of it is unavailable, when in
fact the majority are API-key or keyless and entirely unaffected by the instance URL.
And it states a global tally when the useful information is per-service.

Remove the banner. `summary` then has no consumer in the SPA, so
`apiPublicURLSummary` and its `Summary` field come out of `web/api_services.go` too,
along with the summary assertions in `web/api_services_preflight_test.go`.

### Disable the cards that cannot work

The per-provider check already exists and is already computed for every card:
`Preflight []apiPreflightProblem` (`web/api_services.go:277`) ships on every provider in
the list payload. Today the SPA ignores it on the card and only surfaces it *inside* the
wizard — `ServiceWizard.tsx:201` computes `hardBlocked` and `:564` disables the Connect
button. So the user learns a service cannot work only after picking it.

Use the data that is already there. `ServiceTile`
(`ConnectionsPage.tsx:173`) gains the same derivation:

```ts
const blocked = provider.preflight.some((p) => p.severity === "hard");
```

Severity is the existing two-value model (`internal/publicurl/policy.go:26`).
`SeverityHard` is only ever produced for a provably fatal condition — a raw IP, an
RFC-reserved host suffix, or plain `http` on a public domain — and only a provider
policy marked `verified: true` can produce one at all. `SeveritySoft` (including
`unverified_host`) must **not** disable anything; it stays a warning inside the wizard
exactly as today.

A blocked tile renders dimmed with its status line replaced by "Needs a public URL"
in `text-warn`, and carries `aria-disabled` rather than the `disabled` attribute — a
`disabled` button fires no click, and the click is the whole point.

**One exception: a provider with existing connections is never disabled.** Those
connections still work and the user must be able to reach the wizard to inspect or
delete them. A blocked provider with `connections.length > 0` keeps its normal tile and
its account count; only the Connect action inside the wizard stays disabled, which is
already the behaviour.

### The click

Clicking a blocked tile opens a small dialog instead of the full wizard, carrying the
first hard problem's `message` and `fix` verbatim from the API — the same strings the
wizard already renders, so there is one wording, not two. It offers:

- **Change the instance URL** (primary) → `/settings`, the actual remedy.
- **Open anyway** (ghost) → opens the normal `ServiceWizard`, where Connect is disabled
  and the full preflight list is shown.

"Open anyway" is not optional politeness. The hard block is **UI-only by design** — it
predicts a third party's rules rather than expressing an invariant we own, so a stale
`redirect_policy` entry in a YAML file must never become a lockout with no override.
That reasoning is already recorded for the server side; disabling the card is a stronger
gate than disabling a button, so the escape hatch has to be explicit. It also means the
change stays purely presentational: no endpoint, no server-side gate, no schema change.

### Tests

Vitest against `ConnectionsPage`: a provider with a hard problem and no connections
renders blocked and opens the dialog rather than the wizard; the same provider *with* a
connection renders normally; a provider with only a soft problem renders normally; and
"Open anyway" reaches the wizard. A Go test asserts `preflight` is populated on the list
payload for a hard-blocked provider, since the SPA behaviour now depends on that field
being present rather than merely informative.

---

## 6. Editor scroll containment

### Symptom

With a note longer than the viewport, scrolling moves the whole page: the icon rail and
the file-tree pane scroll out of view instead of staying fixed while only the editor
pane scrolls.

### What is already in place

This exact class of bug was fixed once and is guarded. `AppShell.tsx:84` is
`h-screen overflow-hidden flex flex-col md:flex-row`, `NoteEditor.tsx:839` is
`min-h-0 flex-1 overflow-y-auto overscroll-contain`, and `scrollcontainment.test.tsx`
asserts both declaration strings still exist. Both are present and both tests pass, so
this is **not** a regression of that fix — it is a case that fix does not cover.

### Diagnosis

The shell is a row of three flex items — `IconRail`, the context-pane `aside`, and
`main` — inside a `h-screen overflow-hidden` container. On a wide viewport `main` is
`flex-1 min-w-0 overflow-y-auto`, its height comes from `align-items: stretch` (so
`NoteEditor`'s `h-full` resolves against a definite 100vh), and the note's own pane is
the only thing that scrolls. That path is correct.

Below the `md` breakpoint the container flips to `flex-col`, and the same three items
become a **column**. Two things then change at once:

1. `main` carries `min-w-0` but **not `min-h-0`**. In a column flex container a flex
   item's automatic minimum size is content-based (CSS Flexbox §4.5), so `min-height:
   auto` lets `main` grow to its content instead of holding its flex share. A long note
   therefore makes `main` taller than the viewport.
2. `overflow: hidden` does not prevent scrolling — it creates a scroll container and
   only removes the scrollbar. So the oversized `main` makes the `h-screen` shell
   itself scrollable, and scrolling it carries `IconRail` and the context pane with it.

That is exactly the reported symptom, and it reaches a normal desktop user through
browser zoom: zooming in shrinks the effective viewport width, and past roughly 768 CSS
pixels the column layout engages. The original fix was verified at a single desktop
viewport, which is why it did not surface.

This is the **leading hypothesis, not a confirmed reproduction** — it is derived from
the source, and `scripts/verify-kb-layout.py` is the only thing that can observe the
behaviour. Confirming it at a narrow viewport is the first implementation step; if the
measurement disagrees, the fix is re-derived from what it shows rather than from this.

### Fix

Add `min-h-0` to `main` in `AppShell.tsx`, so it holds its flex share in the column
layout and its own `overflow-y-auto` absorbs the excess instead of the shell doing it.
The rule generalises: **every flex item between the `h-screen` root and a scrolling pane
must carry `min-h-0`**, or the automatic minimum size defeats the containment. The KB
column chain already follows this (`KBPage.tsx:279`, `:285`, `NoteEditor.tsx:839`);
`main` is the one link that does not.

The fix belongs in `AppShell`, not in `NoteEditor`. `main` wraps every route, so a long
page on any other surface has the same latent bug — fixing it at the shell fixes all of
them, and matches the recorded design intent that the shell is a fixed-height frame in
which every scrolling region is explicit.

### Tests

`scrollcontainment.test.tsx` gains a declaration assertion for `main`'s `min-h-0`,
matching how the two existing cases are guarded — jsdom has no layout engine, so a
stylesheet-level assertion is the most a vitest suite can do here.

`scripts/verify-kb-layout.py` gains a **narrow-viewport pass**: it currently drives one
desktop viewport, and the whole point of this bug is that the desktop pass is clean. The
new pass loads a long note at a sub-`md` width, scrolls to the bottom of the editor
pane, wheels again, and asserts both `documentElement.scrollTop` and the icon rail's
`top` are unchanged — the same two measurements the existing check already records for
the desktop case. Running it needs a throwaway instance and a session cookie, so it is
an explicit manual verification step in the plan, not a CI job.

## Build order

1. **List CSS** — smallest change, fixes the visible bug, no dependencies.
2. **Scroll containment** — reproduce at a narrow viewport first, then the one-class
   fix in `AppShell`. Ordered early because it touches the shell every later step
   renders inside, and because a wrong diagnosis is cheaper to discover now.
3. **Connections** — remove the banner, then block the tiles. Independent of the
   editor, no shared risk.
4. **Formatting constructs** — underline → colours → callout → toggle, each landing
   with its fidelity round-trip test. Verify `tiptap-markdown`'s `parse.setup` hook
   before starting the callout.
5. **Image resize** — needs the NodeView, independent of everything above.
6. **AI assist** — Go endpoint and prompt first, then the panel.

Steps 4–6 each touch `editor.ts`'s extension list or `BubbleToolbar.tsx`, so they land
in sequence rather than in parallel.

## Out of scope

- A free colour picker. The palette is fixed by request.
- Streaming the AI response. Selections are short; a plain request avoids an SSE
  endpoint, a cancel path and partial-render handling for no user-visible gain.
- Any AI action that writes without an explicit Accept.
- Persisting an Explain answer into the note.
- Height-based or percentage image resize. Width only, aspect preserved.
- Any broader rework of the sub-`md` layout. Section 6 fixes the containment leak that
  makes a long note scroll the shell; how the rail and context pane are best presented
  on a genuinely small screen is a separate question.
- A server-side gate on a hard-blocked provider. The block stays UI-only and
  overridable, for the reason recorded in section 5.
