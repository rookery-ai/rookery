# Brand identity and narrative — design

**Date:** 2026-08-06
**Scope:** Rookery's visual identity and narrative positioning. Brand surfaces only.
**Status:** Approved. Landing page and launch GTM are separate specs.

This is spec 1 of 3. The work was decomposed because it spans independent
deliverables with different owners and different failure modes:

1. **Identity + narrative** — this document.
2. **Landing page** — structure and section copy. Depends on 1.
3. **Launch GTM** — HN/PH/Reddit narrative, README-as-shopfront, docs. Depends on 1.

Cloud tiers and freemium pricing are explicitly out of scope until the
open-source release ships.

---

## 1. Locked decisions

| Decision | Value | Provenance |
|---|---|---|
| Brand temperature | Warm and living | Chosen directly |
| Positioned against | Obsidian / Notion | Chosen directly |
| Narrative | "Your notes grew hands" | Follows from positioning |
| Mark | The Weave | Chosen directly, from four rendered candidates |
| Colour scope | Marketing surfaces only | **Inferred** — see below |
| App UI re-skin | No | Chosen directly |

**The colour-scope row is an inference, not a stated choice.** The question
offered three ways to resolve the ember/`--warn` collision (§4): (a) brand-only
colour, (b) adopt ember as the product accent and retune `--warn`, (c) promote
dusk to accent. The answer was truncated. But the same round of answers ruled
out re-skinning the app, and both (b) and (c) require editing shipping tokens in
`web/ui/src/index.css`. Only (a) is consistent with "brand only", so (a) is what
this spec builds on. A later reader should treat it as settled by implication
rather than by preference, and (b) remains the more coherent long-term answer if
the app is ever re-skinned.

### Positioning

Rookery is filed **instead of Obsidian or Notion**, not instead of n8n or Zapier.
The vault is the hero; agents are what make it act. This is the truest reading of
the product — the durable markdown vault is the thing no competitor in the
adjacent categories has — and it is also the smallest defended position, which is
the point. n8n has no memory. Notion AI cannot run at 03:00. A hosted assistant
cannot hold your OAuth tokens.

The consequence for design: the mark must say *knowledge base with inhabitants*.
It must not say *workflow*.

---

## 2. The mark — "The Weave"

Three strokes. The top two are lines of text. The third is the same line, bent
into a nest.

The mark states the product claim rather than the metaphor's scenery: **the nest
is woven from your notes**. A rook builds its nest from sticks taken off the tree
it lives in; an agent builds from the notes it lives in, and writes new ones
back. Habitat and material are the same substance.

It was chosen over three alternatives, all of which were rendered and tested:

- **The Nest** (bowl + three arriving) — legible everywhere, memorable nowhere.
- **The Fork** (bare branch, nests at the forks) — closest to the original brief
  and reads as a file tree / git graph to a developer, but the busiest of the set
  and the one that degrades at 16px.
- **The Canopy** (open crown holding three nests) — truest to the dictionary
  meaning of *rookery*, which is a colony rather than a nest, but one step from a
  cloud-platform logo. Wrong association for a self-hosted product.

The Weave has the fewest parts of the four, which is why it also survives 16px
best. Its cost is that the concept needs one sentence of explanation on first
encounter — paid once in the hero copy, never again.

### Geometry — canonical source

Drawn on a 32-unit grid. Stroke 3.1, round caps, no fill. This is the asset; it
is reproduced here in full so the spec does not depend on an external link.

```svg
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32" width="32" height="32"
     role="img" aria-label="Rookery">
  <path d="M11 8.5h10M7.5 14h17M4.5 19.5C4.5 26 9.5 29 16 29S27.5 26 27.5 19.5"
        fill="none" stroke="currentColor" stroke-width="3.1" stroke-linecap="round"/>
</svg>
```

Construction rules, for anyone redrawing it:

- The three strokes sit at y = 8.5, 14 and 19.5 — an even 5.5-unit rhythm. Keep
  it even; unequal spacing reads as an accident rather than as lines of text.
- Widths are 10, 17 and 23 units — progressively wider going down. The widening
  is what makes the top two read as a text block receding, and the bowl as the
  thing they fall into.
