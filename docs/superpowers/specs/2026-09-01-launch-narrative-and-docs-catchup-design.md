# Launch narrative and documentation catch-up — design

**Date:** 2026-09-01
**Repositories:** `rookery-ai/rookery`, `rookery-ai/rookery-web`

Two jobs that arrived together and are kept apart here because they have
different approval characteristics: **correcting what the documentation says
that is untrue**, and **changing the narrative** to the positioning locked in
the launch plan.

`make docs-sync-check` passes 11/11 against both checkouts. Every defect below
is prose, which is the half that check explicitly cannot see — it verifies
counts, variable names, command names, provider names and logo coverage against
source, and says nothing about whether a paragraph describes a feature
correctly. That is the gap this document closes.

## Findings

### Bucket 1 — factual defects

| # | Where | Defect |
|---|---|---|
| 1 | `README.md:45` | Claims Windows has "no service registration yet". `rookery service` ships and registers a Task Scheduler logon task. **The same README contradicts it at line 169**, which lists that task under platform support. The website was corrected in rookery-web#59; the README was not. |
| 2 | `README.md:181` | Tells contributors to "Run the full gate locally first with `make ci`". |
| 3 | `README.md:90–101` | The "Read more" concepts list omits Browser, though `concepts/browser.md` ships and browser control is described two paragraphs above it. |
| 4 | `scripts/gen-readme-assets.py:390` | The features graphic has twelve tiles and no Browser tile. |
| 5 | `rookery-web` `concepts/knowledge-base.md:61–72` | The editing list omits callouts, toggles, underline and text/highlight colours. Line 125 of the same page promises those exact constructs survive an import — it guarantees fidelity for features it never says exist. |

### Bucket 2 — narrative

The launch plan's §1 decisions, applied to the landing page, the documentation
site and the README. Three findings shaped how they are applied:

- **The model-parity claim is in two places.** `concepts/models.md:10–18`
  repeats it uncarved, and its tool list silently omits the browser. Correcting
  only the landing page would leave the plan's self-identified "biggest
  liability" live on the page a reader consults when choosing a model.
- **Browser control has no landing-page section.** It is a sub-paragraph beneath
  the Skills section, positioned directly under the parity claim — which reads
  as the parity covering it. That is the one inference the plan says to prevent.
- **`concepts/browser.md` needs no change.** It already states that an agent
  with the permission on "can spend money on its schedule, without asking, while
  you are asleep". It says "cannot be undone" rather than "irreversible", which
  is why a keyword sweep initially read it as missing.

### Bucket 3 — documents that do not exist

Scoped here, written in a second pass:

- **Prompt injection.** No coverage anywhere in either repository. The launch
  plan's own risk register names it the gap that gets found within an hour.
- **Threat model.** A security document, deliberately not folded into a
  narrative pass where it would put a security review on the critical path.
- **"How it compares"** — n8n, Huginn, Khoj, Activepieces, Browser Use, Skyvern.

## `make ci` is removed

The aggregate target and every line of prose directing anyone to run it.

**Nothing in CI depends on it.** `.github/workflows/*.yml` never invokes `make`;
each job runs its steps directly. `make ci` was local convenience only, so
removing it cannot break the pipeline — verified before the change, because the
failure it would otherwise cause is one nobody sees until a merge is blocked.

**The targeted `ci-*` sub-targets stay.** The objection is to a fifteen-minute
run duplicating what the pipeline does anyway; `ci-fmt` is seconds, `ci-docs` is
what runs `docs-sync-check`, and `ci-package` is the only local way to exercise
the deb/rpm smoke test that the aggregate never included. Removing those would
remove capability rather than waste.

**The ~50 files under `docs/superpowers/plans/` and `specs/` that mention
`make ci` are not touched.** They are dated records of what was true when they
were written; rewriting them would falsify history to tidy a grep.

## Delivery

One pull request cannot span two repositories, so the "defects first" split
lands as three:

| PR | Repository | Contents |
|---|---|---|
| 1 | `rookery` | Bucket 1 defects 1–4, `make ci` removal, this spec. No narrative dependency — ships immediately. |
| 2 | `rookery-web` | Landing page, documentation, design spec, and defect 5. |
| 3 | `rookery` | README narrative pass. Branches off `main` **after** PR 1 merges — a stacked PR runs zero checks. |

