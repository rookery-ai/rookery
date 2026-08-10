# rookery.cloud Migration and README Restructure — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace every `rookery.sh` reference with `rookery.cloud` across both repositories, and restructure the product README onto the website's section order with hand-authored SVG assets.

**Architecture:** Three pull requests across two repositories. PR 1a (product rename) and PR 1b (website rename) are independent of everything and ship immediately — five of those references are live outbound identifiers pointing at a domain someone else now controls. PR 2 (README + SVGs) is blocked on an external merge and ships after.

**Tech Stack:** Go 1.26, YAML connector manifests, Astro 5 + Starlight + React (website), hand-authored SVG, Python 3 (the docs-sync checker).

## Global Constraints

- **Domain:** `rookery.cloud`. Never `rookery.sh`. The completion bar is that `grep -rn 'rookery\.sh'` returns nothing in either repository, excluding `.git`, `node_modules`, `dist`, `.astro` and sibling worktrees.
- **Historical documents are in scope.** The owner chose a full rewrite including `docs/superpowers/specs/2026-07-29-rookery-rename-design.md` and `docs/superpowers/plans/2026-07-29-rookery-rename.md`, against a recommendation to leave the record intact. Do not re-litigate this.
- **Measured counts, never copied.** 91 connector providers, 471 curated actions, 22 bundled core skills, 7 user-facing CLI commands, 9 public `ROOKERY_` variables. Re-measure each at implementation time with the commands in Task 7; do not trust this line, the old README, or `CLAUDE.md`.
- **No `N+` counts.** Write `91 services`, never `100+ services`. `check_inflated` in `scripts/check-docs-sync.py` rejects `N+` against the nouns `services` and `supported`.
- **No invented CLI commands.** The seven real ones are `serve`, `owner`, `backup`, `connector`, `kb`, `healthcheck`, `version`. There is no `rookery db migrate` — migrations apply automatically when the database opens.
- **Conventional Commits**, `type(scope): summary`. Branch off `main`; never commit to `main` directly.
- **Brand voice:** prose says "Rookery" capitalised because it is a proper noun. This is style only — the `check_cli` gate that once justified it is being narrowed to code contexts by its author.
- **SVG:** light-committed palette with an explicit painted background; presentation attributes, not `<style>` blocks; live `<text>` on a system font stack. Brand tokens: `--bone #ece5db`, `--paper #f8f4ee`, `--bark #211d1a`, `--ember #a94c1c`, `--ember-soft #f6e6dc`, `--dusk #46405a`, `--stone #6f6760`, `--line #d9cfc2`.

## File Structure

**Product repository** (`/home/rookie/rookery`)

| Path | Responsibility | PR |
|---|---|---|
| `internal/connectors/providers/{wikipedia,openstreetmap,openlibrary,openfoodfacts}.yaml` | Outbound `User-Agent` identifying Rookery to policy-bound APIs | 1a |
| `internal/llm/openai.go:61` | OpenRouter `HTTP-Referer` | 1a |
| `README.md:66`, `CLAUDE.md:149` | Documented `ROOKERY_PUBLIC_URL` example | 1a |
| `docs/superpowers/{specs,plans}/2026-07-29-rookery-rename*.md` | Historical record | 1a |
| `docs/assets/hero-banner.svg` | README hero: mark, wordmark, tagline | 2 |
| `docs/assets/architecture.svg` | One diagram: everything inside one process on one machine | 2 |
| `README.md` | Full restructure onto site section order | 2 |
| `scripts/check-docs-sync.py` | `CLAIMS` re-pinned to the new wording | 2 |

**Website repository** (`/home/rookie/rookery-web`)

| Path | Responsibility | PR |
|---|---|---|
| `astro.config.mjs:13` | `site:` — drives canonical URLs and the sitemap | 1b |
| `src/components/InstallBlock.tsx` | The install commands visitors copy (4 refs) | 1b |
| `src/components/Transcript.tsx:25` | Demo prose | 1b |
| `src/content/docs/docs/installation/{linux-server,macos,windows}.md` | Install docs | 1b |
| `src/content/docs/docs/getting-started/first-15-minutes.md` | Install docs + one prose example | 1b |
| `README.md`, `docs/website-design-spec.md` | Repository docs and historical spec | 1b |

---

# PR 1a — Product repository rename

Branch: `fix/rookery-cloud-domain`. No dependency on any other work.

### Task 1: Swap the live outbound identifiers

These five references are sent to third parties on every request. Wikimedia, Nominatim, Open Library and Open Food Facts all block or throttle clients that do not identify themselves, and Wikimedia's policy expects the URL to be contactable. This is the urgent part of the whole plan.

**Files:**
- Modify: `internal/connectors/providers/wikipedia.yaml:10`
- Modify: `internal/connectors/providers/openstreetmap.yaml:9`
- Modify: `internal/connectors/providers/openlibrary.yaml:9`
- Modify: `internal/connectors/providers/openfoodfacts.yaml:7`
- Modify: `internal/llm/openai.go:61`
- Test: `internal/connectors/wave2_test.go`, `internal/connectors/wave4_test.go` (existing, not modified)

**Interfaces:**
- Consumes: nothing.
- Produces: nothing consumed by later tasks. Existing tests `TestWikipediaSendsAUserAgent` and `TestPolicyBoundProvidersSendAUserAgent` already cover these files and assert only `strings.Contains(ua, "Rookery")`, so they pass before and after. They are the regression net, not a new test.

