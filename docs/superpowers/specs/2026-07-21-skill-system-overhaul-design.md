# Skill System Overhaul — Design

**Date:** 2026-07-21
**Status:** Approved (section-by-section, 2026-07-21)
**Scope:** Sub-plan 10 of the post-redesign track. SP9 (telegram-parity, power-and-creation) shipped 2026-07-21.

---

## 1. Problem

The skill system has three independent defects. Together they mean a user cannot create a skill,
cannot rely on an agent picking up the skills it needs, and cannot write a skill that invokes a
CLI tool even though the platform ships a skill dedicated to installing CLI tools.

### 1.1 Skill creation fails on the API coder

Live evidence, workspace *Ilija Personal* (`coder_kind=api`, `mistral/mistral-medium-3-5`), draft
`pretty printer`, stuck in `designing`:

> I attempted to build the skill but it did not succeed. Reason: the coder didn't create SKILL.md.

The cause is in the API engine, not the skill designer. `skilldesigner.runGeneration`
(`internal/skilldesigner/flow.go:497`) sets `SA_BUILD_PHASE=generation` so the connector build-guard
applies. `api_engine.go:408` derives `tools.verifyBuild` from that same marker. `verifyBuild` then
activates `hostToolSet.verifyFinishNudge` (`internal/coder/hosttools.go:416`), whose **first gate is
hard-coded to `AGENT.md`**:

```go
if md, err := os.Stat(filepath.Join(h.workDir, "AGENT.md")); err != nil || md.Size() == 0 {
    // "you have NOT written AGENT.md yet — the agent's full instructions,
    //  which are the actual deliverable"
}
```

A skill build must never write `AGENT.md`. So on every finish attempt the engine refuses the finish
and instructs the model to write a file that is not part of a skill, burning the whole
`maxVerifyNudges` budget steering it away from `SKILL.md`. The designer then reads the staging dir,
finds no `SKILL.md`, and soft-fails.

The same agent-shaped assumption appears in gate 2: `isAgentScriptPath` (`hosttools.go:362`) only
recognises `tools/*.py`, so a skill's `scripts/` are never script-verified. The progress string at
`api_engine.go:116` is likewise agent-worded ("verifying the agent's script actually works…"). The
other three `verifyBuild` sites were checked and are deliverable-agnostic: the raised token cap
(`:63`), the larger turn budget (`:88`), and the `[BLOCKED]` grace turn (`:153`) are all correct for
skill builds as-is.

### 1.1a A second, independent cause: SKILL.md written one level deep

The recovered staging dir for that draft shows what the model actually produced:

```
.staging-pretty printer/
├── scripts/                  ← empty, pre-created by Go
└── pretty-printer/           ← the model created its own subdirectory
    ├── SKILL.md              ← valid: correct frontmatter, name, description, triggers
    └── AGENT.md              ← written in response to the misfiring gate-1 nudge
```

Two things are confirmed here. The model **did** comply with the AGENT.md demand, which is direct
evidence for §1.1. And it wrote a **valid SKILL.md** — which was still discarded, because
`runGeneration` reads `filepath.Join(stagingDir, "SKILL.md")` as an exact path. The model nested it
under `<name>/` because that is the layout `skill-creator/SKILL.md` documents (`<name>/SKILL.md`)
and nothing in the implementation prompt pins the output to the working directory root.

So "the coder didn't create SKILL.md" was wrong twice over: the coder created it, and the file was
good.

**Timeline reconciliation.** Gate 1 was introduced in `ad2c3d8` (2026-07-09). The one skill that
has ever saved on this install, `pdf-to-email-summarizer`, was created 2026-07-02 — a week earlier.
Skill creation worked before that commit and has been broken since.

### 1.2 Skill scripts cannot invoke CLI tools

`RunToolGuardrails` runs `checkAST` on every generated skill script. The AST checker
(`internal/agentdesigner/guardrails.go:137`) bans `subprocess.*` outright. A skill whose purpose is
to call `pdftotext`, `pandoc`, `jq` or `tesseract` therefore cannot pass — while the `cli-tool-installer`
core skill teaches the user how to install exactly those binaries. The platform installs tools no
generated skill is permitted to invoke.

### 1.3 An agent's skills are never auto-detected