## PR 1 — `rookery` defects

**Windows status.** Line 45 states what ships: no filesystem confinement on
Windows, and autostart as a Task Scheduler logon task registered by
`rookery service`. This resolves the contradiction with line 169 rather than
merely correcting one side of it.

**Browser link** added to the concepts list.

**Features graphic.** `Workspaces` and `Sandboxed` merge into one **Sandboxing**
tile — *"Separate workspaces, fully isolated."* — and the freed slot becomes
**Browser control**, *"Agents drive a real browser."* The count stays at twelve
and the 3×4 grid is untouched, so this is a content change with no layout work.

Both SVGs are regenerated and `--check` must pass: that gate fails CI when the
committed file no longer matches a fresh render, which makes the graphic a code
change rather than a prose edit. README alt text is updated to match, because it
is the only description of that image a screen reader or a text browser gets.

The merged tile's title says *Sandboxing* while its description states data
isolation. That is deliberate: process confinement is Linux-only, data isolation
holds on every platform, and the description is therefore the claim that is true
wherever the reader is running it. The launch plan's disclosure rule asks for the
two to be stated separately, and they are — in `concepts/workspaces.md`, where
there is room to do it properly. A twelve-word tile is not that place.

## PR 2 — `rookery-web`

**Hero.** The headline becomes *"Describe an agent. Watch it tested. Let it
run."*, the plan's two-sentence subhead beneath it, and the canonical paragraph
immediately below — not separated, because the hero describes the loop and only
the paragraph says what the product is. The `title` constant and the
`<meta name="description">` both still carry the previous narrative and change
with it. The release badge becomes **Apache-2.0, permanently**.

The canonical paragraph renders one step down in scale and colour from the
subhead, so it reads as the explanation rather than as a second pitch competing
with the first.

**"Your knowledge grew hands"** moves to the knowledge-base section, replacing
its current heading; the displaced *"Select a sentence. Watch it improve."*
becomes that section's subhead, where it describes the demo directly beneath it.

**Browser control becomes its own section**, carrying the security design rather
than the capability — the model never receives the password, secrets are
stripped from page text, field values, the address bar and error output,
screenshots are never given to the agent at all, the browser runs as a separate
confined process, private-address filtering covers redirects and page resources,
captchas and bot walls are refused as policy, and irreversible actions are gated
and approved while the plan is being read.

**Parity recalibration in both places.** The landing page and
`concepts/models.md` restate the claim as *tools remove the capability floor,
not the quality ceiling*, and both exclude browser control from it explicitly.
Driving a live page is open-ended, stateful and unforgiving, which is the
opposite of the narrow pre-authenticated surface that lets a small model succeed.

**Documentation edits.** The hosted-version sentence is removed from
`getting-started/what-rookery-is.md`. "A single binary" is qualified with the
optional browser download. The blunter off-Linux sandbox phrasing in
`installation/macos.md` and `installation/windows.md` is brought into line with
the two-part split `concepts/workspaces.md` already states correctly. The
knowledge-base editing list gains the four missing constructs.

**Example agents.** The bill-payment flagship goes on the landing page as the
showcase. `getting-started/first-15-minutes.md` keeps a low-stakes example,
because it is step six of setup and a first agent that spends money is the wrong
first agent — which also satisfies the plan's own instruction to pair the
flagship with something read-only so it reads as an upper bound rather than a
default.

**`docs/website-design-spec.md` §4** specifies the hero and the section order,
so it is updated alongside them; a spec the site no longer matches is worse than
no spec.

## PR 3 — `rookery` README narrative

Reordered to banner → one-liner → canonical paragraph → screenshot → Quickstart.

The licence section becomes **Apache-2.0, permanently**, with the absence of a
contributor licence agreement stated next to it. This is verified rather than
promised: `CONTRIBUTING.md` has no CLA and no DCO, so nobody can relicense the
project unilaterally. It is a structural fact about the repository, which is
what makes it worth stating at all.

**Screenshots are specified, not captured.** They need a live install with real
content, and the plan forbids real payment data in a frame that cannot be
redacted. The markup and slots land with the shot list; the images follow.

The flagship shot must show the approval gate and the `${CARD_NUMBER}`
placeholders **in the same frame**. Capability and restraint in one image reads
as competence; the same capability explained afterwards reads as recklessness
discovered.