- [ ] **Step 1: Confirm the existing tests pass before any change**

```bash
go test ./internal/connectors/... -run 'UserAgent|SendsAUserAgent' -count=1 -v
```

Expected: PASS. This establishes the baseline — if these are red beforehand, stop and investigate rather than attributing it to the rename.

- [ ] **Step 2: Swap the four connector User-Agents**

Each of the four files carries the identical header value. Replace `https://rookery.sh` with `https://rookery.cloud` in each:

```yaml
static_headers:
  User-Agent: "Rookery/1.0 (https://rookery.cloud; self-hosted personal assistant)"
```

Apply to all four:

```bash
sed -i 's|https://rookery\.sh|https://rookery.cloud|g' \
  internal/connectors/providers/wikipedia.yaml \
  internal/connectors/providers/openstreetmap.yaml \
  internal/connectors/providers/openlibrary.yaml \
  internal/connectors/providers/openfoodfacts.yaml
```

- [ ] **Step 3: Swap the OpenRouter referer**

In `internal/llm/openai.go`, line 61:

```go
		headers["HTTP-Referer"] = "https://rookery.cloud"
```

- [ ] **Step 4: Verify no live reference remains**

```bash
grep -rn 'rookery\.sh' internal/
```

Expected: no output.

- [ ] **Step 5: Run the connector tests**

```bash
go test ./internal/connectors/... ./internal/llm/... -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/connectors/providers/wikipedia.yaml \
        internal/connectors/providers/openstreetmap.yaml \
        internal/connectors/providers/openlibrary.yaml \
        internal/connectors/providers/openfoodfacts.yaml \
        internal/llm/openai.go
git commit -m "fix(connectors): point outbound identifiers at rookery.cloud

The rookery.sh domain was registered by someone else. Four connector
User-Agents and the OpenRouter HTTP-Referer sent that domain to third
parties on every request, so each one now advertises a host we do not
control. Wikimedia's policy in particular expects the URL in a
User-Agent to be contactable.

The existing User-Agent tests assert only that the string identifies
Rookery, so they cover this unchanged."
```

### Task 2: Swap the product documentation

**Files:**
- Modify: `README.md:66`
- Modify: `CLAUDE.md:149`
- Modify: `docs/superpowers/plans/2026-07-29-rookery-rename.md:991,1260,1312,1347`
- Modify: `docs/superpowers/specs/2026-07-29-rookery-rename-design.md:58,63,106,267`

**Interfaces:**
- Consumes: nothing.
- Produces: a clean `grep` in the product repository, which Task 9 re-asserts.

- [ ] **Step 1: Swap README and CLAUDE.md**

`README.md:66` becomes:

```markdown
a real hostname — `rookery.cloud` is the documented example — or `http://localhost`.
```

`CLAUDE.md:149` becomes:

```markdown
`ROOKERY_`. The project domain is **rookery.cloud** — it is the documented
```

The surrounding rationale in `CLAUDE.md` — that OAuth providers reject redirect URIs on non-public hostnames, so a `.lan` address fails Google's validation — is about public TLDs generally and holds for `.cloud` unchanged. Do not reword it.

- [ ] **Step 2: Swap the two historical documents**

```bash
sed -i 's|rookery\.sh|rookery.cloud|g' \
  docs/superpowers/plans/2026-07-29-rookery-rename.md \
  docs/superpowers/specs/2026-07-29-rookery-rename-design.md
```

This is deliberate and was chosen by the owner. It leaves the 2026-07-29 design reading as though `.cloud` were weighed against `.com`/`.dev`/`.ai`/`.io`/`.app`/`.org`/`.net`, which is not what happened. The 2026-08-10 spec is the supersession record.

- [ ] **Step 3: Verify the product repository is clean**

```bash
grep -rn 'rookery\.sh' . --exclude-dir=.git --exclude-dir=node_modules --exclude-dir=bin
```

Expected: no output.

- [ ] **Step 4: Confirm nothing else broke**

```bash
go build ./... && go test ./... -count=1 -timeout 300s
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add README.md CLAUDE.md docs/superpowers/
git commit -m "docs: rename the project domain to rookery.cloud

Includes the two 2026-07-29 rename documents. Rewriting a historical
record is normally wrong -- those documents describe a decision that was
true when it was made, including the reasoning for preferring .sh -- but
the owner's stated bar is that grep for the old domain returns nothing.
The 2026-08-10 spec records the supersession and why .cloud was not
chosen on the merits .sh was."
```

- [ ] **Step 6: Open PR 1a**

Title: `fix(connectors): point outbound identifiers at rookery.cloud`

The title must itself be a valid Conventional Commit — merges are squashes and release-please reads it.

---

# PR 1b — Website repository rename

Branch: `fix/rookery-cloud-domain` in `/home/rookie/rookery-web`. Independent of PR 1a.

**Coordination note.** A parallel session owns `src/pages/index.astro` lines 380 and 395, `src/content/docs/docs/reference/connected-services.md`, a new `reference/api.md`, and a sidebar entry in `astro.config.mjs`. It has agreed to take the `astro.config.mjs` conflict on its side, since this PR lands first. Do not touch its files.

### Task 3: Swap the site's live references

**Files:**
- Modify: `astro.config.mjs:13`
- Modify: `src/components/InstallBlock.tsx:21,24,32,35`

**Interfaces:**
- Consumes: nothing.
- Produces: a rebuilt sitemap carrying `rookery.cloud`, asserted in Step 4.

- [ ] **Step 1: Swap the canonical site URL**

`astro.config.mjs` line 13. This field drives canonical `<link>` tags and the generated sitemap, so it is functional, not cosmetic:

```js
  site: "https://rookery.cloud",