`parseSkillsLine` reads a `# Skills: a, b` header from AGENT.md and is the sole source of an agent's
initial skill attachments. But the instruction to emit that header exists **only** in
`BuildDesignSystemPrompt` (`internal/prompts/prompts.go:716`) — the text-only design-conversation
prompt, which writes nothing to disk. `BuildImplementationPrompt` (line 1125), the prompt that
actually writes AGENT.md, has no `Skills` field and never mentions skills at all.

The parser looks for something no prompt asked the file's author to produce. The header is usually
absent, `parseSkillsLine` returns `nil`, and per the current contract **no skills are attached** —
forcing the user to assign them by hand on the agent page.

Measured on the live install: the `agent_skills` table is **empty**. Not one agent, across either
workspace, has a single skill attached.

### 1.4 Catalog drift

- `pdf/SKILL.md` and `docx/SKILL.md` document a `scripts/` directory; both dirs are empty and
  `go:embed` skips empty dirs, so the documented helpers do not exist.
- `readScriptsFromDisk` (`flow.go:1029`) reads only `*.py`, only at the top level of `scripts/`.
  A `.sh` helper, a nested module, or a `references/` doc is silently dropped between the staging
  dir and the saved skill.
- `web-search` and `web-scraper` duplicate the API engine's native `web_search` / `web_fetch`;
  `github-integration` duplicates the GitHub connector.
- `validateSkillName` (`flow.go:875`) accepts `"pretty printer"` — a space — which becomes a
  directory name and a frontmatter `name:` violating the skill-name convention.

## 2. Goals

- Skill creation succeeds end to end on the API coder engine.
- A skill script may install and invoke a CLI tool.
- Every newly created agent has skills attached without the user touching the agent page; the user
  may still add or remove them afterwards.
- The core catalog matches the current architecture: no duplication of native tools or connectors,
  no documented-but-missing files, and coverage of the behaviours a personal-assistant agent needs.

## 3. Non-goals

- Skill **editing** over chat or web (`/skill edit`) — the skill designer still has no edit mode.
  Called out because Phase 1's `buildSpec` is the seam it would extend, but it is not built here.
- Skill import (ZIP / pasted SKILL.md) over chat — needs per-adapter file-upload handling.
- Any change to the runtime skill-injection path. `<skill_instructions>` and `<skill_environment>`
  already work once names are in `agent_skills`; selection was the only broken link.
- MCP-based native connector tools for CLI coders.

---

## 4. Phase 1 — Skill pipeline correctness

### 4.1 Parameterize the build-finish guard

`hostToolSet` gains a build specification describing what the current build must produce:

```go
type buildSpec struct {
    deliverable string                  // "AGENT.md" | "SKILL.md"
    isScript    func(path string) bool  // isAgentScriptPath | isSkillScriptPath
    nudgeNoun   string                  // "the agent's instructions" | "the skill's instructions"
}
```

`coder.WithBuildSpec(spec)` sets it. `verifyBuild` stays keyed off `SA_BUILD_PHASE` as today; only
*what* the gate checks becomes caller-supplied. `agentdesigner.runGeneration` passes the agent spec,
`skilldesigner.runGeneration` the skill spec. With no spec set, the agent spec is the default, so
existing behaviour is unchanged for every current caller.

The blast radius is small and bounded: the three other `verifyBuild` sites (`api_engine.go:63`,
`:88`, `:153`) are deliverable-agnostic and need no change (§1.1). Only `verifyFinishNudge`,
`isAgentScriptPath`, and the progress string at `api_engine.go:116` read the spec.

**Gate 2 for skills — kept, with the nudge text from the spec.** A skill script is a reusable tool
invoked later with real inputs, not the work itself, so an argument-less build-time run may
legitimately produce nothing — which argues for disabling gate 2. But `skill-creator` already
mandates a smoke run (`python3 scripts/x.py --help`, or against a fixture), and such a run does
produce stdout and does satisfy the gate. Gate 2 therefore *enforces the smoke test the skill
contract already requires*, and disabling it would drop that for no gain. What must change is the
wording: today's nudge is agent-shaped ("at build time it cannot reach the live service", "fetch AND
write each destination file"), which on a skill build would burn the nudge budget chasing live data
the script was never meant to fetch. The nudge text moves into `buildSpec`, so the skill variant
says "run it with `--help` or a small fixture and show the output" instead.

Rejected alternatives:

- **Disable the guard for skill builds** (`WithVerifyBuild(false)`). Two lines, but skills lose
  script verification entirely and the defect returns for the next build type.
