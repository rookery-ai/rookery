# Core skills: judgment, not capability

**Date:** 2026-09-04
**Status:** design, approved for implementation

## The question that reframed this

> Do you think core skills are unnecessary as we have everything as tool?

No — but the honest answer kills more of the current set than a "refresh the
skills" framing would, and it invalidates the obvious fix.

The project already wrote the rule down, in `internal/skilllibrary/removed_test.go`:

> A skill and a tool competing for the same job is worse than either alone: the
> weak models this platform runs pick badly between them, which was the whole
> reason the tool was built.

That is why `playwright-browser` was deleted when the native `browser_*` tools
landed. It taught a model to hand-write Playwright in Python against a tool that
did the same job with the sandbox, the address guard and the secret redaction a
hand-rolled script has none of.

**The same defect exists today and was never cleaned up.** `csv` teaches pandas
against `kb_table_query`, which does group-and-aggregate host-side with no code
at all. `docx`/`pptx`/`xlsx`/`pdf` teach python-docx, markitdown, openpyxl and
pdfplumber against `rookery kb convert`, which runs `internal/convert` — with
`pdftotext`, the OCR fallback, table recovery, embedded-image extraction and
lossy-conversion warnings behind it. One instance of this defect was fixed; five
were missed.

## The distinction that survives

**A tool gives capability. A skill should give judgment.**

Any skill whose content is *how to do X with library Y* is dead weight, because
the how is now a tool — and a worse how, since the tool carries confinement and
error handling the snippet does not. What no tool description can carry is
*when*, *why*, and *what good looks like*.

This matters more here than it would elsewhere for two reasons already recorded
in this repo. The platform deliberately runs small models, which pick badly
between a skill and a tool offering the same job. And a core skill ships
SKILL.md only (`TestCoreSkillsShipNoScripts`), so every snippet is copied by the
model into `run_script` or `bash`, where the AST guardrail — which only scans
authored `tools/*.py` — never sees it.

## The measured state

Zero of 21 core skills mention anything shipped since they were written:
`kb_table_query`, `kb_file_map`, `search_files`, `glob`, `get_state`/`set_state`,
`browser_click` and the acting tools, `save_to_kb`, `connector exec`, or MCP.
Exactly one mentions `web_fetch`.

Six carry pip dependencies: `csv` (pandas), `xlsx` (openpyxl, pandas), `docx`
(python-docx), `pdf` (pdfplumber, pypdf), `pptx` (markitdown), `skill-creator`
(pdfplumber).

Against `markdown` and `image-ocr`, which are already CLI-first with zero Python
blocks and work. The house style exists; the document skills drifted from it.

## Design

### A. Sort every skill by the rule, and act on the answer

**Redundant with a tool — replaced by a pointer, not a rewrite.** `csv`, `xlsx`,
`docx`, `pptx`, `pdf`. Each becomes a short skill that says: convert it with
`rookery kb convert`, then read the markdown; query a table with
`kb_table_query`; map a big file with `kb_file_map` first. The pip specs go.

The alternative considered and rejected was swapping pandas for Miller or
csvkit. That trades one library the model has to drive correctly for another,
when a native tool already does the job — it would have reproduced the
playwright-browser defect with a different dependency.

**Genuine judgment — kept, and sharpened.** `notification-writing`,
`time-and-timezones`, `resilient-runs`, `change-detection`, `kb-curation`,
`email-triage`, `calendar-scheduling`, `agent-collaboration`. None of this is
expressible as a tool description: what makes a 03:00 message worth reading, how
to decide something genuinely changed, what to write down and what to discard.
These are the reason the skill system exists at all.

**Structural — kept.** `skill-creator`, `skill-vetter`, `cli-tool-installer`,
`git-and-github`, `api-integration`, `web-research`, `markdown`, `image-ocr`.

### B. State the doctrine once, where it is enforced

A ranked rule in `skill-creator`, so every generated skill follows it, and in the
authoring guidance:

1. **A native tool**, if one exists.
2. **A CLI invocation**, if a tool can do it.
3. **Python**, only when neither will.

Plus the negative rule that `removed_test.go` already implies: do not write a
skill that competes with a tool for the same job.

### C. Teach the capabilities that exist

Folded into existing skills rather than added as new ones, because the enabled
pool is a shared budget and every extra skill costs selection quality:

- `kb_file_map` before reading a big file, and `kb_table_query` for arithmetic —
  into the document skills and `kb-curation`.
- The browser acting tools and the irreversible-action permission — into
  `web-research`.
- `get_state`/`set_state` — into `resilient-runs` and `change-detection`, the two
  that most need durable memory.
- Connections and MCP — into `api-integration`.

### D. New skill: `ssh`

An SSH key held as a secret, used to reach a host. It earns its place under the
rule above because there is no SSH tool: this is judgment plus a CLI, which is
exactly what the second tier is for.

The care it needs is why it is a skill and not a snippet: the key arrives as an
env var and must reach disk at `0600` under `$TMPDIR` (**not** `/tmp`, which the
sandbox does not grant), host-key policy has to be stated rather than blanket
-disabled, and `ProfileSkillScript` permits list-form `subprocess` so a user
skill may drive the binary.

### E. Deletions are swept, not just removed

Any skill removed outright needs an `agent_skills` sweep: that table is keyed by
NAME with no foreign key, so a dangling row does not error — it silently costs an
agent a capability it believes it has. Migration 019 did this for
`playwright-browser` and is the precedent.

**No skill is deleted outright in this change.** The five document skills are
rewritten rather than removed: the trigger descriptions still need to match "read
this excel", and a skill that says *use `kb convert`* is more useful than an
absent one. So no migration is required — recorded here because the obvious
reading of "redundant" is "delete", and deleting would break those triggers.

### F. Website

`SkillsBento.tsx` and the capability section, which currently describe the skills
by their old shape.

## Testing

- `catalog_test.go` continues to assert frontmatter parses, name matches
  directory, description carries triggers, and referenced `scripts/` exist.
- A new test asserting **no core skill instructs the model to install or import a
  library for a job a native tool does** — the mechanised form of the rule, so
  the next drift fails rather than ships.
- `TestCoreSkillsShipNoScripts` unchanged.
- `docs-sync-check` for the skill count on the website.

## Out of scope

Per-workspace skills are untouched: they are the user's own and may do whatever
they like, including drive a library. The doctrine here governs what the platform
ships.