```

- [ ] **Step 2: Swap the install commands**

`src/components/InstallBlock.tsx` — four references across two tab definitions:

```tsx
  {
    id: "script",
    label: "Linux / macOS",
    icons: [LinuxIcon, AppleIcon],
    command: "curl -fsSL https://rookery.cloud/install.sh | sh",
    note: {
      text: "Read it first — it's short:",
      href: "https://rookery.cloud/install.sh",
      linkText: "install.sh",
    },
  },
  {
    id: "powershell",
    label: "Windows",
    icons: [WindowsIcon],
    command: "irm https://rookery.cloud/install.ps1 | iex",
    note: {
      text: "Read it first — it's short:",
      href: "https://rookery.cloud/install.ps1",
      linkText: "install.ps1",
    },
  },
```

Leave the `docker` and `binary` tabs untouched — they carry no domain.

- [ ] **Step 3: Build the site**

```bash
cd /home/rookie/rookery-web && npm run build
```

Expected: build succeeds.

- [ ] **Step 4: Assert the built output carries the new domain**

```bash
grep -rn 'rookery\.sh' dist/ | head
grep -c 'rookery\.cloud' dist/sitemap-0.xml
```

Expected: the first returns nothing; the second returns a non-zero count. If `dist/sitemap-0.xml` does not exist, list `dist/sitemap*` and check the file that does — the sitemap integration names files by index.

- [ ] **Step 5: Commit**

```bash
git add astro.config.mjs src/components/InstallBlock.tsx
git commit -m "fix: point the site and install commands at rookery.cloud

site: drives canonical URLs and the sitemap, so this is functional
rather than cosmetic. The install commands are what a visitor copies."
```

### Task 4: Swap the site's documentation and demo prose

**Files:**
- Modify: `src/content/docs/docs/installation/linux-server.md:13`
- Modify: `src/content/docs/docs/installation/macos.md:8`
- Modify: `src/content/docs/docs/installation/windows.md:10`
- Modify: `src/content/docs/docs/getting-started/first-15-minutes.md:13,19,76`
- Modify: `src/components/Transcript.tsx:25`
- Modify: `README.md:3,119`
- Modify: `docs/website-design-spec.md:14,36,120,121,326`

**Interfaces:**
- Consumes: nothing.
- Produces: a clean `grep` across the website repository.

- [ ] **Step 1: Swap the installation pages**

Three files, one command each, all inside fenced code blocks:

```bash
sed -i 's|https://rookery\.sh|https://rookery.cloud|g' \
  src/content/docs/docs/installation/linux-server.md \
  src/content/docs/docs/installation/macos.md \
  src/content/docs/docs/installation/windows.md
```

- [ ] **Step 2: Swap first-15-minutes, including the prose example**

Lines 13 and 19 are install commands. Line 76 is different — it is the sample agent request a reader is invited to type, and it names the project's own site as the thing to monitor:

```markdown
> Every morning, check whether rookery.cloud is reachable and tell me only if it
> isn't.
```

```bash
sed -i 's|rookery\.sh|rookery.cloud|g' \
  src/content/docs/docs/getting-started/first-15-minutes.md
```

- [ ] **Step 3: Swap the transcript demo line**

`src/components/Transcript.tsx` line 25:

```tsx
  { from: "you", text: "rookery.cloud and my blog. Only when something is wrong." },
```

Change the domain string only. **Do not touch anything else in this file.** Its header comment records that the whole transcript is placeholder content requiring a verbatim capture from a real designer build before launch — that obligation is real, is out of scope here, and has been raised with the owner separately.

- [ ] **Step 4: Swap the repository docs**

```bash
sed -i 's|rookery\.sh|rookery.cloud|g' README.md docs/website-design-spec.md
```

In `docs/website-design-spec.md` lines 120–121 the domain sits inside a markdown table with escaped pipes; `sed` on the domain substring alone preserves the escaping. Verify by eye after.

- [ ] **Step 5: Verify the website repository is clean**

```bash
grep -rn 'rookery\.sh' . --exclude-dir=.git --exclude-dir=node_modules --exclude-dir=dist --exclude-dir=.astro
```

Expected: no output.

- [ ] **Step 6: Rebuild and typecheck**

```bash
npm run build
```

Expected: build succeeds. The `Transcript.tsx` edit is inside a typed `Turn` literal, so a broken quote would fail here.

- [ ] **Step 7: Commit and open PR 1b**

```bash
git add -A
git commit -m "docs: rename the site domain to rookery.cloud

