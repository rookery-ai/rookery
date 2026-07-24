---
name: skill-creator
description: Use this skill when authoring a new skill from scratch — designing its SKILL.md, deciding whether it needs scripts, picking the right toolchain, and packaging it so other agents can use it. Triggers include "create a skill", "build a new skill", "write a skill that", "package this capability as a skill".
version: 1.0.0
license: MIT-0
category: Meta
---

# Skill Creator

Author new skills for the simple-agents platform. A skill is a folder
(`<name>/SKILL.md` + optional `scripts/`) that teaches an agent how to do a
recurring capability. Skills are loaded into the agent's context on demand
(progressive disclosure): only `name` + `description` are always present; the
SKILL.md body is injected when the agent decides the skill is relevant.

## The standard SKILL.md format

```yaml
---
name: my-skill          # required; lowercase, hyphens, 3-64 chars
description: ...        # required; what it does + when to use it (triggers)
version: 1.0.0
license: MIT-0
category: File Processing
metadata:
  requires:
    bins: [pandoc]       # tools that MUST all be installed
    anyBins: [pdftotext, pandoc]  # at least one
    env: [SOME_API_KEY]           # required env vars
  install:
    - kind: binary        # static binary download
      bin: pandoc
      url: https://github.com/jgm/pandoc/releases/download/3.6.4/pandoc-3.6.4-linux-amd64.tar.gz
      strip: 1
    - kind: pip           # python package
      package: pdfplumber
    - kind: node          # npm package
      package: pptxgenjs
---

# My Skill

Body: concise instructions + copy-pasteable examples. <5k words, <500 lines.
```

Only `name` and `description` are strictly required. The `description` is the
**trigger** — it must say both what the skill does and the specific phrases /
contexts that should activate it. Without a good description, the agent never
picks the skill.

## Layout: authoring vs published

A PUBLISHED skill lives at `<name>/SKILL.md`. When you are AUTHORING one you
are already inside that folder — write files at the **root of your current
working directory**. Creating another folder named after the skill nests it,
and the build cannot be saved.

```
SKILL.md               ← at the root, right here
scripts/extract.py      ← optional: deterministic code (.py or .sh)
references/             ← optional: docs loaded on demand (one level deep)
assets/                 ← optional: templates, sample files
```
Exclude README/CHANGELOG/install-guides — SKILL.md is the single source.

## Scripts

Scripts may be Python (`.py`) or shell (`.sh`). A script may drive a command
line tool — that is often the whole point of a skill.

- Call CLI tools with `subprocess` and an ARGUMENT LIST, e.g.
  `subprocess.run(["pdftotext", path, "-"], capture_output=True, text=True)`.
- NEVER use `shell=True` — including `shell=1`, or any `**kwargs` spread into
  a `subprocess.*` call (the checker cannot prove `shell` is absent from a
  spread and rejects it outright) — nor `os.system` nor `os.popen`. They
  evaluate a shell string, which is an injection risk and is rejected by the
  safety check.
- A tool installed by the cli-tool-installer skill lives at
  `$HOME/.local/bin/<tool>`, which is NOT on the sandboxed agent's `PATH`.
  Resolve it there first, then fall back to `shutil.which` (Python) or
  `command -v` (shell).
- Refer to tools by their BARE name in the SKILL.md body; the runtime
  environment block supplies the real path. Inside a script, resolve and use
  the absolute path yourself (see above) — never hardcode `/usr/bin/...`.
- Fail with a clear message naming the missing tool and how to install it,
  rather than crashing with a traceback.
- Only USER skills — the kind this process authors — actually ship scripts:
  they land in the user's vault on disk and run for real. A CORE skill
  reaches an agent as embedded SKILL.md text only; anything you put in its
  scripts folder is never written to disk at run time and never executes.
  Don't write instructions that assume a script runs unless you are
  authoring a user skill.

## Testing

