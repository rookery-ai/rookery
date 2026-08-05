# Provider icons, core-skill crash, and KB editor affordances

**Date:** 2026-08-05
**Status:** approved

Six defects reported against the running SPA. Five are independent; they share
this document because they were found in one pass, not because they interact.

| # | Symptom | Root cause | Severity |
|---|---|---|---|
| A | `/skills/core/<slug>` throws `Cannot read properties of null (reading 'length')` | Go nil slice marshals to `null`; a TS default parameter does not fire on `null` | crash |
| B | Slash menu opens below the viewport | `position()` never checks viewport bounds | broken affordance |
| C | Scrolling past the end of a note drags the whole shell off-screen | overscroll chains to a scrollable document | friction |
| D | Moonshot (Kimi) shows no icon | white mark vendored onto a white tile | cosmetic |
| E | llama.cpp, LocalAI, Jan, Readwise show letter tiles | deliberate exemptions, now stale | cosmetic |
| F | Open Library's strokes are missing | vendoring strips `<style>`, mark uses `class="st0"` | cosmetic |

Explicitly **not** changing: the Hacker News logo. The vendored
`hackernews.svg` is byte-identical to `https://news.ycombinator.com/y18.svg` —
Hacker News genuinely uses the Y Combinator orange square. It reads as YC
because it *is* YC's Y. Replacing it with an invented mark would be less
accurate, and the owner confirmed keeping it.

---

## A. Core-skill page crash

### Cause

`flattenRequires` (`web/api_skills.go:64`) accumulates into `var out []string`
and returns it. A skill declaring no `requires.bins` / `anyBins` / `env` never
appends, so the function returns a **nil** slice, which `encoding/json` marshals
as `null` — not `[]`.

On the SPA side `SkillView` declares `requires = []` as a default parameter.
A default parameter substitutes only for `undefined`. `null` passes straight
through, and `requires.length > 0` (`SkillView.tsx:142`) throws.

This reproduces on every core skill with no declared tooling —
`agent-collaboration` among them. It does not reproduce on the owner's own
skills because those declare requirements, which is why "custom skills work
well".

### Fix

Both sides, because either alone leaves the trap armed for the next field.

**Server** — `flattenRequires` returns `[]string{}` rather than nil, so the
wire shape is always an array. This is the load-bearing half: it fixes every
current and future consumer of the field at once.

**Client** — `SkillViewProps.requires` becomes `string[] | null | undefined`
and is normalised with `?? []` at the top of the component. Defence in depth:
a Go backend emits `null` for every empty slice, so a component consuming one
must tolerate it regardless of what the server currently sends.

`TestFlattenRequiresShapes` compares the empty case against `nil` via
`reflect.DeepEqual`; it moves to `[]string{}` with the change. `DeepEqual`
distinguishes the two, so the existing test would otherwise fail — which is
correct, it is asserting the old contract.

### Sweep

The same nil-slice-to-`null` shape exists wherever a Go handler returns an
accumulated slice and the SPA reads `.length` or `.map` on it. Audit the
`/api/v1` DTOs for slice fields, and for each one either confirm the handler
initialises with `make(...)`/`[]T{}` or make the consumer null-tolerant. Scope
is an audit plus whatever it turns up — not a refactor.

### Tests

- Go: request a core skill that declares no requirements and assert the raw
  response body contains `"requires":[]`. Asserting on the marshalled bytes,
  not on a decoded struct, is the point — decoding into `[]string` erases the
  `null`-vs-`[]` distinction that causes the crash.
- Vitest: render `SkillView` with `requires={null}` and assert it does not
  throw and renders no "Needs:" line.

---

## B. Slash menu placement

### Cause

`SlashMenu.tsx:149-155`:

```ts
el.style.left = `${rect.left}px`;
el.style.top  = `${rect.bottom + 4}px`;
```

The element is `position: fixed`, so those are viewport coordinates, and
nothing bounds them. Measured with the caret on the last line of a long note
in a 1600×900 viewport, the popup renders at `top: 868`, `height: 442`, so its
bottom lands at 1310 — **410px below the fold**, leaving roughly 32px of it
visible. A caret near the right edge pushes it off the side the same way.
Neither is recoverable: the menu is `position: fixed`, so no ancestor can
scroll it into view.

The 442px measurement matters for the fix — the menu is tall enough that on a
short viewport it may not fit on *either* side, so "flip above" alone is not
sufficient.

### Fix

Replace `position()` with a bounded placement pass:

1. Measure the rendered menu with `getBoundingClientRect()` (it is already in
   the DOM when `position()` runs — `onStart` appends before calling).
2. `below = innerHeight - caret.bottom - GAP`, `above = caret.top - GAP`.
3. Prefer below. Flip above when the menu does not fit below but does fit
   above.
4. When it fits **neither** side, choose the larger side and set `max-height`
   to that space with `overflow-y: auto`, so every item stays reachable rather
   than the list being clipped at an arbitrary point.
5. Clamp `left` into `[GAP, innerWidth - width - GAP]`.

