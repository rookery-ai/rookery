# The Rookery mark in the product — design

**Date:** 2026-08-10
**Unit:** A of the [onboarding, brand and platform batch](2026-08-10-onboarding-brand-and-platform-batch-design.md)

Three related requests: make the logo the default workspace image, offer it in
several colours, re-tune the existing presets for the current brand, and put the
mark on the two screens that sit outside the app shell.

They share a prerequisite that did not exist. **The product had no Rookery mark
at all** — only `web/ui/public/favicon.svg`, which a browser tab reads and no
component can. Everything else here is built on top of creating one.

## The mark is a component, not a file

`components/brand/RookeryMark.tsx` draws the glyph inline. That is not a style
preference: **an `<img>` cannot inherit `currentColor`**, and drawing it inline is
what lets one definition sit in a light header, a dark rail and a coloured tile
without three copies. Unit B of this batch exists precisely because the
documentation site does load the mark as an `<img>`, so it paints black and
disappears against the dark theme.

Three exports, because the mark is used three ways:

- **`RookeryMark`** — the glyph alone, stroked in `currentColor`. Takes the
  colour of whatever it sits in.
- **`RookeryTile`** — the mark on its rounded tile, the favicon/app-icon form.
  Its glyph is painted the explicit brand cream rather than `currentColor`,
  because a tile supplies its own background: an inherited foreground would take
  the page's text colour and vanish into the fill. The gradient `id` is a prop
  for the same reason `WorkspaceAvatar` derives one from its slug — two tiles on
  screen with the same id make the second reference the first one's gradient.
- **`RookeryLogo`** — mark plus wordmark, locked up as one unit with a
  deliberately tight gap. Mark and word read as one logo only while they are
  closer to each other than either is to anything else; spaced further apart they
  read as two adjacent objects. That is the same defect reported on the website.

`public/favicon.svg` stays a separate file — a browser tab needs a real one — and
is the one copy that cannot be generated from the component. The comment says so.

## Workspace images

**Eight mark presets**, one per brand hue, with `rookery` (ember → amber) first.
It is first because it is also the default, and a picker whose first tile is the
one you already have is easier to read than one where the default is buried.

**The default changed.** An unset `workspaces.icon` used to render the workspace
name's first letter on a solid `bg-foreground` square. It now renders the
`rookery` preset, so a first-run install looks like the product before anyone has
chosen anything.

The cost is real and worth stating: two un-customised workspaces are no longer
distinguishable by avatar alone. The name is adjacent in both places the avatar
appears — the rail's switcher and the workspace-selection rows — so the
information moved rather than disappeared.

An **unknown** slug still falls back to the initial. That case means a workspace
was configured by a newer build than this one, and rendering the default there
would silently present it as the user's choice. Falling back to the monogram is
honest about not recognising the value.

`rookery` must also be storable as an explicit choice, or picking the tile a
workspace already displays would 400 against the server's validator.

**The 30 motif presets keep their slugs and their motifs**, so no stored value is
orphaned; only the gradients changed. Every pair was re-derived to sit with
ember: one lightness recipe (a deep stop against a light one), saturation pulled
back off the default Tailwind ramps, each hue nudged warm.

Hue **separation** is preserved on purpose. These presets exist to tell
workspaces apart at 20px in the rail — that is their only job — so pulling them
all toward orange in the name of consistency would defeat it. Harmonised means
the same recipe, not the same colour.

`TestWorkspaceIconSlugsMatchTheSPA` parses the TSX against
`web/api_settings.go`'s validator, so the eight new slugs had to be added in both
places; a slug in one alone fails the build rather than shipping a tile that
cannot be saved or a value with no artwork.

## The two unbranded screens

Sign-in and workspace selection are the only screens the owner sees **outside the
app shell**, where the rail's branding is absent. Both now carry the mark.

On sign-in the tile sits above the heading, and the heading drops to naming the
page — two "Rookery"s stacked would be one more than the screen needs. On
workspace selection the mark and wordmark sit in a small header rule above the
existing page title, which keeps `PageTitle` doing its one job rather than being
special-cased.

## Testing

`components/brand/RookeryMark.test.tsx`:

- the mark strokes in `currentColor` and fills `none` — the property the whole
  component-not-image decision rests on;
- it is labelled for screen readers;
- the tile paints its glyph explicitly rather than inheriting;
- two tiles get distinct gradient ids;
- the logo lockup keeps its tight gap;
- eight mark presets exist and the default is first;
- the default resolves to a real preset;
- slugs are unique;
- **every original motif slug survives** — the palette pass changed only colours,
  and renaming one would orphan every workspace that stored it;
- an unset icon renders the mark; an unknown one renders the initial.

Two existing tests asserted the old monogram default and were updated to the new
behaviour rather than worked around.

## Not in scope

Custom workspace image upload stays deferred for the reasons already in
`CLAUDE.md`: it needs a multipart endpoint, an `iolimit` cap, MIME sniffing (SVG
is an XSS vector), vault storage with backup implications, and a two-shape icon
field. Bundling it here would put a security review on the critical path of a
visual change.