- **Infer the deliverable from `workDir`.** No new API, but the engine would guess at the caller's
  intent — the same implicit coupling that caused the bug.

Skill builds keep the script-verification safety net: a skill whose script never once returned real
output is exactly as suspect as an agent's.

### 4.2 Guardrail profiles

`checkAST(code)` becomes `checkAST(code, profile)`:

| | `ProfileAgentTool` | `ProfileSkillScript` |
|---|---|---|
| `eval`, `exec`, `compile`, `__import__` | banned | banned |
| `os.system`, `os.popen`, `os.exec*`, `os.spawn*` | banned | banned |
| `socket.socket` | banned | banned |
| `subprocess.*` with an argument list | banned | **allowed** |
| `subprocess.*(shell=True)` | banned | **banned (new violation)** |

`shell=True` is a new, explicit violation rather than an omission: it reintroduces precisely the
shell-string injection surface `os.system` is banned for.

`RunToolGuardrails(filename, code, profile)` and `RunFullGuardrails(code, profile)` take the profile.
`agentdesigner` passes `ProfileAgentTool` (unchanged behaviour); `skilldesigner.runGeneration` and
`SkillSaver.SaveSkill` pass `ProfileSkillScript`.

Skills carry two defences agent tools do not — the `skill-vetter` LLM audit and the Landlock
sandbox — which is what makes the widened profile acceptable at this boundary and not at the other.

### 4.3 Script file handling

`readScriptsFromDisk` is replaced by a recursive read of the entire staging dir minus `SKILL.md`,
reusing `agentdesigner.isTestArtifact` to skip build junk. That classifier is currently unexported
(`toolstree.go:63`) and its `toolsDir` parameter is agent-shaped, so it is exported as
`IsTestArtifact` with the tools-dir argument generalised to the build's script root — one change,
both callers. Files keep their path relative to the
skill root, so `scripts/lib/parse.py` and `references/api.md` round-trip intact.
`SkillSaver.SaveSkill` already rejects `..` and absolute paths per file; that check is the safety
boundary and is retained unchanged.

`runTests` gains `bash -n` for `.sh` alongside `py_compile` for `.py`, and **skips** files it cannot
statically check rather than reporting them as failures — a `references/*.md` must not appear as a
failed test.

### 4.3a Locating SKILL.md

`runGeneration` reads `filepath.Join(stagingDir, "SKILL.md")` as an exact path, so the one-level
nesting in §1.1a is fatal even though the file is valid. Fixed on both sides:

- **Prompt.** `BuildSkillImplementationPrompt` states explicitly that `SKILL.md` and `scripts/` go
  at the root of the working directory — do not create a `<name>/` folder, the folder already
  exists. `skill-creator/SKILL.md` documents the *published* layout (`<name>/SKILL.md`), which is
  what misled the model; §6.2 aligns its wording.
- **Read.** If the root `SKILL.md` is absent, search one level down for exactly one `SKILL.md` and,
  on a unique hit, treat that directory as the skill root — the whole subtree is hoisted, so
  `pretty-printer/scripts/x.py` becomes `scripts/x.py`. Zero or multiple hits keep today's
  soft-fail. A build must not be thrown away over a directory level.

The pre-created empty `scripts/` dir at the staging root is dropped: it does not steer the model
(it wrote its own tree anyway) and an empty dir is indistinguishable from one the model chose to
leave empty.

### 4.4 Skill naming

`validateSkillName` slugifies before use: lowercase, spaces and underscores to hyphens, strip
anything outside `[a-z0-9-]`, collapse repeats. `"pretty printer"` becomes `pretty-printer`. The
slug is applied before the staging dir is built, so no path ever contains a space.

`finalizeSkill` currently trusts `meta.Name` from the generated frontmatter, which may differ from
the name the user typed and was validated. It re-validates and re-slugifies the generated name; the
core-skill collision check in `SaveSkill` remains as the backstop.

### 4.5 Testing

- `verifyFinishNudge`: table test over both build specs × (deliverable present / absent) ×
  (script verified / not), asserting the nudge names the right file and allows the finish when
  satisfied.
- Guardrail corpus: `subprocess.run([...])` passes under `ProfileSkillScript` and fails under
  `ProfileAgentTool`; `subprocess.run(..., shell=True)` and `os.system` fail under both;
  `eval`/`exec`/`__import__` fail under both.