Installation pages, the first-15-minutes walkthrough including its
sample agent request, the placeholder transcript's demo line, and the
repository's own README and design spec."
```

Title: `fix: point the site and its documentation at rookery.cloud`

---

# PR 2 — README restructure and assets

**Blocked.** Do not start until the parallel session's documentation-accuracy PR has merged into `main`. That PR corrects the README's provider and action counts and adds `scripts/check-docs-sync.py` to `make ci`. Starting earlier means hand-merging count corrections this work would immediately overwrite.

**Precondition check before Task 5:**

```bash
git -C /home/rookie/rookery fetch origin && git -C /home/rookie/rookery log origin/main --oneline -5
ls /home/rookie/rookery/scripts/check-docs-sync.py
```

Proceed only when `check-docs-sync.py` is present on `origin/main`. Then branch: `docs/readme-restructure`.

### Task 5: Author the hero banner

**Files:**
- Create: `docs/assets/hero-banner.svg`

**Interfaces:**
- Consumes: the brand tokens in Global Constraints, and the glyph geometry from the website's `src/assets/mark.svg` (three strokes: `M11 8.5h10`, `M7.5 14h17`, and the bowl curve).
- Produces: `docs/assets/hero-banner.svg`, referenced by Task 7's README as `<img src="docs/assets/hero-banner.svg">`.

- [ ] **Step 1: Create the assets directory**

```bash
mkdir -p docs/assets
```

- [ ] **Step 2: Write the banner**

Create `docs/assets/hero-banner.svg` with exactly this content:

```svg
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1280 340" width="1280" height="340" role="img" aria-labelledby="t">
  <title id="t">Rookery — your knowledge grew hands</title>
  <rect width="1280" height="340" fill="#ece5db"/>
  <circle cx="1168" cy="34" r="286" fill="#a94c1c" opacity="0.09"/>
  <circle cx="86" cy="332" r="212" fill="#46405a" opacity="0.07"/>
  <g transform="translate(92 74) scale(2.6)" fill="none" stroke="#a94c1c" stroke-width="3.1" stroke-linecap="round">
    <path d="M11 8.5h10"/>
    <path d="M7.5 14h17"/>
    <path d="M4.5 19.5C4.5 26 9.5 29 16 29S27.5 26 27.5 19.5"/>
  </g>
  <text x="200" y="148" font-family="ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif" font-size="86" font-weight="600" letter-spacing="-4" fill="#211d1a">rookery</text>
  <text x="94" y="236" font-family="ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif" font-size="40" font-weight="600" letter-spacing="-1" fill="#211d1a">Your knowledge grew hands.</text>
  <text x="94" y="280" font-family="ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif" font-size="22" fill="#6f6760">Self-hosted AI agents that run on your own machine, around the clock.</text>
  <rect x="94" y="302" width="72" height="4" rx="2" fill="#a94c1c"/>
</svg>
```

Note the painted `<rect>` background: without it the banner would be transparent and the warm palette would read as muddy grey against GitHub's dark theme.

- [ ] **Step 3: Verify it is well-formed XML**

```bash
python3 -c "import xml.dom.minidom;xml.dom.minidom.parse('docs/assets/hero-banner.svg');print('ok')"
```

Expected: `ok`. There is no renderer on this host, so well-formedness plus a visual read of the coordinates is the available check; confirm appearance in the PR preview on GitHub.

- [ ] **Step 4: Confirm no remote dependencies**

```bash
grep -nE 'https?://(?!www\.w3\.org)' docs/assets/hero-banner.svg || echo "no remote refs"
grep -n '<style' docs/assets/hero-banner.svg || echo "no style block"
```

Expected: `no remote refs` and `no style block`. If `grep -E` rejects the lookahead, use `grep -n 'http' docs/assets/hero-banner.svg` and confirm the only hit is the `xmlns`.

- [ ] **Step 5: Commit**

```bash
git add docs/assets/hero-banner.svg
git commit -m "docs: add the README hero banner

