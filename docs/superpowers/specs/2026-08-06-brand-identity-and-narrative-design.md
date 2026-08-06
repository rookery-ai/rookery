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
| Audience | Normal people, not programmers | Chosen directly |
| Name for the thing | "Agent", on every surface | Chosen directly — §8 |
| Brand temperature | Warm and living | Chosen directly |
| Positioned against | Obsidian / Notion | Chosen directly |
| Narrative | "Your notes grew hands" | Follows from positioning |
| Mark | The Weave | Chosen directly, from four rendered candidates |
| Type for reading | Warm sans, not monospace | Follows from audience — §3 |
| Voice | Transcribed from the product, not the repo | Follows from audience — §6 |
| Colour scope | Brand **and** the app accent | Chosen directly — §4 |
| App UI re-skin | Accent only, not a full re-skin | Chosen directly |

The audience row is load-bearing and was added after the first draft. It
invalidated two sections of this spec — the monospace-for-reading type direction
(§3) and the whole voice section (§6) — while leaving the mark, palette and
positioning intact. Warm-and-living and a notes-first frame are *more* right for
a non-technical audience, not less.

**The colour-scope row was revised.** The first draft of this spec resolved the
ember/`--warn` collision (§4) with option (a) — brand-only colour, app keeps its
slate accent — because the answer selecting between the three options had been
truncated and "no app re-skin" appeared to rule the others out. That inference
was superseded by a direct instruction to bring ember into the app, which is
option **(b)**: adopt ember as `--accent` and retune `--warn` to clear it.

This is a narrower change than "re-skinning the app". The chrome, the type scale,
the borders and the layout are all untouched; only the accent and the one status
colour that collided with it moved. §4 records the work and the measurements.

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

**Monospace for the wordmark only. Everything a person reads is a warm sans.**

The `.sh` domain sets a genre, and the *wordmark* may agree with it: a logotype
is a shape, not reading material, and `rookery` set lowercase in monospace reads
as identity rather than as code. That argument stops at the wordmark. It does
**not** extend to headings and body copy.

This is a correction to an earlier draft of this spec, which set display and
headings in monospace on the strength of the `.sh` argument. That was wrong for
the stated audience (§6): monospace running text signals "this is for people who
live in a terminal" on every single line, which is precisely the impression the
product's own interface works to avoid.

| Role | Face |
|---|---|
| Wordmark, and only the wordmark | `ui-monospace, "SFMono-Regular", "SF Mono", "JetBrains Mono", Menlo, Consolas, monospace` |
| Headings and body | Inter (self-hosted) — the variable woff2 already vendored at `internal/fonts/InterVariable.woff2` |
| Code samples, terminal output, key/value labels | The monospace stack above, at its literal use only |

Inter is the default for one strong practical reason: it is already in the
repository, chosen for a tall x-height and open apertures, and the project
already maintains exactly one copy of it because two consumers need identical
bytes. Reusing it means the landing page and the app render the same face, and
the brand adds no new asset.

If spec 2 decides the landing page wants more warmth than Inter's fairly neutral
tone gives, the shortlist is Source Sans 3, IBM Plex Sans or Public Sans — all
open-licensed and self-hostable. That is a landing-page decision, not an identity
one; Inter is what ships absent a deliberate change.

A serif display face was deliberately rejected. Warm cream ground plus a serif
display plus a terracotta accent is currently the single most over-produced
"warm" identity in circulation; the palette in §4 already leans warm, so the mark
and the dusk counterweight are where the direction is differentiated.

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

### Ember is the app accent too

`web/ui/src/index.css` now carries ember as `--accent` / `--primary` / `--ring` /
`--sidebar-*`, in both themes: `#a94c1c` on light, `#e08d51` on dark. The slate
`#2d5a74` is gone. Nothing outside that file referenced it, so no component
changed.

**The collision was worse than first stated, and it was measured rather than
eyeballed.** The claim in the first draft was that ember sat "close to" the
existing `--warn`. In fact:

| Pair | Hue gap | Verdict |
|---|---|---|
| light ember `#a94c1c` vs old `--warn` `#985a2e` | **4.5°** | the same colour to the eye |
| dark ember `#e08d51` vs old `--warn` `#d99a66` | **2.0°** | worse |

A primary button and a warning would have been indistinguishable at a glance.
`--warn` was therefore retuned to gold, keeping warning semantics while clearing
the accent:

| Token | Was | Now | Hue gap from ember | Ratios (bg / chrome / own soft) |
|---|---|---|---|---|
| `--warn` light | `#985a2e` | `#85610f` | 21.3° | 5.65 / 5.23 / 4.85 |
| `--warn` dark | `#d99a66` | `#dac462` | 23.8° | 10.09 / 9.35 / 7.66 |

Dark `--secondary` and `--sidebar-accent` also moved from the blue-slate
`#2d3e4f` to a warm `#3a2b20`; a cool fill under an ember primary read as a
leftover from the old palette.

### The guard that was missing

`contrast.test.ts` computes WCAG ratios out of the stylesheet in both themes and
would have failed the build on a sloppy retune — but **it never caught this
collision, and could not have.** Every one of the old values passed every ratio
assertion. A luminance ratio is blind to hue.

Two tests were added there: `--accent` must clear each status colour by a hue
floor (60° from `--ok`, 18° from `--warn`, 15° from `--danger`), and `--warn` and
`--danger` must stay 25° apart. Verified by reverting `--warn` to `#985a2e` and
confirming the suite fails at 4.48° against the 18° floor — a test that has never
failed proves nothing.

### Residual, accepted

Light ember and `--danger` `#be322c` sit **18.0°** apart — the tightest pair that
survives. They are not retuned further because they separate on lightness and
saturation as well as hue, and because the design system already requires a
destructive *confirm* to carry an icon, where the icon is the warning. The test
floor is set at 15° for this pair specifically so the constraint is visible in
code rather than remembered. If `--danger` is ever restyled, this is the pair to
re-check.

---

## 5. Repository changes

Three files, all shipped and verified (§4 covers the palette work):

| File | Change |
|---|---|
| `web/ui/src/index.css` | Ember as `--accent`/`--primary`/`--ring`/`--sidebar-*`, `--warn` retuned, dark `--secondary`/`--sidebar-accent` warmed. Both themes. |
| `web/ui/src/contrast.test.ts` | Two hue-separation tests — the guard that was missing. |
| `web/ui/public/favicon.svg` | The Weave, replacing the scaffold purple. Below. |

Verified with `tsc -b` (clean), `vitest run` (96 files, 923 tests, all pass),
`oxlint` (no new warnings) and `vite build` — the four checks the CI Frontend job
runs.

### The favicon

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

The tile fill is `--accent`'s light value, so the favicon and the app's primary
colour are now literally the same ember rather than two things that happen to
look alike.

---

## 6. Voice

**The audience is a normal person, not a programmer.** Someone who wants their
email triaged, their bills tracked and their notes kept — not someone who wants
to configure an agent runtime. This is the governing constraint, and it is
stricter than it sounds: it disqualifies most of what a repository like this one
would naturally say about itself.

### The voice already exists. Do not invent one.

An earlier draft of this spec derived the voice from `README.md` and `CLAUDE.md`,
holding up lines like *"a `python3` warning is not cosmetic"* as exemplary. That
was a mistake worth recording, because the failure mode generalises: **those are
engineering documents, written for developers and for coding agents. They are the
codebase's voice, not the product's.** Brand copy taken from a repository's
internal documentation will always read as software talking about itself.

That specific example was worse than merely mis-registered. To a normal reader it
advertises a dependency and a silent failure — *you need Python installed, and if
you don't have it a safety check quietly switches off*. On a page meant to build
confidence, that is harmful content, not just the wrong tone.

The product's real voice is already decided, already written down, and already
**enforced in shipping code**. `internal/prompts/prompts.go` (the `<constraints>`
block, around line 783) forbids the assistant from using these terms with a user
unless immediately explained in one plain sentence:

> AGENT.md, Python, script, vault, cron, JSON, shell, subprocess, Bash, webhook,
> endpoint, API key

and ships its own translation table:

| Never | Always |
|---|---|
| cron, schedule expression | run schedule — "every morning at 9" |
| vault, knowledge base | your notes |
| write to `state.md` | the assistant will remember this |
| invocation, execution | each time it runs |
| script, tool, Python | *say what it does, not how* |

Note that **"agent" itself is not banned** and is the approved word for the thing
a user creates (§8). What the blocklist governs is the vocabulary of how it is
*implemented*, not the name of the thing itself.

**That block is the source of truth for brand copy too.** Marketing that uses a
word the product is contractually forbidden from saying is not a style
difference; it is a promise the product then breaks on first contact. Any brand
surface — landing page, README, social card, docs homepage — is bound by the same
list.

### Three rules, each derived from the product