- The bowl is symmetric: the `S` command reflects the preceding control point
  about (16, 29). Do not hand-tune one side.
- `stroke="currentColor"` always. The mark is monochrome and inherits its colour
  from context, exactly like the 91 provider marks it sits beside on the
  connections page.

### Clear space and minimum size

- Clear space on all sides: 4 grid units (12.5% of the mark's box).
- Minimum size: 16px. It was designed against that constraint and tested at it.
  Below 16px the three strokes begin to merge; do not use it smaller.
- Never add a drop shadow, gradient, or outline. Never rotate it.

---

## 3. Typography

**Monospace for display and labels. System sans for body.**

The `.sh` domain sets the genre before anyone reads a word: developer tool,
self-hosted, runs on your own machine. A monospace wordmark agrees with that
rather than arguing with it, and it gives the warm palette something hard-edged
to push against — which is the product exactly. A warm knowledge base, run by an
uncompromising single binary.

| Role | Stack |
|---|---|
| Display, headings, labels, wordmark | `ui-monospace, "SFMono-Regular", "SF Mono", "JetBrains Mono", Menlo, Consolas, monospace` |
| Body | `ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif` |

A serif display face was deliberately rejected. Warm cream ground plus a serif
display plus a terracotta accent is currently the single most over-produced
"warm" identity in circulation; the palette in §4 already leans warm, so the type
is where the direction is differentiated.

**No CDN webfont on any brand surface.** The product refuses an external font
import because it ships as one binary for offline and LAN installs
(`internal/fonts` exists precisely so there is exactly one copy of Inter and it
travels inside the binary). The marketing site holds the same line and
self-hosts. That discipline is part of the story, not a footnote to it.

### Wordmark

- **Always lowercase.** `rookery`, never `Rookery`, in the wordmark. The name is
  a command you would type, not a company you would incorporate. Running prose
  capitalises it normally.
- Letter-spacing `-0.05em` at display sizes; monospace sets loose by default and
  needs tightening to read as a wordmark rather than as code.
- Three approved lockups:
  1. **Primary** — mark in ember, then `rookery` in ink. Mark height equals cap
     height plus ~15%.
  2. **With domain** — mark in ink, then `rookery` in ink with `.sh` in stone.
     The TLD is set lighter so it reads as an address, not as part of the name.
  3. **Mark alone** — favicon, avatar, tile grids, anywhere under 32px.

---

## 4. Palette

Warm ground and warm ink, held against a cool counterweight. Rooks are seen
against a dusk sky, so the cool value is subject-derived rather than arbitrary —
and it is what keeps the direction out of generic-warm territory.

| Token | Hex | Role |
|---|---|---|
| Bone | `#ECE5DB` | Page ground |
| Paper | `#F8F4EE` | Raised surfaces, cards |
| Bark | `#211D1A` | Ink — warm near-black, never pure `#000` |
| Ember | `#A94C1C` | Accent. Used sparingly: one per view |
| Dusk | `#46405A` | Counterweight — hero grounds, quotes, deep bands |

Dark-mode equivalents: ground `#17140F`, surface `#211D18`, ink `#ECE5DB`,
ember `#E08D51`, dusk `#3B3550`.

### The collision, and why the palette stops at the brand

Ember at `#A94C1C` sits close to two semantic colours the app already ships:
`--warn: #985a2e` and `--danger: #be322c`. If ember became the in-product accent,
a primary button would read as a warning state to anyone scanning quickly.

**These tokens therefore do not enter `web/ui/src/index.css`.** The app keeps
`--accent: #2d5a74` and its existing warm-grey Notion chrome. The brand palette
governs the marketing site, the README, social cards, slides and any published
asset — nothing else.

### Accepted cost

Someone clicking from rookery.sh into an app screenshot sees a different colour
world: ember and dusk outside, slate and warm grey inside. This is recorded as a
decision, not allowed to be discovered later as drift. It is defensible — brand
colour diverging from product accent is common and the two palettes share a warm
neutral ground, so they read as relatives rather than strangers — but it is a
cost. Revisit it if and when the app is re-skinned, at which point option (b)
from §1 (adopt ember as `--accent`, retune `--warn` toward a yellower amber)
becomes the coherent move. That change would need a pass through
`web/ui/src/contrast.test.ts`, which computes WCAG ratios directly out of the
stylesheet in both themes and fails the build on a sloppy retune.

---

## 5. The one repository change

Brand-only scope has exactly one seam with shipping code: **the favicon lives
inside the app.**

`web/ui/public/favicon.svg` currently renders three white dots on a `#7e14ff`
rounded square. Git shows that purple was inherited verbatim from the Vite
starter bolt it replaced (`2e544c1` → `0580e75`) — it was never chosen, and it
matches neither the brand nor the app. It is scaffold residue, not a baseline.

**It becomes The Weave in bone on an ember rounded square:**

```svg
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32" width="32" height="32"
     role="img" aria-label="Rookery">
  <rect width="32" height="32" rx="7" fill="#A94C1C"/>
  <path d="M12 10h8M9.2 14.4h13.6M6.8 18.8C6.8 24 10.8 26.4 16 26.4S25.2 24 25.2 18.8"
        fill="none" stroke="#ECE5DB" stroke-width="2.8" stroke-linecap="round"/>
</svg>
```

The filled-tile form is chosen over a bare mark on transparent for one reason: a
three-stroke monochrome mark on transparent is weak in a browser tab strip
against arbitrary chrome, where a mid-tone filled tile stays recognisable in both
light and dark.

Two things about these numbers are deliberate and should not be "cleaned up":

- **The geometry is scaled by 0.80 about (16, 16) and baked into the path**,
  rather than applied as a `transform`. The scale supplies the clear space the
  tile would otherwise crop; baking it keeps the file a single flat path with no
  nested group to misread.
- **The stroke is 2.8, not the 2.48 that scaling 3.1 by 0.80 would give.** That
  is an optical correction. The version validated at 16px was the *bare* mark,
  whose strokes land at 1.55px; scaling it into a tile thins them to 1.24px,
  which is below where three parallel strokes reliably separate. 2.8 brings the
  rendered weight back to ~1.4px. Small sizes need weight added; this is that.

The bare-mark form in §2 keeps `stroke-width="3.1"` and is the one to use
anywhere the mark is not on a filled tile.

This is deliberately the *only* file the brand touches. No token changes, no
component changes, no re-skin.

---

## 6. Voice

The repository's own writing is the strongest brand asset in the project and
nobody has noticed it. Lines like *"a `python3` warning is not cosmetic"* and
*"the guard is check-then-write — a run can still start in the gap"* do something
almost no landing page manages: they demonstrate competence instead of claiming
it. Brand surfaces should sound like that, not like a site.

Three rules:

**Specific over superlative.** "91 providers, 471 curated actions" beats
"seamless integrations". Numbers a reader can go and check are the entire trust
mechanism for a self-hosted tool that will be handling their OAuth tokens.

**Name the limits out loud.** *"Off Linux there is no filesystem sandbox at all"*
is already in the README. Keep it on the site. Volunteering the sharp edge is
what makes everything else believable, and the audience for a self-hosted agent
runner will find it anyway.

**No hype adjectives.** No "effortlessly", no "powerful", no "revolutionise". The
test: if a sentence would survive being pasted into a competitor's site
unchanged, cut it.

---

## 7. Deliverables

In scope for the identity, produced against this spec:

- `favicon.svg` (§5) — the only repository change.
- Mark as standalone SVG, at 32 and as a filled tile.
- The three wordmark lockups (§3).
- Palette tokens as a CSS custom-property block for reuse by the landing page.

Out of scope, deferred to spec 2 (landing page) and spec 3 (launch GTM):

- Open Graph and social card artwork — needs the landing page's headline.
- README header image — needs the launch narrative.
- Slide template, docs site theme.

---

## 8. Open questions

None blocking. One deliberately deferred:

**Whether the app is ever re-skinned to the brand palette.** Answered "no" for
this release. §4 records what changes if that answer changes, so the decision can
be revisited without re-deriving the reasoning.
