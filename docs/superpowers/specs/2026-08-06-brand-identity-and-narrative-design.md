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
| Brand temperature | Warm and living | Chosen directly |
| Positioned against | Obsidian / Notion | Chosen directly |
| Narrative | "Your notes grew hands" | Follows from positioning |
| Mark | The Weave | Chosen directly, from four rendered candidates |
| Type for reading | Warm sans, not monospace | Follows from audience — §3 |
| Voice | Transcribed from the product, not the repo | Follows from audience — §6 |
| Colour scope | Marketing surfaces only | **Inferred** — see below |
| App UI re-skin | No | Chosen directly |

The audience row is load-bearing and was added after the first draft. It
invalidated two sections of this spec — the monospace-for-reading type direction
(§3) and the whole voice section (§6) — while leaving the mark, palette and
positioning intact. Warm-and-living and a notes-first frame are *more* right for
a non-technical audience, not less.

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
| agent run, invocation | each time it runs |
| script, tool, Python | *say what it does, not how* |

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

**"Agent" or "assistant"? — needs an answer, blocks brand copy.**

The product currently says both, and the split is not random. The conversational
layer is contractually **assistant**: the designer prompts say "your assistant",
and `<constraints>` forbids the technical vocabulary that "agent" travels with.
Every other surface says **agent**: the SPA (176 uses against 75 of "assistant"),
the `/api/v1/agents` routes, the `agents` table, the `/agent` chat command, the
CLI and the README.

The brand has to pick one word and use it everywhere, and each choice has a cost:

- **"Assistant"** is the right word for the stated audience and is what the
  product already says to a person's face. Cost: the SPA chrome, the docs and the
  README drift further from it until someone does a rename pass. Route and table
  names can stay — those are internal.
- **"Agent"** is what the code, the API and the ecosystem say, and it is the term
  a technical evaluator will search for. Cost: it is exactly the register §6
  rules out, and the product's own designer is forbidden from using it
  conversationally, so the landing page would introduce a word the product then
  avoids.

Recommendation: **"assistant"** for all brand and user-facing copy, "agent"
retained as the internal/API term. But this implies future UI copy work, so it is
the user's call, not this spec's.

**Whether the app is ever re-skinned to the brand palette.** Answered "no" for
this release. §4 records what changes if that answer changes, so the decision can
be revisited without re-deriving the reasoning.

**Who the landing page addresses at OSS launch — deferred to spec 2, deliberately.**
There is a real gap between the person who *installs* Rookery today (registers
their own Google OAuth app, sets `ROOKERY_PUBLIC_URL`, runs a binary) and the
person it is *for* (§6). At launch the visitor may be technical even when the
beneficiary is not. This does not change the voice — every reading of "normal
users" gives the same answer — so it is not resolved here. It is a **depth**
problem for the landing page: plain-language value on top, technical proof
available below for the evaluator. Spec 2 owns it.
