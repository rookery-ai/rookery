# UI overhaul, spec 1 of 3: design system foundation

**Date:** 2026-07-30
**Status:** approved
**Depends on:** nothing
**Consumed by:** spec 2 (layout & space), spec 3 (gaps & fixes)

## Problem

The SPA reads as small, flat and inconsistent. Concretely, and all verified in
the source rather than asserted:

- **Two icon vocabularies.** `SettingsPage.tsx:31-37` uses emoji *strings*
  (`👤 🏠 🧠 ⚙️ 🔐 🌓 🛡`) for its section nav; the KB tree, connections bar and
  command palette use monochrome lucide. That is the whole "settings is
  coloured, everything else is grayish" complaint.
- **Everything is small.** Body is 14px; `text-xs` (12px) appears **212** times
  and `text-sm` (14px) **193** times. On top of that, **39** call sites hardcode
  `text-[10px]`/`text-[11px]`.
- **Click targets are under-sized.** A KB tree row is `px-1.5 py-1` ≈ 26px tall;
  its `⋯` trigger is `p-0.5` ≈ 18px and is *not mounted* until hover.
- **Buttons drift.** Sizes run `h-6`/`h-8`/`h-9`/`h-10`; some carry icons, most
  don't; no rule says which variant means what.
- **Card outlines are hairline and tight.** 23 hand-rolled
  `rounded-lg border border-border p-3` blocks, no shared primitive.

Two symptoms turn out to be *invisibility*, not absence, which is the strongest
argument for this spec existing at all:

- "Inbox has no mark-all-as-read" — `HomePage.tsx:234` renders one, as
  `variant="ghost" size="xs"`: a 24px-tall, 12px grey **text** button in a
  narrow pane, only when `unread > 0`. The endpoint, hook and tests all exist.
- "The KB delete has no confirmation" — `FileTree.tsx:377` has a
  "This can't be undone" dialog already wired.

## Non-goals

- A vendored monospace family. Not requested; another ~100 KB in the binary for
  code blocks alone.
- `border-2` card outlines (see "Borders" for why darkening beats thickening).
- Any layout, page-structure or feature change — those are specs 2 and 3.

## Font: Inter Variable, vendored once

Inter Variable, latin subset, ~100 KB woff2. Chosen for a tall x-height and open
apertures — the properties that make 13px legible, which is the actual
complaint. It must be **self-hosted**: the SPA is embedded in a single binary
shipped for offline/LAN installs, so a Google Fonts `@import` both fails there
and adds an external request.

### One file, two consumers

The woff2 lives at exactly one path:

```
internal/fonts/InterVariable.woff2
internal/fonts/fonts.go        // //go:embed InterVariable.woff2
```

`internal/fonts` exists as its own package because **`go:embed` cannot reach
outside its own package directory**, and two independent consumers need the same
bytes:

1. **Go** — `internal/export` embeds it for the HTML/PDF export path.
2. **Vite** — the SPA imports it through an `@fonts` alias added to
   `vite.config.ts` (`resolve.alias`), pointing at `../../internal/fonts`.

A checked-in duplicate is rejected: the two copies would drift and nothing would
notice. If the Vite alias proves unworkable during implementation, the fallback
is a duplicate **plus** a Go test asserting the two files are byte-identical —
never a silent duplicate.

### Three enforcement points

The request is that the font reach the knowledge base and the converted
documents, so it is declared in three places, not one:

1. **`web/ui/src/index.css`** — `@font-face` (`font-weight: 100 900`,
   `font-display: swap`) plus `--font-sans` in `@theme inline`, so Tailwind's
   `font-sans` utility and the `body` rule both resolve to Inter. The existing
   `body { font-family: -apple-system, … }` override is replaced.
2. **`web/ui/src/pages/kb/editor.css`** — `.tiptap` inherits from `body` today,
   so this is a verification plus an *explicit* `font-family` declaration on
   `.note-editor-content .tiptap`, so a future change to body styling cannot
   silently drop the KB editor back to a system font.
3. **`internal/export/html.go:18`** — currently
   `-apple-system, BlinkMacSystemFont, "Segoe UI", …`. Replaced by an
   `@font-face` whose `src` is the woff2 **base64-inlined as a `data:` URI**.

### Why the export embeds the font rather than naming it

Two reasons, both load-bearing:

- An exported HTML file stays **self-contained offline** — the same property the
  export path already buys for images.
- `ToPDF` shells out to a headless renderer (weasyprint/chromium/wkhtmltopdf/
  libreoffice/pandoc). A named font would have to be **installed on the server**;
  it won't be, so PDF export would silently fall back to a system font while
  appearing to succeed. An inlined `data:` URI makes the PDF correct with no
  host dependency.