- Script round-trip: a staging dir containing `.py`, `.sh`, a nested module and a `references/` file
  survives read → save with its tree intact; a test artifact is dropped.
- SKILL.md location: root hit; single one-level-down hit (subtree hoisted, `scripts/` paths
  rewritten); no hit and two ambiguous hits both soft-fail. The recovered `pretty printer` staging
  tree from §1.1a is the fixture for the hoist case.
- Slugification: names with spaces, mixed case, and punctuation.

---

## 5. Phase 2 — Skill autodetection

### 5.1 Tier 1: make the header likely

`ImplementationParams` gains `Skills []SkillRef`. `BuildImplementationPrompt` and
`BuildEditImplementationPrompt` gain an `<available_skills>` block plus the emit-the-header
requirement, single-sourced as a shared block with `BuildDesignSystemPrompt` so the three cannot
drift.

### 5.2 Tier 2: a selector call independent of the build model

`agentdesigner.SelectSkills(ctx, coderSvc, agentMD, pool) []string` — one text-only call
(`WithNoTools`) using `prompts.BuildSkillSelectionPrompt`: the agent's AGENT.md plus the catalog's
name+description lines, asked to return only the names the agent needs. The response is parsed with
the same tolerant matcher `parseSkillsLine` already uses, so formatting drift is handled and
unknown names are dropped with a warning.

Wired into `saveAndFinish` and `updateAndFinish`:

```go
names := parseSkillsLine(agentMD, pool)
if names == nil {                                   // header absent entirely
    names = SelectSkills(ctx, coderSvc, agentMD, pool)
}
db.SetAgentSkills(agentID, names)
```

Three contracts, each a place this could go subtly wrong:

- **`nil` vs empty is load-bearing.** `parseSkillsLine` returns `nil` only when no header exists at
  all; a present `# Skills: none` returns a non-nil empty slice. The selector fires on `nil` only.
  An explicit "none" is a decision, and silently overriding it would make attachment unpredictable.
- **Edits never clobber.** On the edit path the selector runs only when the agent has no
  `agent_skills` rows, mirroring the rule `AutoBindTargets` uses for connections. A hand-curated
  skill set survives a re-edit.
- **Fails closed, loudly.** One retry; on persistent error or an unparseable response, attach
  nothing and `slog.Warn`. This is today's behaviour, not a silent guess.

The agent page's Skills card remains the manual override for both add and remove.

### 5.3 Testing

A fake coder drives the selector: well-formed response, prose-wrapped response, hallucinated names
(dropped), empty response (attach nothing). Plus the two clobber rules — an edit with existing rows
is a no-op, and `# Skills: none` is respected without a selector call.

---

## 6. Phase 3 — Core skill catalog

### 6.1 Fix the drift

`pdf` and `docx` get the `scripts/` helpers their bodies document. Phase 1 makes these possible for
the first time — they invoke `pdftotext` / `pandoc` via `subprocess` with an argument list — so this
doubles as the first dogfood of `ProfileSkillScript`.

### 6.2 Update the two meta skills

These drive the designer itself; if they lag the code, every generated skill inherits the lag.

- **`skill-creator`** — teach the widened contract: scripts may be `.py` or `.sh`; CLI tools are
  invoked via `subprocess` with an argument list, never `shell=True`; tools are named bare in the
  body and called by absolute path in scripts; `references/` holds on-demand docs. Also separate the
  *published* layout (`<name>/SKILL.md`) from the *authoring* layout — files go at the working
  directory root, no `<name>/` folder — since conflating the two is what produced §1.1a.
- **`skill-vetter`** — matching audit criteria: `shell=True`, raw-IP network calls, obfuscated
  payloads, and any read of `USER.md` / `SOUL.md` / secrets.

### 6.3 Refocus the three redundant skills (3 → 2)

- **`web-research`** (merges `web-search` + `web-scraper`) — multi-source research strategy, judging
  source quality, and structured extraction from HTML the native `web_fetch` already returned: the
  part the built-in tools do not cover.
- **`git-and-github`** (refocuses `github-integration`) — local repo work (clone, diff, branch,
  commit), with an explicit pointer to the GitHub connector for API calls, so skill and connector
  stop competing for the same triggers.

`agent_skills` is keyed by skill **name**, so renaming a core skill orphans any existing attachment.
On this install the table is empty (§1.3), so no migration is required — but the rename is recorded
here as the reason a future core-skill rename would need one.