Hand-authored SVG on the website's own palette so the README and the
site read as one product. Live text on a system font stack: this host
has no font tooling to outline the wordmark, and hand-authoring glyphs
as beziers with no renderer to check them would be worse."
```

### Task 6: Author the architecture diagram

**Files:**
- Create: `docs/assets/architecture.svg`

**Interfaces:**
- Consumes: the brand tokens in Global Constraints.
- Produces: `docs/assets/architecture.svg`, referenced by Task 7's README.

- [ ] **Step 1: Write the diagram**

Create `docs/assets/architecture.svg` with exactly this content. It shows the one thing the prose cannot: that everything is inside a single process on a single machine, with the connector layer as the only thing reaching outward.

```svg
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1200 690" width="1200" height="690" role="img" aria-labelledby="t">
  <title id="t">Rookery architecture: chat platforms and browser reach one binary on your machine, which holds isolated workspaces and reaches out through the connector layer</title>
  <rect width="1200" height="690" fill="#ece5db"/>
  <g font-family="ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif">

    <text x="600" y="34" font-size="13" font-weight="600" letter-spacing="2.4" fill="#6f6760" text-anchor="middle">YOU</text>
    <g fill="#f8f4ee" stroke="#d9cfc2">
      <rect x="273" y="48" width="150" height="46" rx="23"/>
      <rect x="441" y="48" width="150" height="46" rx="23"/>
      <rect x="609" y="48" width="150" height="46" rx="23"/>
      <rect x="777" y="48" width="150" height="46" rx="23"/>
    </g>
    <g font-size="17" fill="#211d1a" text-anchor="middle">
      <text x="348" y="77">Telegram</text>
      <text x="516" y="77">Discord</text>
      <text x="684" y="77">Slack</text>
      <text x="852" y="77">Browser</text>
    </g>

    <path d="M600 100V136" stroke="#a94c1c" stroke-width="2.5"/>
    <path d="M593 130l7 12 7-12z" fill="#a94c1c"/>

    <rect x="70" y="150" width="1060" height="392" rx="24" fill="#f8f4ee" stroke="#d9cfc2" stroke-width="1.5"/>
    <text x="98" y="186" font-size="14" font-weight="600" letter-spacing="1.6" fill="#a94c1c">YOUR MACHINE — ONE BINARY, ONE PROCESS</text>

    <rect x="98" y="204" width="1004" height="312" rx="18" fill="none" stroke="#d9cfc2" stroke-dasharray="6 5"/>
    <text x="122" y="234" font-size="15" fill="#6f6760">workspace — an isolated tenant, entered with its own password</text>

    <g fill="#ece5db" stroke="#d9cfc2">
      <rect x="122" y="252" width="228" height="150" rx="14"/>
      <rect x="374" y="252" width="228" height="150" rx="14"/>
      <rect x="626" y="252" width="228" height="150" rx="14"/>
      <rect x="878" y="252" width="200" height="150" rx="14"/>
    </g>
    <g font-size="18" font-weight="600" fill="#211d1a">
      <text x="146" y="288">Knowledge base</text>
      <text x="398" y="288">Agents</text>
      <text x="650" y="288">Coder</text>
      <text x="902" y="288">Secrets</text>
    </g>
    <g font-size="14.5" fill="#6f6760">
      <text x="146" y="316">Markdown vault</text>
      <text x="146" y="338">on your own disk.</text>
      <text x="146" y="360">Agents read and</text>
      <text x="146" y="382">write it directly.</text>
      <text x="398" y="316">Built by describing</text>
      <text x="398" y="338">them. Run on a</text>
      <text x="398" y="360">schedule, on demand,</text>
      <text x="398" y="382">or when you ask.</text>
      <text x="650" y="316">Your CLI tool or a</text>
      <text x="650" y="338">provider API. Confined</text>
      <text x="650" y="360">with Landlock on</text>
      <text x="650" y="382">Linux.</text>
      <text x="902" y="316">Encrypted at rest,</text>
      <text x="902" y="338">unlocked only into</text>
      <text x="902" y="360">the thing that</text>
      <text x="902" y="382">needs them.</text>
    </g>

    <text x="122" y="440" font-size="15" fill="#6f6760">SQLite is the system of record — chats, schedules, runs. Nothing leaves this box unless you connect something.</text>
    <text x="122" y="484" font-size="15" fill="#6f6760">Other workspaces sit alongside, sealed: separate vault, separate credentials, separate agents.</text>

    <path d="M600 546V582" stroke="#a94c1c" stroke-width="2.5"/>
    <path d="M593 576l7 12 7-12z" fill="#a94c1c"/>

    <rect x="150" y="592" width="900" height="76" rx="20" fill="#f6e6dc" stroke="#a94c1c" stroke-opacity="0.25"/>
    <text x="600" y="624" font-size="19" font-weight="600" fill="#211d1a" text-anchor="middle">91 connected services — direct OAuth, credentials you own</text>
    <text x="600" y="650" font-size="14.5" fill="#6f6760" text-anchor="middle">Google · GitHub · Notion · Slack · Home Assistant · Spotify · Todoist · and 84 more</text>
  </g>
</svg>
```

- [ ] **Step 2: Verify it is well-formed XML**

```bash
python3 -c "import xml.dom.minidom;xml.dom.minidom.parse('docs/assets/architecture.svg');print('ok')"
```

Expected: `ok`.

- [ ] **Step 3: Re-measure the provider count the diagram claims**

The diagram hardcodes `91` and `84 more`. Confirm before committing:

```bash
ls internal/connectors/providers/*.yaml | wc -l
```

If this is not `91`, update both numbers in the diagram (`N connected services` and `and N-7 more`) to match.

- [ ] **Step 4: Commit**

```bash
git add docs/assets/architecture.svg
git commit -m "docs: add the README architecture diagram

Shows what the prose cannot: everything inside one process on one
machine, with the connector layer as the only thing reaching outward."
```

### Task 7: Restructure the README

**Files:**
- Modify: `README.md` (full rewrite of structure; Quickstart, Configuration, Platform support, Health and License are preserved in substance and re-ordered)

**Interfaces:**
- Consumes: `docs/assets/hero-banner.svg` and `docs/assets/architecture.svg` from Tasks 5 and 6.
- Produces: the four sentences Task 8 re-pins its regexes against. Task 8 depends on the exact wording written here, so do not reword after Task 8 without re-running it.

- [ ] **Step 1: Re-measure every count**

Do not trust the plan, the old README, or `CLAUDE.md` — the README was previously wrong by half.

```bash
ls internal/connectors/providers/*.yaml | wc -l                          # providers
grep -h '^  - name:' internal/connectors/connectors/*.yaml | wc -l       # actions
ls -d internal/skilllibrary/skills/*/ | wc -l                            # core skills
grep -rhoE 'ROOKERY_[A-Z_]+' --include='*.go' internal/ cmd/ | sort -u   # env vars
```

Expected at time of writing: 91, 471, 22, and 14 names of which 9 are public. The five internal ones — `ROOKERY_BUILD_PHASE`, `ROOKERY_CONNECTOR_URL`, `ROOKERY_CONNECTOR_TOKEN`, `ROOKERY_KB_URL`, `ROOKERY_KB_TOKEN` — are written into a subprocess environment by the host, never operator-set. `ROOKERY_CLAUDE_BIN` **is** public: it is read with `os.Getenv` in `internal/config/config.go:133`, exactly as `ROOKERY_HOST` is.

- [ ] **Step 2: Write the new README**

Replace `README.md` entirely with the following. Substitute any count that Step 1 measured differently.

````markdown
<p align="center">
  <img src="docs/assets/hero-banner.svg" alt="Rookery — your knowledge grew hands" width="100%">
</p>

<p align="center">
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/badge/license-Apache--2.0-a94c1c"></a>
  <a href="https://github.com/ilijad1/rookery/releases"><img alt="Release" src="https://img.shields.io/github/v/release/ilijad1/rookery?color=a94c1c"></a>
  <a href="https://github.com/ilijad1/rookery/actions"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/ilijad1/rookery/pr.yml?branch=main"></a>
  <a href="https://github.com/ilijad1/rookery/pkgs/container/rookery"><img alt="Container" src="https://img.shields.io/badge/ghcr.io-rookery-a94c1c"></a>
</p>

# Rookery

**Self-hosted AI agents that run on your own machine, around the clock.**

Rookery is a single binary. It keeps your knowledge as plain markdown on your
own disk, builds agents from a conversation rather than a config file, reaches
91 external services with credentials you own, and talks to you on Telegram,
Discord or Slack. The database is SQLite, secrets are encrypted at rest, and
coder subprocesses are confined with Landlock on Linux.

Full documentation lives at **[rookery.cloud/docs](https://rookery.cloud/docs)**.

## Quickstart

```bash
curl -fsSL https://rookery.cloud/install.sh | sh
```

On Windows, in PowerShell:

```powershell
irm https://rookery.cloud/install.ps1 | iex
```

Then create the owner account and start the server:

```bash
rookery owner bootstrap -u <username> -p <password>
rookery serve
```

Open `http://localhost:8080`, log in, and create your first workspace.