Cost: ~135 KB of base64 per exported HTML/PDF. This is consistent with the
existing precedent — `api_kb.go:963` documents that base64's ~33% inflation is
an accepted cost for self-contained exports of vault images.

### Stated limitations

- **DOCX can only name the font.** `internal/export/docx.go` sets the run
  font to `Inter` with a fallback; Word substitutes if the reader hasn't
  installed it. Embedding fonts in the OOXML package is out of scope. This is
  written into the spec rather than left as a surprise.
- **Mono stays the system stack** (`ui-monospace, SFMono-Regular, Menlo, …`) in
  both the SPA and the export CSS.

## Type scale

Tailwind v4 resolves `text-*` utilities from `--text-*` tokens, so the scale is
remapped in **one file** and all ~405 existing `text-xs`/`text-sm` call sites
grow at once. Editing 250 call sites by hand is rejected as churn that would
also drift straight back.

Note that raising `body { font-size }` alone does **not** do this: `text-sm` is
an absolute `rem` value, so the body rule and the token remap are different
fixes and the token remap is the one that matters.

```css
@theme inline {
  --text-xs: 0.8125rem;              /* 12px → 13px */
  --text-xs--line-height: 1.25rem;
  --text-sm: 0.9375rem;              /* 14px → 15px */
  --text-sm--line-height: 1.5rem;
  --text-base: 1rem;
  --text-base--line-height: 1.625rem;
  --text-lg: 1.125rem;
  --text-lg--line-height: 1.75rem;
  --text-xl: 1.375rem;               /* 20px → 22px */
  --text-xl--line-height: 1.875rem;
  --text-2xl: 1.75rem;               /* 24px → 28px */
  --text-2xl--line-height: 2.125rem;
}
```

**Both halves of each pair must be set.** Tailwind v4 pairs every `--text-*`
with a `--text-*--line-height`; setting only the size leaves line-height pinned
to the old metric, which makes text *cramped* rather than more readable.

`body` goes from `font-size: 14px` to `15px` for the elements that carry no
`text-*` class at all.

### The 39 hardcoded sizes

All `text-[10px]` (19) and `text-[11px]` (20) uses collapse to `text-xs`. Every
one is a micro-label — uppercase section headings, stat-tile captions, the "+N
more" hint — which read correctly at 13px bold. Keeping any arbitrary px value
re-opens exactly the drift this spec closes. A test asserts no
`text-[<n>px]` remains in `web/ui/src`.

## Density and click targets

Floor: **36px for list rows, 32px for icon buttons.** WCAG 2.2 AA's target-size
minimum is 24px, so this clears it with margin without going to a touch-first
44px that would waste vertical space in a dense pane.

| Element | Now | After |
|---|---|---|
| KB tree row (`FileTree.tsx:603`) | `px-1.5 py-1 gap-1.5` ≈ 26px | `px-2 py-2 gap-2` ≈ 36px |
| Tree row icon / chevron | `size-3.5` | `size-4` |
| Tree `⋯` trigger (`FileTree.tsx:625`) | `p-0.5` ≈ 18px | `size-7` (28px) |
| Button `default` | `h-9` | `h-10` |
| Button `sm` | `h-8` | `h-9` |
| Button `xs` | `h-6` | `h-7` |
| Button `icon` / `icon-sm` / `icon-xs` | `size-9` / `size-8` / `size-6` | `size-10` / `size-9` / `size-7` |
| Emoji grid cell (`EmojiPicker.tsx`) | `h-8 w-8` | `size-9` |
| `ContextSection` heading | `text-[11px]` | `text-xs` |

The `⋯` trigger keeps `opacity-0 group-hover:opacity-100 focus-visible:opacity-100`
but becomes **always mounted**. It is not today, and mounting a node under the
cursor is the precise bug CLAUDE.md already records for the chat `MessageMeta`
footer (it cancels an in-progress drag-select). Reveal by opacity, never by
mount.

## Borders and cards

Two moves — darken the token, enlarge the box:

```
--border         light  #e9e7e3 → #dcd8d2      dark  #333330 → #3f3f3b
--border-strong  light  #c9c4bc                dark  #4d4d48   (new)
```

`--border-strong` is for dividers and emphasised outlines; nothing in this spec
is required to use it, it exists so spec 2 and any follow-up have a defined
step up rather than inventing one.

A **`Card`** primitive (`components/ui/card.tsx`) replaces the 23 hand-rolled
`rounded-lg border border-border p-3` blocks — the skills list, secrets list,
homepage stat tiles, "Next up", "Needs attention", "Reminders":

```
rounded-xl border border-border bg-background p-4
```

### Why not `border-2`

