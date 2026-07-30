# UI overhaul, spec 2 of 3: layout and space

**Date:** 2026-07-30
**Status:** approved
**Depends on:** spec 1 (design system foundation) — consumes `entityIcons.ts`,
the `Card` primitive, the type scale and the button contract.

## Problem

- **Content is squeezed while the sides sit empty.** Four independent hardcoded
  widths, all centred: `SettingsPage.tsx:399` (`max-w-3xl`, 768px),
  `ConnectionsPage.tsx:498` (`max-w-5xl`), `FolderPage.tsx:40` (`max-w-3xl`),
  `NoteEditor.tsx:796` (`max-w-3xl`). On a 1920px display that wastes ~900px.
- **Page titles are 16 different things.** `text-xl font-bold`,
  `text-lg font-bold`, `text-2xl font-semibold` — and none carries an icon.
- **The side sheet is too narrow, and its width lives in two places.**
  `sheet.tsx:65` defaults to `sm:max-w-sm` (384px); `AppShell.tsx:113` overrides
  to `sm:max-w-md` (448px). Changing one leaves the other drifted.
- **The search modal is small.** `CommandPalette.tsx:188` is `max-w-xl` (576px)
  at `top-[20%]`.
- **The KB editor has huge side margins.** `NoteEditor.tsx:796` is
  `mx-auto max-w-3xl px-6` — on a wide screen that's a ~25-30% gutter per side.
- **The Owner area is cluttered.** Settings is one page with a 7-item pane nav,
  and "Owner" is a *single* entry that stacks five sub-sections inside it
  (Workspaces, Instance URL, System status, Backup, Audit log). There is no
  signal for which one you are looking at.
- **The homepage is half empty** — a greeting, three stat tiles and three short
  cards.

## Page container

One shared primitive replaces all four hardcoded widths:

```
components/shell/PageContainer.tsx
  w-full max-w-[1600px] mx-auto px-8 py-6
```

`mx-auto` only takes effect once the 1600px cap is reached, so a 1440px viewport
is genuinely fluid and a 2560px one doesn't grow 200-character line lengths.

**Forms inside still cap their own field column at ~640px** (`max-w-2xl`). A
1500px-wide text input is worse than a cramped one — the fluid container is for
*layout*, not for stretching every control.

Reading surfaces opt out and take a percentage gutter instead (see "KB editor").

## Page title

```
components/shell/PageTitle.tsx
  icon (size-5, from spec 1's entityIcons) + text-xl font-bold + actions slot
```

Replaces the 16 divergent `<h1>` elements. The icon comes from the **same** map
the rail uses, so a page and its rail entry can never show different icons.

## Side sheet: one third of the page

```
w-[clamp(400px,33vw,720px)]
```

Applied in **both** places or they drift again: `sheet.tsx:65` (the `side="right"`
default) and `AppShell.tsx:113` (the slide-over override). The 400px floor keeps
it usable on a small laptop; the 720px ceiling stops it becoming a second page
on an ultrawide.

`ChatWindow`'s `compact` mode keeps its own narrower treatment — it is embedded,
not a page-level sheet.

`AppShell.tsx`'s `p-0 gap-0` on the content well is preserved: panel content owns
its inner padding, and a shell-level `p-4` would double up chrome for a
full-height embed like the global chat panel.

## Search modal

`CommandPalette.tsx:188`:

- `max-w-xl` → `max-w-3xl` (576px → 768px)
- `top-[20%]` → `top-[12%]`
- results list max-height raised to use the extra room

## KB editor gutters

`NoteEditor.tsx:796`: `mx-auto max-w-3xl px-6 py-8` → **`px-[7%] py-8`**. No cap,
no centring — 7% per side sits in the requested 5–10% band.

`NoteEditor.tsx:813` — the **raw markdown textarea** — goes `px-6` → `px-[7%]`
to match. Miss it and switching WYSIWYG↔raw jumps the layout horizontally.

> ⚠️ **tailwind-merge group trap.** It must be `px-[7%] py-8`, never `p-8`
> alongside `px-[7%]`. tailwind-merge treats `p` and `px` as *different* groups,
> so `cn("p-8","px-[7%]")` keeps **both** classes and leaves the winner to
> generated-stylesheet ordering. CLAUDE.md records this exact bug in
> `ChatScroll`, which uses `px-4 py-4` rather than `p-4` for precisely this
> reason. Two `px-*` classes are one group, where the last provably wins.

## Owner split: grouped nav, one page per section

`SettingsPage.tsx`'s `SECTIONS` becomes two labelled groups in the context pane:

```
WORKSPACE   Profile · Workspace · AI Providers · Coder · Master password · Appearance
OWNER       Workspaces · Instance URL · System status · Backup · Audit log
```

