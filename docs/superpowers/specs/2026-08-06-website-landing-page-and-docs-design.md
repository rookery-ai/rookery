# Website: landing page and documentation — design

**Date:** 2026-08-06
**Scope:** rookery.sh — the landing page and the documentation site, as one
property, built and released from **its own repository** (`ilijad1/rookery-web`)
and never compiled into the product binary. See §10.
**Status:** Approved for writing. Spec 2 of 3.

Spec 1 (`2026-08-06-brand-identity-and-narrative-design.md`) fixed the mark, the
palette, the type and the voice. This spec consumes all of it and adds nothing to
it. Spec 3 (launch GTM — HN/PH/Reddit narrative, README as shopfront) follows.

---

## 1. Hard dependency — read this first

**The hero of this page is an install command that does not exist.**

`install.sh` and `install.ps1` are not in the repository. `CLAUDE.md` records
that they were deferred because release assets on a *private* repo need an
authenticated request, so `curl | sh` could not work — and that "everything those
installers will need is already built." Going public removes the blocker, but
somebody still has to write them.

**The landing page cannot go live before those two scripts exist and are served
from `rookery.sh`.** This is release-engineering work, explicitly out of this
spec's scope, and it is on the critical path. Everything else here can be built
in parallel; only the hero is blocked.

What exists today, for reference: goreleaser produces six binary archives plus
`.deb`/`.rpm`, `checksums.txt`, cosign signatures and an SBOM per archive, and
buildx pushes a multi-arch image to GHCR.

---

## 2. Audience and goal

**One audience: the person who will install it.** Success is a running instance.

An earlier draft of this spec proposed a two-tier page — plain-language value on
top for a non-technical reader, technical proof below for an evaluator. That was
wrong and is recorded so it is not reintroduced. If the page's job is installs,
the visitor *is* the installer; there is no second reader to design for.

Spec 1's "normal people, not programmers" governs **register**, not audience.
Plain language is how you write for anyone, including a developer: describe the
outcome, not the mechanism. It does not mean the page addresses someone who
cannot run a binary.

**Deployment story: server-first.** Rookery is meant to sit on a machine that
stays on, so agents run around the clock. Local installation is supported and
documented, but the ordering everywhere — docs, examples, screenshots — leads
with a always-on Linux host.

---

## 3. Landing page structure

```
1  Nav                mark + wordmark · Docs · GitHub
2  Hero               headline, subhead, OS-detected install block
3  The transcript      the proof — a real designer build, replayed
4  Features            one full section each, revealed on scroll:
                        4.1  Workspaces
                        4.2  Knowledge base
                        4.3  Agents
                        4.4  Skills
                        4.5  Connections   (logo grid lives here)
                        4.6  Chat
                        4.7  Notifications — inbox and chat apps
                        4.8  Bring your own model
                        4.9  Secrets
                        4.10 Scheduling and reminders
5  Install            the block again, for the reader who scrolled
6  Support the project GitHub · Star
7  Footer
```

Two sections proposed in an earlier draft — "Why it's safe to run" and "Honest
limits" — were **removed by decision**. The sandbox caveat moves into §4.1 as a
footnote. The inspectable-script link stays beside the install command (§5); it
is one line, not a section, and for this audience it is part of the conversion
path rather than a disclaimer.

---

## 4. Hero

> # Your Knowledge Grew Hands.
>
> Rookery runs agents on your own machine — around the clock.

**Why "knowledge" and not "notes".** The knowledge base is not a notes app. It
holds what the owner knows, what their agents learn and write back across runs,
and what flows in from the services they connect. "Notes" undersells it to the
size of Obsidian; "knowledge" is the accurate word for the union of all three.

Below the subhead: the install block (§5). Nothing else. No badges, no logos, no
social proof.

---

## 5. The install block

A single command visible at a time, in a tabbed block whose default tab is
**chosen by detected operating system**:

| Detected | Default tab | Command |
|---|---|---|
| Linux, macOS | **Script** | `curl -fsSL https://rookery.sh/install.sh \| sh` |
| Windows | **PowerShell** | `irm https://rookery.sh/install.ps1 \| iex` |

Other tabs, always available regardless of detection: **Docker** and **Binary**.

- **Docker, not Podman.** The repository's own examples use podman, and the
  Makefile honours whichever is installed. The site deliberately diverges to
  docker for reach. The README should follow in spec 3 so the two do not drift.
- Detection is a progressive enhancement: it picks the *default tab* only. With
  JavaScript disabled every tab is still present and reachable, and the script
  tab is the server-rendered default.
- **The script's URL is a plain link directly above the command** — "read it
  first, it's short." A `curl | sh` CTA asks more trust than almost any other
  software page, from the audience most primed to refuse it, and the pitch is
  that this software will hold their account credentials. The inspectable link is
  not a disclaimer; it is what makes the command acceptable.