Run every script you write before you finish — with `--help`, or against a
small fixture you create in the working directory. A script you have never
seen run must not ship. Never print, log, or return a secret value.

## Degrees of freedom (match specificity to task fragility)

- **High freedom** — plain text instructions for flexible tasks (analyze,
  summarize, advise). Most skills are this.
- **Medium freedom** — pseudocode / parameterized snippets when a preferred
  pattern exists (parse CSV, transform a file).
- **Low freedom** — exact scripts when the operation is fragile/error-prone
  (PDF merge, binary extraction).

Pick the simplest tier that solves the task. A reasoning-only skill (no scripts)
is ideal when the LLM + a few inline snippets suffice.

## Platform conventions (non-negotiable)

- Agents run **sandboxed** (Linux Landlock, filesystem-only). Network is
  available during runs.
- Per-user persistent HOME = `$HOME/.local/bin/` holds installed CLI tools.
  **Invoke every installed tool by absolute path** (`$HOME/.local/bin/pandoc`),
  never bare `pandoc` — the inherited PATH points at the operator's dirs.
- **No `/tmp`** — use `$TMPDIR` (= `$HOME/tmp`).
- Can't install system-wide (`/usr/bin` is RO). Static-binary download to
  `$HOME/.local/bin/` is the mechanism for pandoc/ffmpeg/poppler/jq.
- The runtime **environment block** tells the agent the resolved absolute path
  of each `requires.bins` tool (or "missing — install via the cli-tool-installer
  skill"). So write tool names **bare** in the body (`pandoc ...`); the env
  block supplies the real path. Never hardcode `/usr/bin/...`.
- **Secrets are env vars** — read from `os.environ`, never hardcode keys.
- **Connected services** (Gmail, GitHub, Notion, …) are reached through the platform's
  native connector tools — never hand-roll OAuth or hit a third-party aggregator.
- Write outputs into the user's **vault** or `$TMPDIR`, never `/tmp`.

## The 6-step authoring process

1. **Understand** — gather concrete usage examples via focused questions. Ask
   one thing at a time; don't dump a long form. What does the user want the
   skill to do? What inputs/outputs? What tools/services are involved?
2. **Plan reusable contents** — from the examples, decide: is this
   reasoning-only (just SKILL.md), one snippet, or multi-script? Which tools
   does it `requires`? Which secrets/env? Draft the frontmatter.
3. **Write SKILL.md + scripts** — frontmatter first (a strong `description`!),
   then the body (concise, imperative voice, copy-pasteable examples). If
   scripts/, implement them and keep them minimal (e.g. scripts/extract.py).
4. **Test** — run every script you authored (see "## Testing" above — a
   script never seen to run must not ship) and confirm it doesn't crash.
   Validate the frontmatter parses and the `description` reads as a clear
   trigger. Emit results in a `[TEST_OUTPUT]...[/TEST_OUTPUT]` block.
5. **Package** — ensure the folder is self-contained: SKILL.md present, frontmatter
   valid, no stray files. The skill is saved to the user's vault
   `skills/<name>/` and registered in their skills list.
6. **Iterate** — note any friction for the next revision.

## Safety (the skill-vetter will audit your output)

Do not produce a skill that: reads/writes outside the user's vault; exfiltrates
vault notes / `memory/USER.md` / `SOUL.md` / secrets; makes raw-IP network
calls; uses obfuscated/base64/encoded payloads; runs `sudo`; installs unlisted
packages; harvests credentials; or contains deceptive instructions that trick
the agent. If the capability genuinely needs sensitive access, declare it
explicitly in `requires.env` and document why.

## Best practices

- The `description` is the most important field — invest in it. Name concrete
  triggers ("read this pdf", "send an email via gmail").
- Prefer the LLM + inline snippets over a script whenever possible.
- Keep SKILL.md under ~500 lines; move deep reference into `references/`.
- Use the platform's existing core skills as templates (see `pdf`, `markdown`,
  the platform's native connectors).