Eleven entries, each rendering as its own full-width page. Driven by the
**existing** `?section=` query param — no new routing mechanism, and the pane
highlight is the "where am I" signal that is missing today.

New slugs: `owner-workspaces`, `owner-instance-url`, `owner-system`,
`owner-backup`, `owner-audit`. `?section=owner` **redirects to
`owner-workspaces`** so existing links and bookmarks keep working.

`OwnerSections.tsx` is decomposed: its five internal section components become
five separately-rendered sections, and the `OwnerSections` wrapper (with its
`<h2>Owner</h2>` and stacked layout) goes away.

### Composing with the owner re-auth gate

This must not weaken the gate that landed in `e225165` / `396a9f3` / `85db98d`.
It doesn't, and the reason is worth recording:

- Each of the five owner entries renders `<OwnerGate>` around **just that one**
  section.
- `OwnerGate` probes `/api/v1/admin/overview` through react-query on key
  `["admin","overview"]`. All five entries therefore **share one cached probe** —
  mounting the gate five times across navigation costs no extra requests.
- One unlock covers all five, because **the server owns the stamp**; the
  component is the affordance, not the protection. Its own doc comment is
  explicit that there is deliberately no client-side TTL, since a timer here
  could only disagree with the server.
- `OwnerGate` gains an optional `title` prop (default "Owner settings") so the
  prompt names the area actually being opened.

The gate stays server-enforced and unchanged in substance. Backup and audit
remain behind it, as does workspace deletion.

## Homepage

All four new cards, arranged as a hierarchy rather than a heap:

```
Greeting                          [Quick actions: New agent · New note · Chat · Connect]
[ active agents ] [ recent runs ] [ connected services ]        ← stat tiles (Card)
┌────────────────────────────────┬────────────────────────────────┐
│ Recent activity  (last 8 runs) │ Next up                        │
│ Agents at a glance             │ Needs attention                │
│                                │ Reminders                      │
│                                │ Recently edited notes          │
└────────────────────────────────┴────────────────────────────────┘
```

Left column carries the dense, scannable content; right column short status
cards. Collapses to one column below `lg`.

| Card | Data source | New endpoint? |
|---|---|---|
| Recent activity | `dash.recent_runs` — already fetched, today **only its failures render** | no |
| Quick actions | static links | no |
| Agents at a glance | `useAgents()` + `dash.upcoming` | no |
| Recently edited notes | `useRecentFiles` (existing client-side store) | no |

**No new API endpoints.** Every card is built from data already on the page or
already fetched elsewhere in the SPA.

`Recent activity` renders success runs too, not just failures — a status dot
(`ok`/`warn`/`danger` from spec 1's tokens), agent name, trigger and a relative
timestamp, each row deep-linking to the agent. `Needs attention` keeps its
failure-only framing, since that is its job.

`Agents at a glance` is a compact table: status chip, schedule, next run, last
outcome. It supersedes the information in `NextUpCard` but not the card itself —
`Next up` stays as the short "what fires soonest" summary.

`Recently edited notes` reads the existing `useRecentFiles` store and links back
into the editor, so Home becomes a starting point for knowledge work rather than
only an agent dashboard.

## Testing

- `PageContainer` / `PageTitle` render tests; a test asserting no page still
  uses a hardcoded `max-w-3xl`/`max-w-5xl` page wrapper.
- Sheet width: one test asserting the clamp class is present, and that
  `sheet.tsx` and `AppShell.tsx` agree (the drift being fixed).
- `NoteEditor`: both the WYSIWYG container and the raw textarea carry
  `px-[7%]`, and **neither** carries a `p-*` shorthand (the tailwind-merge trap).
- Settings nav: 11 entries in two groups; `?section=owner` redirects to
  `owner-workspaces`; each owner slug renders inside `OwnerGate`; a gated probe
  renders the prompt instead of the section body.
- Homepage: each new card renders; `Recent activity` shows successful runs (not
  only failures); quick actions link to the right routes; the grid collapses to
  one column below `lg`.
- `web/api_parity_test.go` must stay green — no route changes are intended, and
  that test is the gate proving it.
- `make ci` green.

## Risks

- **Owner decomposition touches gated code.** Mitigation: the gate is
  server-enforced; the change is purely which component tree the gate wraps.
  Tests assert every owner slug is gated, so a missed wrap fails the build
  rather than silently exposing an install-level section.
- **Fluid width can make wide tables awkward.** `Agents at a glance` gets its
  own `overflow-x-auto`, per the existing convention that wide content scrolls
  inside its own container rather than the page.
</content>
</invoke>