**Never make a person decode anything.** The strongest piece of copy in the
codebase is a placeholder: `"Remind me in 10 minutes to call the doctor…"`. It
teaches the entire input format by example, contains no instruction, and assumes
nothing. That is the target register for every headline and every empty state.

**Never guess on someone's behalf.** `web/ui/src/components/designer/SpecPanel.tsx`
translates a schedule into plain English *only when it can prove the translation
is right*, and falls back to the raw expression otherwise — on the reasoning that
the user has no way to tell a wrong plain-English answer from a right one. Copy
inherits this: say the certain thing, or say less. Never round a claim up to make
a sentence flow.

**Show the outcome, never the mechanism.** "It reads your inbox each morning and
writes what matters into your notes" — not "scheduled agent with connector-backed
Gmail access writes to the vault". Both describe the same run. Only one is about
the person.

### Standing bans

No hype adjectives — no "effortlessly", no "powerful", no "seamless", no
"revolutionise". No feature-count bragging as a headline. No sentence that would
survive being pasted unchanged into a competitor's site.

Numbers stay, but as *reassurance placed under a plain-language claim*, never as
the claim itself: "connects to the apps you already use" on top, "91 services,
471 actions" underneath as proof. Concrete counts are still the trust mechanism
for software people run themselves — they are just no longer the pitch.

### One thing the old draft got right, kept

Being straight about limits still belongs, but it moves out of the headline and
into the honest-detail layer, phrased for a person: not *"off Linux there is no
filesystem sandbox at all"*, but *"strongest protection is on Linux — see what
that means on Mac and Windows"*. Volunteering the sharp edge is what makes the
rest believable. Volunteering it in kernel-security vocabulary is not.

---

## 7. Deliverables

In scope for the identity, produced against this spec:

- The three repository changes in §5 — palette, hue guard, favicon. **Shipped.**
- Mark as standalone SVG, at 32 and as a filled tile.
- The three wordmark lockups (§3).
- Palette tokens as a CSS custom-property block for reuse by the landing page.

Out of scope, deferred to spec 2 (landing page) and spec 3 (launch GTM):

- Open Graph and social card artwork — needs the landing page's headline.
- README header image — needs the launch narrative.
- Slide template, docs site theme.

---

## 8. Open questions

**"Agent" or "assistant"? — RESOLVED: agent, everywhere.**

The product said both. The SPA, the `/api/v1/agents` routes, the `agents` table,
the `/agent` chat command, the CLI and the README all say **agent** (176 uses in
the SPA against 75 of "assistant"); the designer prompts lean **assistant** when
addressing a person.

**Decision: "agent", on every surface — brand copy included.** It is what the
code, the API, the docs and the wider ecosystem already say, and it is the term a
technical evaluator searches for.

One correction is recorded here because it was mis-stated when the question was
put: **the bare word "agent" is not on the `<constraints>` blocklist.** Only
`AGENT.md`, the filename, is. The blocklist governs implementation vocabulary
(Python, script, vault, cron, shell, webhook…), not the name of the thing the
user is creating. So this decision costs nothing — it needs no rename pass and
does not conflict with the enforced rules in §6, which continue to apply in full.

The §6 translation table quotes `prompts.go` verbatim, including its own use of
"the assistant will remember this". Those quotations are left exactly as the
source writes them; this decision governs *new* brand and product copy, not
citations of existing code.

**How far the app re-skin goes — partially answered.** The *accent* is now ember
in both themes (§4). What remains untouched, deliberately, is the surrounding
chrome: `--background`, `--chrome` and `--foreground` are still the original
Notion-ish white / warm-grey / near-black, not the brand's bone / paper / bark.
That is a defensible stopping point — the accent is what carries brand
recognition, and the neutrals are what carry readability across a dense
information UI — but it does mean the app's neutrals and the marketing site's
neutrals are near-relatives rather than identical. Taking it further would touch
every contrast assertion in `contrast.test.ts` and is a larger piece of work than
this spec.

**Who the landing page addresses at OSS launch — deferred to spec 2, deliberately.**
There is a real gap between the person who *installs* Rookery today (registers
their own Google OAuth app, sets `ROOKERY_PUBLIC_URL`, runs a binary) and the
person it is *for* (§6). At launch the visitor may be technical even when the
beneficiary is not. This does not change the voice — every reading of "normal
users" gives the same answer — so it is not resolved here. It is a **depth**
problem for the landing page: plain-language value on top, technical proof
available below for the evaluator. Spec 2 owns it.