<details>
<summary>Build from source instead</summary>

Requires Go 1.26 and Node 24.

```bash
make build
./bin/rookery owner bootstrap -u <username> -p <password>
./bin/rookery serve
```
</details>

<details>
<summary>Run the container instead</summary>

```bash
podman run -d --name rookery -p 8080:8080 \
  -v rookery-data:/data ghcr.io/ilijad1/rookery:latest
```

The image is slim: it ships no CLI coder binary and sets
`ROOKERY_CODER_MODE=slim`, so workspaces must use the `api` coder kind.
</details>

## What it's like

<!--
  PLACEHOLDER CONTENT, mirroring src/components/Transcript.tsx in the website
  repository. That file carries the same obligation and states the reason: a
  scripted demo alongside claims that are checkable would be a demo that lies.
  Replace this with a VERBATIM capture from a real designer build before
  launch, redaction being the only permitted edit — and replace BOTH copies
  from the SAME capture, or the two surfaces will show different "real" runs.
-->

> **You** — Every morning, tell me if any of my sites went down overnight.
>
> **Rookery** — Two quick questions. Which sites should I watch, and should I
> message you every morning or only when something is actually wrong?
>
> **You** — rookery.cloud and my blog. Only when something is wrong.
>
> **Rookery** — Here is the agent I will build:
> - Checks both sites every morning at 7am
> - Messages you only if one is unreachable or slow
> - Writes every check into your notes, under Uptime
>
> Type approve and I will build it and test it for real.
>
> **You** — approve

It then writes the agent, runs it against the real services, reports what came
back, and saves only once you have seen that.

## Workspaces

**One machine. Sealed, separate worlds.**

Every workspace is its own tenant — its own knowledge, its own credentials, its
own connected accounts, its own agents. You enter one with its password, and
nothing crosses between them.

## Knowledge base

**Everything you know, as plain markdown on your own disk.**

What you write, what your agents learn, and what your connected services bring
in, all in one vault. Open it in Rookery or in any editor you like. Agents read
the whole vault and write durable knowledge back into it across runs.

## Agents

**Describe it. Don't configure it.**

Say what you want in your own words. Rookery asks a couple of questions,
proposes a plan, builds it, tests it against the real services, and shows you
what happened before anything is saved. Then it just runs.

## Skills

**Things your agents already know how to do.**

22 built in and ready to attach to any agent — reading PDFs and spreadsheets,
web research, browser automation, git, email triage — plus any you create the
same conversational way.

Every agent gets the same tools, whatever model is behind it. A small model on
your own machine and a frontier one are given exactly the same reach. The model
decides how *well* a job is done — never whether it can be done at all.

## Connections

**91 services. No middleman holding your keys.**

Rookery talks to them directly, using credentials you own: 91 providers and 471
curated actions, over self-managed OAuth. There is no third-party integration
broker in the path.

Google, GitHub, Notion, Slack, Jira, Stripe, Shopify — and the self-hosted tier
too: Home Assistant, Immich, Paperless-ngx, Nextcloud, Jellyfin.

## Chat

**Ask what you know. Then have it act.**

Talk to your knowledge the way you'd talk to someone who has read all of it —
and have it write a note, or do something in a connected account, right there in
the conversation.

## Notifications

**You find out the moment it happens.**

An agent finished, a service returned something new, a reminder came due. It
lands in your inbox, and reaches you on Telegram, Discord or Slack when you're
away.

## Models

**Your machine. Your model.**

Use the coder tool you already have, or connect the provider you prefer —
hosted, or running entirely on your own hardware. Nothing ties you to one
vendor.

## Secrets

**Credentials that stay yours.**