- Beside it, one line naming that releases carry checksums and cosign
  signatures. The project already ships both and currently tells nobody.
- Copy-to-clipboard uses the same fallback the app uses
  (`navigator.clipboard?.writeText` → `document.execCommand`), and shows a
  visible failure state rather than a silent no-op.

---

## 6. The transcript

The one element that proves the central claim: **you describe an agent in plain
English and Rookery builds it and really tests it.** A screenshot cannot make
that claim; a paragraph asserting it is worth nothing on a page asking for
`curl | sh`.

**Form.** HTML and CSS that types itself out — not a video, not a GIF, not an
embedded player. Light, self-hosted, accessible, copy-pasteable, and readable
with animation disabled. Honours `prefers-reduced-motion` by rendering the
completed transcript immediately.

**Content.** One full agent-designer session showing: the owner's plain-English
request → the designer's two clarifying questions → the proposed plan → the build
→ a real test with real output → the saved agent.

**Capture procedure — this is load-bearing.**

1. Run a real build through the designer. Do not write a session for the page.
2. Capture the exchange verbatim, including the designer's own wording.
3. Redact secrets, tokens, addresses, hostnames and any third-party account
   identifiers. Redaction is the only permitted edit.
4. Store as JSON beside the page; the replay component renders from it.

**Re-capture rule.** If the designer's prompts or output protocol change, the
transcript is re-captured before release. A replay that has drifted from the real
product is worse than no proof at all — it is a demo that lies, on a page whose
entire argument is that its claims are checkable.

---

## 7. Feature sections

Each is a full section revealed on scroll, in this order. Every section states an
outcome first and a mechanism second, per spec 1's voice rules.

*The `4.x` labels below are positions in the page structure of §3, not
subsections of this document.*

**4.1 Workspaces.** One owner, many workspaces, each a sealed tenant: its own
knowledge, its own secrets, its own connected accounts, its own agents. Entering
one requires its master password, re-entered on every switch. Nothing crosses
between them. *Footnote here, not a section:* on Linux, agent subprocesses are
additionally confined to their own workspace with Landlock; on macOS and Windows
that confinement is not available and they run unconfined.

**4.2 Knowledge base.** Everything you know, everything your agents learn, and
what your connected services bring in — as plain markdown files you own, on your
disk. Readable and editable in Rookery or in any editor. Agents read the whole
base and write durable knowledge back across runs, so the thing gets better the
longer it runs.

**4.3 Agents.** Described in plain English, not configured. You say what you
want; Rookery asks a couple of questions, proposes a plan, builds it, tests it
against the real services, and shows you the result before saving. Links to the
transcript (§6).

**4.4 Skills.** 22 built in — reading PDFs and spreadsheets, web research,
email triage, calendar work, change detection. Plus any you create the same
conversational way agents are created.

**4.5 Connections.** **100+ services**, self-managed OAuth, no third-party
broker holding your tokens. The logo grid lives in this section, built from the
already-vendored `web/ui/src/assets/logos/`. See §11 for the count decision.

**4.6 Chat.** Talk to your knowledge and act through it — ask what you know, have
it write notes, or have it do something in a connected account, in the moment.

**4.7 Notifications — the inbox and chat apps.** *You find out the moment it
happens.* An agent finished, a connected service returned something new, a
reminder came due, something needs a decision.

The line is deliberately neutral about whether the news is good or bad. An
earlier draft said "when something breaks", which framed the whole feature as
failure alerting — but most of what Rookery reports is ordinary news from a
machine that never stops watching.

Two places it reaches you, and the section covers both:

- **The inbox**, in the app — every notification lands here, grouped by day,
  unread marked, each one linking back to whatever produced it. This is the
  durable record; it is where you look when you were away.
- **Chat apps** — Telegram, Discord or Slack, for when you are not looking at the
  app. Two-way: they carry notifications out, and you can talk to your agents
  back through them. All three connect **outbound** — the bot dials out, so
  nothing listens on an open port and it works from behind a home firewall
  without forwarding anything.

**4.8 Bring your own model.** Works with the coder CLI you already use, or talks
directly to the model provider of your choice — hosted or **running locally on
your own hardware**. Nothing forces a hosted model, and nothing ties you to one
vendor.

*No provider counts or vendor names on this page:* the list grows continuously,
so any figure or enumeration goes stale and becomes a copy-maintenance burden.
The **documentation** carries the current list; the landing page states the
capability.

**4.9 Secrets.** Credentials encrypted at rest, decrypted only into the process
that needs them, scoped to one workspace, never handed to a third party.