Reposition on window `resize` and on scroll of the editor pane, not only on
suggestion updates: the menu is fixed-positioned while the caret is not, so any
scroll desynchronises them. Listeners are attached in `onStart` and removed in
`onExit`, alongside the existing element teardown.

No new dependency. The existing comment records that manual positioning is a
binding constraint of the original task; this keeps it.

### Tests

Unit tests driving `position()` with stubbed `clientRect` values and a stubbed
`innerHeight`/`innerWidth`: caret at top (opens below), caret near bottom
(flips above), caret with room on neither side (capped `max-height`, larger
side chosen), caret near the right edge (clamped `left`).

---

## C. Editor bottom space and scroll containment

### Cause

Measured in a real browser (headless Chromium, 1600×900, against an isolated
instance) rather than reasoned about, because jsdom has no layout engine and
cannot answer any of this. Two of the three theories in the first draft of this
spec were wrong and are recorded here so they are not re-attempted.

**The actual defect is overscroll chaining.** With a long note, once the
editor pane is scrolled to its bottom, one more wheel notch scrolls the
**document**, which drags the entire `h-screen` shell — icon rail, context
pane, file tree — up and out of the viewport:

| state | `documentElement.scrollTop` | rail `top` | pane `scrollTop` |
|---|---|---|---|
| initial | 0 | 0 | 0 |
| pane pinned to its bottom | 0 | 0 | 1491 |
| after wheeling further | **1459** | **−1459** | 1491 |

That single row is both reported symptoms at once: the page scrolls instead of
the editor, and what you scroll into is blank background — you are scrolling
the whole app shell off-screen and seeing what lies beneath it.

It is possible because the document is genuinely scrollable on a long note:
`documentElement.scrollHeight` is 2359 against a `clientHeight` of 900, even
though `body.scrollHeight` is 900. Setting `documentElement.scrollTop = 600`
directly moves the rail to `top: -600`, confirming it. The tall editor content
propagates its scrollable overflow to the initial containing block.

**Disproved — do not re-attempt:**

- *"The blank region is inert."* It is not. Clicking the 472px empty band on a
  short note already focuses the editor and places the caret
  (`focused: true`, `selInsideEditor: true`). `min-height: 60vh` gives a short
  note a working click target, which is what it is for. No click-to-append
  handler is needed, and adding one would risk hijacking ordinary clicks for
  no gain.
- *"`scrollIntoView` chains to every scrollable ancestor."* It does not here.
  `scrollIntoView({block:"nearest"})` on the last block moved the pane
  (`scrollTop` 0 → 1459) and left `documentElement.scrollTop` at 0. Likewise
  `Ctrl+End` scrolled only the pane. Caret movement is not the trigger.

### Fix

Scroll containment only — the smaller change, and the one the evidence
supports:

- **`overscroll-behavior: contain`** on the editor's scroll container
  (`NoteEditor.tsx:835`). This stops wheel and trackpad momentum at either end
  of the pane from chaining outward.
- **`overflow-hidden` on the `h-screen` shell root** (`AppShell.tsx:78`). The
  app shell is a fixed-height, non-scrolling frame by design; every scrolling
  region inside it is explicit. Making that explicit means no page can scroll
  its own chrome out of view, which protects every route rather than just this
  one. This is the load-bearing half — containment alone still leaves a
  document that *can* be scrolled by other means.

`min-height: 60vh` stays exactly as it is.

### Tests

- The editor's scroll container carries `overscroll-behavior: contain`.
- The shell root carries `overflow-hidden`.
- Browser-level verification (not a unit test — jsdom cannot express it):
  re-run the measurement above and assert that wheeling past the end of the
  pane leaves `documentElement.scrollTop` at 0 and the rail at `top: 0`.

---

## D. Moonshot renders invisibly

### Cause

`scripts/vendor-brand-logos.sh` maps `moonshot:kimi-color`. lobehub's
`-color` suffix does not mean "full colour on any background" — for Kimi it is
a white mark on a transparent field, drawn for the brand's blue container. The
vendored file's only substantial path is `fill="#fff"`.

`ProviderLogo` renders every mark on a **white** tile (a deliberate choice, so
that the whole gallery reads consistently). A white mark on a white tile is
invisible; all that survives is the small `#1783FF` dot.

The file is present and well-formed, so `TestBrandLogoCoverage` passes. The
gap is that coverage tests for *existence*, never for *visibility*.

### Fix

Re-vendor from lobehub's `kimi` (monochrome) instead of `kimi-color`. The
monochrome mark paints with `fill="currentColor"`, and `ProviderLogo` already
pins `color: #18181b` for monochrome marks (`isMonochrome`). Confirmed present
in `@lobehub/icons-static-svg@1.94.0` as `package/icons/kimi.svg`.

### Test

A guard that fails when a vendored SVG's fills are exclusively white or
near-white — invisible against the tile. This is the check that would have
caught the defect at vendoring time. It must tolerate marks that are white
*over their own coloured background shape* (Telegram, Reddit, Facebook and a
dozen others are legitimately white-on-brand-colour), so the condition is "no
non-white fill anywhere", not "contains a white fill".