Encrypted where they sit, unlocked only into the thing that needs them, scoped
to one workspace, and never handed to anyone else.

## Scheduling

**Every weekday at eight. And again at ten.**

Say when in your own words — twice a day, the first Monday of the month, every
twenty minutes during work hours, or only when you ask. Reminders work the same
way: *remind me in 10 minutes to call the doctor.*

## How it fits together

<p align="center">
  <img src="docs/assets/architecture.svg" alt="Chat platforms and a browser reach one binary on your machine, which holds isolated workspaces and reaches outward through the connector layer" width="100%">
</p>

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `ROOKERY_HOST` | `0.0.0.0` | bind address; `127.0.0.1` for loopback-only |
| `ROOKERY_PORT` | `8080` | listen port |
| `ROOKERY_DATA_DIR` | `~/.rookery` | data root; also relocates the database |
| `ROOKERY_SESSION_KEY` | generated | hex 32-byte session key |
| `ROOKERY_SYSTEM_KEY` | generated | hex key encrypting stored credentials |
| `ROOKERY_PUBLIC_URL` | — | externally reachable base URL for OAuth callbacks |
| `ROOKERY_SANDBOX` | `1` | `0`/`false`/`off` disables Landlock confinement |
| `ROOKERY_CODER_MODE` | `full` | `slim` removes the local CLI coder kind |
| `ROOKERY_CLAUDE_BIN` | — | override the path to a coder binary |

`ROOKERY_PUBLIC_URL` matters more than it looks: OAuth providers reject redirect
URIs on non-public hostnames, so a `.lan` address fails Google's validation. Use
a real hostname — `rookery.cloud` is the documented example — or
`http://localhost`.

## Platform support

| Target | Sandbox | Service |
|---|---|---|
| linux amd64/arm64 | Landlock | systemd user unit |
| container (linux) | Landlock | runtime-managed |
| darwin amd64/arm64 | none | launchd (not yet shipped) |
| windows amd64/arm64 | none | SCM (not yet shipped) |

**Off Linux there is no filesystem sandbox**: coder subprocesses run unconfined.
`/healthz` and the startup log both report this.

## Health

`GET /healthz` is unauthenticated and reports version, commit, sandbox status
including the Landlock ABI, coder mode and host-tool presence. A `python3`
warning is not cosmetic — without it the agent-tool AST guardrail self-skips, so
generated tool scripts run unchecked.

## Contributing