### 6.4 Ten new skills

**Agent behaviour** — how an agent should act on this platform:

| Skill | Covers |
|---|---|
| `kb-curation` | Writing well-formed markdown into the vault: heading structure, `[[wikilinks]]`, `notes/` vs `memory/`, append vs rewrite, keeping memory files clean |
| `change-detection` | Compare against `state.md` between runs, report only what is new, `[SILENT]` when nothing changed; seen-ID sets, cursors, last-value tracking |
| `notification-writing` | What belongs in a `[CHAT]` message, how short, when to stay silent, formatting for a small screen |
| `api-integration` | Calling a REST API outside the 28 connectors with a stored secret: auth headers, pagination, rate limits, never printing the secret |
| `agent-collaboration` | The `[CALL: <agent-name>]` protocol, max depth 3, when to split work across agents |
| `resilient-runs` | Retry vs give up, reporting partial results honestly, degrading when a service is down, never claiming success it did not have |
| `time-and-timezones` | Timezone-correct scheduling and date math against the workspace profile timezone; DST; catch-up after downtime |

**Domain**:

| Skill | Covers |
|---|---|
| `email-triage` | Gmail/Outlook connector workflows: inbox summary, importance filtering, action-item extraction, drafting |
| `calendar-scheduling` | Calendar connector workflows: day/week ahead, free slots, event creation, conflict detection |
| `image-ocr` | Screenshots, scans and photos → text via `tesseract` |

`image-ocr` is deliberately the acceptance test for the install-and-use requirement: it declares
`requires.bins: [tesseract]`, installs through `cli-tool-installer`, and its script invokes the
installed binary by absolute path. If that works end to end, install-and-use works.

### 6.5 Catalog size

The result is 22 core skills (10 kept, 2 refocused from 3, 10 new). At roughly 40 words of
trigger-phrase description each, `<available_skills>` is ~900 words injected into every design turn,
every implementation prompt, and the selector call.

Descriptions stay at full length — they *are* the matching signal, and truncating them would
undercut Phase 2. Instead the block is **grouped by the `category:` field the frontmatter already
carries**, so the model scans a structured list rather than a flat wall. If autodetection still
degrades on a weak model, the fallback is to send the selector a category-filtered subset; that is
not built until measured.

### 6.6 Catalog invariants as a test

One table test over every embedded core skill, so this drift cannot recur:

- `ParseMeta` succeeds.
- `name` is non-empty and equals the directory name.
- `description` is non-empty.
- Every `scripts/` path referenced in the body exists on disk.
- Every shipped script passes `ProfileSkillScript` guardrails.

The last item holds the core catalog to the same bar as user-generated skills.

---

## 7. Risks

- **The selector call adds a turn to every agent save** where the header is absent. Text-only and
  bounded by the catalog size, but on a rate-limited provider it is one more call that can fail.
  Mitigated by failing closed with a warning rather than blocking the save.
- **`ProfileSkillScript` widens what a generated skill may do.** The mitigation is layered rather
  than singular: `shell=True` is a hard violation, the `skill-vetter` audit runs on the real
  generated content, and Landlock confines the process to the workspace's own vault and HOME.
- **22 skills may degrade autodetection on a weak model** before the selector even runs. Category
  grouping is the first mitigation; a filtered selector subset is the measured fallback.
- **Live verification is manual.** There is no e2e harness. Phase 1 is verified by rebuilding the
  stuck `pretty printer` draft on the Mistral workspace; Phase 3 by an `image-ocr` agent that
  installs `tesseract` and reads a real image.

## 8. Acceptance

1. The `pretty printer` draft (or an equivalent) builds to `StateVerifying` and saves on the
   `api`/Mistral workspace, with no `AGENT.md` in the staging dir.
2. A generated skill containing `subprocess.run([...])` passes guardrails and saves; the same script
   with `shell=True` is rejected.
3. A skill with a `.sh` helper and a `references/` file saves with its tree intact.
4. A newly created agent that processes PDFs has `pdf` attached **without the user opening the agent
   page**, in the case where AGENT.md carries no `# Skills:` header — i.e. the selector fired and
   wrote the row. Asserting the row exists is the test; asserting the code path exists is not.
   `agent_skills` being empty across the install (§1.3) is the baseline this must move.
5. An `image-ocr` agent installs `tesseract` via `cli-tool-installer` and reads text from an image.
