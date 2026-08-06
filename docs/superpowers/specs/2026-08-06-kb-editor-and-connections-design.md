# KB editor formatting, AI selection actions, and connections-page cleanup

**Date:** 2026-08-06
**Status:** Approved design

Six requested changes across three subsystems: the knowledge-base rich text editor,
a new AI-assist endpoint driven from a text selection, and two misleading elements on
the connections page.

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
And it is **redundant** — per-provider preflight (`Preflight []apiPreflightProblem`,
`web/api_services.go:277`) already reports the specific problem on the specific card, at
the moment the user tries to connect, which is where it is actionable.

Remove the banner. `summary` then has no consumer in the SPA, so
`apiPublicURLSummary` and its `Summary` field come out of `web/api_services.go` too,
along with the summary assertions in `web/api_services_preflight_test.go`. Per-provider
`preflight` is untouched — it is the mechanism that makes removing the banner safe.

---

## Build order

1. **List CSS** — smallest change, fixes the visible bug, no dependencies.
2. **Connections cleanup** — independent of the editor, no shared risk.
3. **Formatting constructs** — underline → colours → callout → toggle, each landing
   with its fidelity round-trip test. Verify `tiptap-markdown`'s `parse.setup` hook
   before starting the callout.
4. **Image resize** — needs the NodeView, independent of everything above.
5. **AI assist** — Go endpoint and prompt first, then the panel.

Steps 3–5 each touch `editor.ts`'s extension list or `BubbleToolbar.tsx`, so they land
in sequence rather than in parallel.

## Out of scope

- A free colour picker. The palette is fixed by request.
- Streaming the AI response. Selections are short; a plain request avoids an SSE
  endpoint, a cancel path and partial-render handling for no user-visible gain.
- Any AI action that writes without an explicit Accept.
- Persisting an Explain answer into the note.
- Height-based or percentage image resize. Width only, aspect preserved.