**4.10 Scheduling and reminders.** Agents run on a schedule you describe in
words, or when you ask. Reminders in plain language — "remind me in 10 minutes to
call the doctor."

---

## 8. Support the project

Replaces the earlier draft's second install block position. GitHub link, star
call, and a plain statement that the project is Apache-2.0 and self-hostable
forever. No sponsorship ask at launch.

---

## 9. Documentation

Documentation is **mandatory**, not a follow-up. It shares the site's
infrastructure, navigation and design, and lives at `rookery.sh/docs`.

This spec defines the information architecture and a per-page outline. **Writing
the prose is the implementation work**, not part of this document.

### Ordering: server-first

Installation leads with an always-on Linux host, matching the deployment intent
(§2), even though the hero subhead says "your own machine."

### Information architecture

```
Getting started
  What Rookery is
  Your first 15 minutes          install → owner → workspace → first agent
  Choosing a model               CLI coder vs direct API vs local model

Installation
  Linux server (recommended)     script, systemd user unit, enable-linger
  Docker
  macOS
  Windows
  Binary and .deb/.rpm           checksums, cosign verification
  Reaching it from outside       ROOKERY_PUBLIC_URL, TLS, why OAuth needs it

Concepts and features            (the sequence, in order)
  1  Workspaces                  owner vs workspace, master passwords, isolation
  2  Knowledge base              layout, memory files, editing, search, backlinks
  3  Agents                      designing, editing, running, schedules, state, logs
  4  Skills                      the 22 built in, creating, importing
  5  Chat                        one-off chat, knowledge access, acting through connections
  6  Secrets                     how they are stored, how agents receive them
  7  Connections                 connecting a service, own OAuth apps, per-agent binding
  8  Notifications               the inbox; Telegram, Discord, Slack; what gets sent
  9  Scheduling and reminders
  10 Backup and restore          snapshots, destinations, restoring on new hardware
  11 Models                      CLI coders, hosted providers, running a local
                                 model. The CURRENT enumerated provider list
                                 lives here, never on the landing page.

Operations
  Configuration                  every ROOKERY_* variable, defaults, effects
  Health and troubleshooting     /healthz, host tools, common failures
  Security model                 sandbox, encryption at rest, network egress, limits

Reference
  CLI commands
  Connected services             the enumerated provider list
```

### Per-page outline rule

Every page: what it is in one sentence → why you would use it → the shortest
working example → the options → what goes wrong and how to tell.

### Accuracy rule — the important one

**Every factual claim in the documentation is verified against source at the time
of writing.** Not against `CLAUDE.md`, and not against `README.md`.

This is not a theoretical risk. `README.md` currently claims 45 providers and 272
actions; the real counts, from the YAML in `internal/connectors/`, are **91 and
471**. A landing surface has been understating the product by half. `CLAUDE.md`
is a design record, not a specification of current behaviour, and it will drift
the same way.

Claims requiring source verification include: environment-variable names and
defaults, command syntax, supported platforms, provider and action counts, skill
counts and file paths.

### Voice: the documentation carve-out

Spec 1 binds brand copy to the blocklist enforced at
`internal/prompts/prompts.go:783` — no *cron, script, vault, webhook, endpoint,
API key, JSON, shell, Python* used unexplained.

**Documentation is carved out, deliberately and narrowly.** Reference docs need
every one of those words; install instructions without "shell" are unusable. The
blocklist's own wording is "FORBIDDEN terms to use **unexplained**", and
documentation explains by nature.

The rule:

- **Landing page and product UI** — bound by the blocklist in full.
- **Documentation** — uses the real term, and defines it on first use per page.

Without this written down, someone applies the rule literally and produces
install docs that cannot instruct anyone.

---

## 10. Build, hosting and privacy

### The website is its own repository and its own application

**`ilijad1/rookery-web`, separate from `ilijad1/rookery`.** It is never compiled
into the binary, never embedded with `go:embed`, and shares no build with the
product.

An earlier draft of this spec put the site at `/site` inside the product
repository. That is superseded. The reasoning for separating:

- The binary stays exactly what it claims to be. `web/ui` is already embedded in
  it; a marketing site has no business travelling inside a self-hosted server
  someone runs on their own hardware.
- The site's release cadence is unrelated to the product's. A typo fix should
  not touch a repository whose merges drive release-please and cut versioned
  binaries.
- The site can be public before the product repository is.

**Framework: Astro, with Starlight for the documentation.** This is a change of
recommendation, forced by documentation becoming mandatory (§9). Plain HTML was
the right call for a single landing page; it is the wrong call for a ~25-page
sequenced documentation site, where the hand-maintained navigation, previous/next
links and search become the whole cost. Starlight supplies all three, outputs
static files, and ships no JavaScript for content pages by default — so the
no-CDN, no-tracker discipline below survives intact.