---

## E. Four providers rendering as letter tiles

### Cause

Not an oversight. `web/logo_coverage_test.go`'s `allowNoLogo` exempts
`llamacpp`, `localai`, `jan`, `readwise` and `generic`, with a recorded
rationale: no vendoring source carried these marks, and *"an approximated logo
misrepresents someone else's brand, which is worse than a letter."*

That policy is correct and stays. What changed is the premise — three of the
four now publish a fetchable asset, and Readwise's is reachable by a path the
original attempt did not try.

### Fix

Vendor the authentic upstream asset for each and delete its `allowNoLogo`
entry. All four URLs were probed and returned 200 on 2026-08-05:

| Slug | Source | Form |
|---|---|---|
| `llamacpp` | `ggml-org/llama.cpp` → `media/llama1-icon.svg` | 250×250 SVG |
| `localai` | `mudler/LocalAI` → `core/http/static/favicon.svg` | 1024×1024 SVG |
| `jan` | `jan.ai/favicon.ico` | 48×48 ICO |
| `readwise` | their CDN `apple-touch-icon` | 180×180 PNG |

`generic` **stays exempt**. It is the coder settings' "Custom
(OpenAI-compatible)" escape hatch — a user-supplied endpoint with no brand, for
which the neutral tile is the correct rendering, not a fallback.

Notes on the three awkward ones:

- **Readwise.** `readwise.io/favicon.ico` and every static path still return
  403 behind their CDN challenge, which is exactly what the exemption comment
  records. The page itself serves 200 to a browser user-agent, and its
  `<link rel="apple-touch-icon">` points at a content-hashed CDN URL that
  serves the PNG unchallenged. Because that URL embeds a content hash it will
  change when Readwise redeploys; the vendoring script pins it, and the
  committed SVG is what the app actually renders, so a future 404 breaks a
  re-vendor run and never the app.
- **Jan** publishes only an ICO. ImageMagick is not available on this host;
  Pillow is, and reads ICO. The script gains an ICO branch that extracts the
  largest frame to PNG and then reuses the existing PNG wrapping path.
- **LocalAI**'s square asset is its 1024×1024 `favicon.svg` at ~111 KB. Its
  `logo-mark.png` is smaller but 240×163, which letterboxes badly in a square
  tile. Take the SVG and reduce it — every logo is inlined into the DOM by
  `ProviderLogo`, so size is paid on render. If it cannot be brought under
  roughly 40 KB while staying faithful, fall back to the PNG-wrapping path at a
  square canvas rather than shipping a disproportionate mark.

The script already wraps a PNG as `<svg><image href="data:..."/></svg>`,
because `ProviderLogo` inlines every asset and therefore requires SVG. A
full-colour raster needs no `currentColor`, so wrapping costs nothing.

### Test

Removing the four `allowNoLogo` entries makes `TestBrandLogoCoverage` itself
the regression test — it fails if any of the assets goes missing.
`TestBrandLogoAssetsAreWellFormed` already covers shape (starts with `<svg`,
has a `viewBox`, no `<script>`, no `<title>`) and applies to the new files
automatically.

## F. Open Library's missing strokes

`openlibrary.svg` draws part of its mark with `class="st0"` and no inline
attributes. `strip_svg` removes `<style>` blocks — correctly, since these files
are inlined with `dangerouslySetInnerHTML` — so those paths lose their
`fill:none; stroke:…` rules, fall back to `fill: black` with no stroke, and
being zero-area lines they render as nothing. The coloured paths survive, so
the mark is degraded rather than absent, which is why it was never noticed.

Re-vendor with the stroke rules inlined as presentation attributes. If the
class-based styling cannot be resolved faithfully, leaving the current file is
acceptable — this is the lowest-value item here and must not block the rest.

---

## Sequencing

A is the only defect that breaks a page, and it is the smallest change; it goes
first. B and C are independent of A and of each other. D, E and F all touch
`scripts/vendor-brand-logos.sh` and the logo assets, so they land together to
keep the vendoring run single.

## Risks

- **Re-running the vendoring script rewrites unrelated assets.** It fetches
  the full manifest. Only the intended files may appear in the diff; verify
  before committing, and revert incidental churn.
- **`TestBrandLogoAssetsAreWellFormed` runs over every file in the directory**,
  so a badly-converted new asset fails the suite rather than shipping. That is
  the desired behaviour and needs no change.
- **`overflow-hidden` on the shell root is app-wide.** It is the right
  default — every scrolling region inside the shell is already explicit — but
  a route that was quietly relying on the document scrolling would lose its
  scrollbar. Check the long pages (settings, connections, agents) in a browser
  after the change, not only the KB.
- **Defect C can only be verified in a real browser.** jsdom reports 0 for
  every geometry, so the unit tests can assert the CSS declarations but not the
  behaviour. The measurement harness that found this must be re-run to confirm
  the fix.