The request was "bolder and bigger a bit". A 2px hairline across 23 cards reads
as heavy and noisy rather than crisp; darkening the token raises contrast
against both `--background` and `--chrome` while more padding and a larger
radius supply the "bigger". If the running app still reads thin, switching cards
to `border-strong` is a one-line follow-up — which is why that token is being
defined now.

### Contrast is a gate, not a nicety

`index.css` already carries explicit comments that `--ok`/`--warn`/`--danger`
were darkened because a review measured `--ok` at **3.68:1** against its own
`-soft` fill. Any token this spec changes is re-verified at **4.5:1 against
three backgrounds** — `--background`, `--chrome`, and its own `-soft` fill where
one exists — in **both** themes, with the computed ratios written into the
implementation PR. Borders are decorative and fall under the 3:1 non-text
requirement; the new values are checked against that.

The implementation adds a small contrast helper test so the ratios are asserted,
not eyeballed once and then trusted forever.

## Icon system

A new **`web/ui/src/lib/entityIcons.ts`** maps every route and entity kind to
exactly one lucide icon:

```
home Home · agents Bot · skills Sparkles · secrets KeyRound · kb BookOpen
connections Plug · chats MessagesSquare · settings Settings · owner Shield
inbox Inbox · reminders Bell · note FileText · folder Folder
owner-workspaces Building2 · owner-instance-url Link2 · owner-system Activity
owner-backup HardDriveDownload · owner-audit ScrollText
```

Four consumers, so the rail and a page title can never disagree:

1. `components/shell/IconRail.tsx`
2. the `PageTitle` primitive (spec 2)
3. `components/search/CommandPalette.tsx`'s `KIND_META`
4. `SettingsPage.tsx`'s section nav — **the seven emoji strings are deleted**

### Rules

- lucide only.
- `size-4` inline, `size-5` for page titles.
- `strokeWidth` 2 (lucide default), never overridden per-site.
- **`currentColor` always.** No coloured icon except semantic status, which uses
  `text-danger` / `text-warn` / `text-ok`.
- **One documented exception:** `components/brand/ProviderLogo.tsx` keeps full
  brand colour. A monochrome Slack or Google mark is harder to recognise than a
  coloured one, which defeats the purpose of a logo.

## Button contract

Four variants, each with one job:

| Variant | Job |
|---|---|
| `default` | the primary action on a surface |
| `outline` | secondary action |
| `ghost` | tertiary / inline / toolbar action |
| `destructive` | removes data |

`secondary` and `link` remain in `buttonVariants` for shadcn compatibility but
are not part of the contract; `link` is for inline text links only.

**Every action button carries a leading lucide icon.**

### The carve-out, stated explicitly

Two exceptions, because the blanket rule produces worse UI:

- **Dialog footer button pairs** (`Cancel`/`Save`, `Cancel`/`Delete`) stay
  text-only. An icon on "Cancel" is noise, and the two buttons read as a
  matched pair.
- **The `link` variant** stays text-only.

This is written down rather than applied blindly or dropped silently.

## Testing

- `styles.test.ts` (extended): every `--text-*` token has a matching
  `--text-*--line-height`; `@font-face` references `InterVariable.woff2`;
  `--font-sans` is defined; the reduced-motion rule still exists.
- New: no `text-[<n>px]` literal remains under `web/ui/src`.
- New: `entityIcons.ts` exports an icon for every rail route and every
  `CommandPalette` kind (a missing key is a compile error via a
  `Record<Route, LucideIcon>` type, and a test covers the palette kinds).
- New: `SettingsPage`'s section list contains no emoji (regression guard for
  the exact drift being fixed).
- New contrast test: computed WCAG ratios for every changed token against
  `--background`, `--chrome` and its `-soft` fill, both themes.
- Go: `internal/export` — exported HTML contains an `@font-face` with a
  `data:font/woff2;base64,` src, and the embedded font is non-empty.
- `internal/fonts`: the embedded file is non-empty and is a woff2 (magic
  bytes `wOF2`).
- Existing suites must stay green: `make ci` (gofmt, vet, `-race`,
  cross-compile, `tsc -b`, oxlint, vitest, vite build).

## Risks

- **Everything grows at once.** A global type-scale change can overflow narrow
  fixed-width containers. Mitigation: the context pane is user-resizable with a
  200px floor, and the implementation walks all five panes plus the mobile
  breakpoint after the remap rather than trusting it.
- **Font swap shifts layout.** `font-display: swap` is chosen anyway: a brief
  reflow beats invisible text on a slow LAN load, and the woff2 is served from
  the same binary so the window is small.
- **Export size grows ~135 KB per file.** Accepted, with precedent.
</content>
</invoke>