### The vendoring problem separation creates

Spec 1 is emphatic that the UI font has **one copy and two consumers**, because a
second checked-in copy drifts silently. A separate repository needs its own copy
of both the font and the brand logos, which reintroduces exactly that risk.

How each is handled, and why they differ:

- **Brand logos** — the website runs its **own copy of
  `scripts/vendor-brand-logos.sh`**. These assets are *generated from upstream*,
  not hand-authored, so regenerating is the correct operation and there is no
  drift to speak of. The script's rules from `CLAUDE.md` carry over verbatim:
  never hand-edit the output, `inline_class_styles` before stripping `<style>`,
  and check `git status` after a run.
- **Inter** — **copied**, with provenance recorded in the file's directory:
  canonical source is `internal/fonts/InterVariable.woff2` in the product
  repository. A copy is unavoidable across a repository boundary; a submodule for
  one 48 KB file costs more than it saves. The mitigating rule: the website's copy
  is refreshed from the product repository whenever the product's is, and the
  provenance note names that obligation.
- **The Weave mark and the palette** — copied as small literal values (SVG path,
  hex tokens) from spec 1. These are the identity; they are meant to be stable,
  and spec 1 is the canonical record.

**Fonts self-hosted.** No CDN import — the product refuses one and the site holds
the same line.

### Cross-repository consequences

- **The spec stays canonical in the product repository**, at
  `docs/superpowers/specs/`, alongside specs 1 and 3. The website repository's
  README links to it rather than copying it — a duplicated living document is the
  same drift trap as a duplicated font, with none of the excuse.
- **The connections count** (§11) cannot be generated from `internal/connectors/`
  any more, because the website cannot see it. This makes the "100+" decision
  moot as an engineering question: the figure is hand-written either way now.
- **Docs accuracy** (§9) gets harder, not easier: a writer in the website
  repository cannot grep the source to check a default. The rule stands, and the
  practical answer is that whoever writes a page has both repositories checked
  out. Worth stating plainly rather than discovering.

**No analytics. No cookies. No third-party requests of any kind.** Stated on the
page in plain words: *"No trackers on this site."* A privacy-first, self-hosted
product running third-party analytics is a contradiction this audience will
screenshot. This is brand copy, not a compliance notice.

---

## 11. Recorded decisions

**"100+ connections" in the hero — deliberate, against recommendation.**

The true count today is **91**, verified by counting
`internal/connectors/connectors/*.yaml`. The recommendation was to generate the
number at build time from the registry so the page states the real figure and
rises on its own. The owner chose to write **100+** now, having been shown that
count and the conflict.

Recorded plainly so it is a decision and not an accident:

- Spec 1's voice rules say never round a claim up. This is a knowing exception.
- It becomes literally true at 100 providers — **nine more than today**.
- The audience for a self-hosted tool does check counts.

**Consequence to settle before writing docs:** the Reference → *Connected
services* page enumerates providers. A reader who sees "100+" in the hero and
counts 91 in the docs sees both numbers on the same property. Either that page
also states "100+" without enumerating, or the mismatch is accepted knowingly.
The default this spec assumes is that **the docs page enumerates truthfully**,
because a reference page that will not list what it counts is worse than a
hero that rounds. Flag if the other way is preferred.

**No model-provider counts or vendor names on the landing page.** Chosen
directly: the provider list grows continuously, so any figure or enumeration is
a copy-maintenance burden that goes stale between releases. The page states the
capability — works with your CLI coder, hosted providers, or a local model on
your own hardware — and the documentation (Concepts → *Models*) carries the
current list.

Noted alongside the "100+" decision above so the two are visible together: the
connections count is stated as a rounded-up figure, and the model count is not
stated at all. Both are deliberate owner decisions, made for different reasons —
reach in the first case, staleness-avoidance in the second.

**"Machine" not "server" in the subhead.** Chosen directly, despite the
deployment story being server-first (§2). "Machine" is the broader, friendlier
word and covers a home server, a VPS and a laptop alike. Docs ordering stays
server-first regardless.

**Docker not Podman** in all site examples, for reach (§5).

---

## 12. Out of scope

- `install.sh` / `install.ps1` — blocking dependency, release engineering (§1).
- Documentation prose — this spec defines IA and outlines only.
- Pricing, cloud waitlist, email capture, testimonials, company logos.
- Open Graph and social card artwork — needs the final headline; produce after
  the page is built.
- README rewrite — spec 3, where the stale 45/272 counts get fixed.

---

## 13. Open questions

**Where the site is deployed.** `/site` is static, so any host works. Not decided
here because it does not affect what gets built.

**Whether the docs enumerate providers truthfully against a "100+" hero** — §11.