Branch off `main`; `main` only ever advances through merged pull requests. Use
[Conventional Commits](https://www.conventionalcommits.org/) — the PR title
becomes the squashed commit and drives release versioning. Run the full gate
locally before opening a PR:

```bash
make ci
```

## License

Apache-2.0. See [LICENSE](LICENSE).
````

- [ ] **Step 3: Check the claims the docs-sync checker will read**

The four `CLAIMS` regexes on `main` are pinned to the OLD wording and will not match this README. That is expected and is fixed in Task 8. Confirm the failure is the expected one rather than something else:

```bash
python3 scripts/check-docs-sync.py
```

Expected: four failures, each naming a claim pattern that no longer matches. Any *other* failure — an inflated `N+`, an undeclared CLI command — is a real defect in the README written above; fix it here.

- [ ] **Step 4: Confirm the asset paths resolve**

```bash
ls docs/assets/hero-banner.svg docs/assets/architecture.svg
grep -c 'docs/assets/' README.md
```

Expected: both files listed, and a count of 2.

- [ ] **Step 5: Commit**

```bash
git add README.md
git commit -m "docs: restructure the README onto the website's shape

The README predated the website's design pass and shared no structure,
ordering or voice with it. It now follows the site's section order --
workspaces through scheduling -- with the engineering tail the site does
not carry: configuration, platform support, health, contributing.

Adds ROOKERY_CLAUDE_BIN, which the old configuration table omitted
entirely rather than merely describing badly."
```

### Task 8: Re-pin the docs-sync claims

**Files:**
- Modify: `scripts/check-docs-sync.py` (the `CLAIMS` list, around lines 98–104)

**Interfaces:**
- Consumes: the exact README sentences written in Task 7.
- Produces: a green `make docs-sync-check`.

Silently deleting these entries is not acceptable — they exist because the README was wrong by half, and they are the only thing that keeps it honest.

- [ ] **Step 1: Re-pin the four product regexes**

The Task 7 README states the counts in different sentences from the old one. Replace the four `product` entries with the five below, leaving the two `web` entries untouched:

```python
CLAIMS = [
    ("product", "README.md", r"reaches\s+(\d+) external services", "providers"),
    ("product", "README.md", r"credentials you own: (\d+) providers", "providers"),
    ("product", "README.md", r"providers and (\d+)\s*\n?curated actions", "actions"),
    ("product", "README.md", r"\*\*(\d+) services\. No middleman", "providers"),
    ("product", "README.md", r"(\d+) built in and ready to attach", "skills"),
    ("web", "src/pages/index.astro", r"(\d+)\+? services", "providers"),
    ("web", "src/content/docs/docs/concepts/skills.md", r"— (\d+) built in", "skills"),
]
```

**Two of these patterns must tolerate a line break, and this is not optional.**
The README's prose wraps at roughly 80 columns, so `reaches` and its number land
on different lines, as do `471` and `curated actions`. A literal space in either
pattern matches nothing — verified during plan review, where
`r"reaches (\d+) external services"` returned `None` against the exact
paragraph Task 7 writes. Hence `\s+` and `\s*\n?`. If Task 7's prose is
re-wrapped later, re-run Step 2 rather than assuming these still hold.

- [ ] **Step 2: Verify each regex actually matches**

A regex that matches nothing fails loudly rather than silently, but confirm directly before relying on it:

```bash
python3 - <<'PY'
import re, pathlib
t = pathlib.Path("README.md").read_text()
pats = [
    r"reaches\s+(\d+) external services",
    r"credentials you own: (\d+) providers",
    r"providers and (\d+)\s*\n?curated actions",
    r"\*\*(\d+) services\. No middleman",
    r"(\d+) built in and ready to attach",
]
for p in pats:
    m = re.search(p, t)
    print(("OK  " if m else "MISS"), p, "->", m.group(1) if m else None)
PY
```

Expected: every line `OK`, with `91`, `91`, `471`, `91`, `22`. Any `MISS` means the README wording and the regex disagree — fix whichever is wrong, but do not delete the entry.

- [ ] **Step 3: Run the checker**

```bash
python3 scripts/check-docs-sync.py
```

Expected: no failures. If `check_cli` reports a hit on ordinary README prose rather than on a command inside backticks or a fence, that is the checker's known over-match — its author committed to narrowing the scan to code contexts. **Fix the checker, not the README.** Do not reword prose to satisfy it.

- [ ] **Step 4: Run the full gate**

```bash
make ci
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add scripts/check-docs-sync.py
git commit -m "chore: re-pin the docs-sync claims to the restructured README

The README's counts moved into new sentences, so all four product
regexes stopped matching. Re-pinned rather than removed: they exist
because the README was previously wrong by half."
```

### Task 9: Final verification and PR

**Files:** none modified.

**Interfaces:**
- Consumes: everything above.
- Produces: PR 2.

- [ ] **Step 1: Assert the domain bar across both repositories**

```bash
grep -rn 'rookery\.sh' /home/rookie/rookery --exclude-dir=.git --exclude-dir=node_modules --exclude-dir=bin --exclude-dir=worktrees
grep -rn 'rookery\.sh' /home/rookie/rookery-web --exclude-dir=.git --exclude-dir=node_modules --exclude-dir=dist --exclude-dir=.astro
```

Expected: no output from either. This is the owner's stated completion bar.

- [ ] **Step 2: Run the full test suite**

```bash
go test ./... -count=1 -timeout 300s
```

Expected: PASS.

- [ ] **Step 3: Re-confirm every README number against source**

```bash
ls internal/connectors/providers/*.yaml | wc -l
grep -h '^  - name:' internal/connectors/connectors/*.yaml | wc -l
ls -d internal/skilllibrary/skills/*/ | wc -l
```

Cross-check each against the README text. A number that has drifted since Task 7 means a connector or skill landed mid-flight — update the README and re-run Task 8 Step 2.

- [ ] **Step 4: Confirm the Configuration table carries every public variable**

The checker's author is adding an assertion that the README's table row set matches source-minus-allowlist, so a tenth public variable added later cannot quietly miss the table. That gate may land before or after this PR; check it directly either way:

```bash
python3 - <<'PY'
import re, pathlib, subprocess
src = set(subprocess.run(
    ["grep","-rhoE","ROOKERY_[A-Z_]+","--include=*.go","internal/","cmd/"],
    capture_output=True, text=True).stdout.split())
internal = {"ROOKERY_BUILD_PHASE","ROOKERY_CONNECTOR_URL","ROOKERY_CONNECTOR_TOKEN",
            "ROOKERY_KB_URL","ROOKERY_KB_TOKEN"}
table = set(re.findall(r"\| `(ROOKERY_[A-Z_]+)`", pathlib.Path("README.md").read_text()))
public = src - internal
print("missing from the README table:", sorted(public - table) or "none")
print("in the table but not in source:", sorted(table - public) or "none")
PY
```

Expected: both `none`. If a name is missing, add a row — this is the exact defect the restructure exists to fix, since the old table omitted `ROOKERY_CLAUDE_BIN` outright. If `internal` needs updating, read the variable's usage first: a variable assigned into `extraEnv` for a subprocess is internal; one read with `os.Getenv` in `internal/config` is public.

- [ ] **Step 5: Open PR 2**

Title: `docs: restructure the README onto the website's shape`

Body must note that it re-pins `scripts/check-docs-sync.py`'s `CLAIMS`, and that the README and the checker have to land together or `make ci` is red between them.

---

## Notes for the implementer

**The SVGs cannot be rendered on this host.** There is no rasterizer and no headless browser (`chromium`, `rsvg-convert`, `inkscape`, `magick`, `convert` are all absent). Well-formedness is machine-checkable; appearance is not. Review both images in the GitHub PR preview, in light **and** dark theme, before merging. If either reads badly, the coordinates are in the plan and are cheap to adjust.

**The transcript is placeholder content in two places now.** The website's `Transcript.tsx` says so in its own source; the README's version carries the same comment. They must be replaced from the *same* real capture, or the product will show two different "real" runs. This is out of scope here and has been raised with the owner.

**If `check_cli` fires on prose, fix the checker.** Its author agreed the bare-prose match is a defect and committed to restricting the scan to backtick spans and fenced blocks. If that has not landed by the time PR 2 is ready, narrow it as part of this PR rather than rewording documentation around a broken gate.
