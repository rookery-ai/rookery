# Skill dependencies: build without installing, install safely at run time

Date: 2026-10-26
Status: draft for review

## Why

A user asked the skill designer for a `trading-agents` skill that wraps
TauricResearch/TradingAgents, a Python package with a large dependency tree.
The build failed with *"I attempted to build the skill but it did not succeed.
Reason: `` ` block is warranted: the script is not broken``…"*. Investigation of
the stored draft (`skill_drafts`, workspace `Personal`, API coder on OpenRouter)
found two separate problems:

1. **The build very likely succeeded and was thrown away.** The model wrote a
   sentence *about* the `[BLOCKED]` marker ("whether a `[BLOCKED]` block is
   warranted…"). `skilldesigner.parseBlockedOutput` matches the token anywhere
   with `strings.Index`, takes everything after it as the reason when there is no
   `[/BLOCKED]` closer (there was none — the "reason" swallowed `[TEST_OUTPUT]`
   and `[SILENT]`), and the caller calls `cleanupStaging()` before reading what
   is on disk. The draft row afterwards holds an empty `pending_skill_md`. The
   most likely trigger is `graceTurnBudgetNudge` ("Immediately emit [BLOCKED]
   now") after the build spent its turns; this cannot be confirmed because the
   skill designer writes **no** log lines per build.
2. **There is no defined story for skill dependencies.** The skill build
   inherits the tool-calling capabilities block, which says *"Do not install
   packages."* — the same line agent runs see, so an agent using a skill is
   told not to run its install step either. `metadata.install` (including
   `kind: pip`) is documented by `skill-creator` but only ever *displayed*;
   nothing performs it. `<skill_environment>` reports missing CLI binaries and
   says nothing about Python packages.

## What the owner decided

- **The skill builder never installs.** It writes a skill (SKILL.md, scripts,
  an install step) that an agent can use; the agent installs on first use.
- **A missing live run at build time is expected, never a blocker.** The build
  tests what it can without the dependency (`--help`, error paths, logic against
  a stand-in) and saves the skill, saying what was not exercised.
- **Agents on both coder kinds may install** a skill's declared dependencies
  and anything else they need.
- **Installs persist in the workspace home** (`claude-homes/<workspace>/`), as
  they already do for CLI tools in `$HOME/.local/bin`: the first run pays, later
  runs reuse. One Python environment per skill.
- **Vetting is the safety gate for the skill**: it checks the skill and its
  install step for harm (exfiltrating secrets, hidden download sources).
- **A skill that writes a secret's value to disk is blocked** by vetting, with a
  message telling the user to keep it in Settings → Secrets and read it from the
  environment variable.

## Goals

- The `trading-agents` build (and builds like it) completes and can be saved.
- An agent that uses a skill with Python dependencies installs them on its
  first run, without being told not to, on both the API and the CLI coder.
- A malicious or compromised dependency is contained as far as the platform can
  enforce, and what it can still reach is stated rather than implied.
- Every install is visible in the run's transcript and recorded on disk.

## Non-goals

- Installing anything during a skill build.
- Ephemeral per-run environments (decided against: persistence is the cache).
- Auditing the *contents* of a downloaded package. The vetter reads text and
  has no tools; it judges the source and the use, not the code it will fetch.
- Package managers other than pip in this change (npm, cargo, …). Static CLI
  binaries keep using the existing `cli-tool-installer` path.
- A UI listing installed environments (recorded as follow-up).
- Choosing a different Python interpreter (host `python3` is 3.14; `uv`-managed
  interpreters are a follow-up if a real package proves incompatible).

## Anti-goals

- Relaxing a prompt line and calling it safety. Every safety claim below is
  either enforced in code or explicitly listed as residual.
- A safety mechanism so cumbersome that agents route around it with raw
  `pip install` in `bash` every time.
- Breaking existing agents: secret injection for an ordinary agent run is
  unchanged by this design.

## Threat model for "a harmful dependency exposes the workspace"

What a process inside an agent run can reach today (Linux, Landlock on):

| Asset | Reachable today | Why |
|---|---|---|
| Every workspace secret | yes | `runner.go` injects `svc.GetAll` into the run env |
| Coder login (`.claude/.credentials.json`) | yes | lives in the RW workspace home |
| Whole knowledge base | read+write | vault is RW for runs |
| Other skills' installed envs | read+write | same home |
| Network | unrestricted | Landlock here is filesystem-only |
| DB, `system.key`, other workspaces | no | outside the Landlock grant |

A malicious Python package gets code execution at two moments: **install
time** (setup.py / build backend / a git dependency's build) and **import
time** (when the skill's script runs). The design contains each separately.

## Design

### Part A — the false block (bug fix)

1. **Line-anchor `[BLOCKED]`** in `skilldesigner.parseBlockedOutput`: only a
   line whose trimmed content *starts with* `[BLOCKED]` opens a block (the
   closer still optional). A token inside a sentence or backticks is ignored.
   Same rule as `chat.CleanReply` records. Apply the identical fix to
   `agentdesigner.parseBlockedOutput`, which has the same substring shape.
2. **Reconcile against disk before discarding.** When a block is parsed, the
   skill designer first runs `LocateSkillRoot(stagingDir)`. If a SKILL.md with
   parseable frontmatter exists, the build proceeds to guardrails/vetting as a
   normal build, with the block text prepended to the review message as a
   caveat (mirrors `agentdesigner.reconcileBlockedOutcome`). Only a block with
   nothing on disk discards staging.
3. **Build log lines**, mirroring the agent designer's `build_id` tracing:
   start (workspace, skill, backend), coder returned (stop_reason, script
   verified/ran), decision (blocked / no SKILL.md / vetting verdict / ready),
   finished with duration. `grep build_id=<id>` reconstructs one build.

### Part B — the build writes an install step, installs nothing

1. `BuildSkillImplementationPrompt` gains a `<dependencies>` section:
   - Declare Python packages in frontmatter as `metadata.install` entries of
     `kind: pip` with `package:` holding a pip requirement (name with version
     pin, or a `git+https://` URL pinned to a tag or commit).
   - Do **not** install them during this build. Write SKILL.md so that the
     agent runs the install command (Part C) before first use, and invokes the
     script with that environment's interpreter.
   - Test without the dependency: `--help`, argument validation, the
     "dependency missing" error path, and the logic against a stand-in. A live
     end-to-end run is not expected; say in `[TEST_OUTPUT]` what was not
     exercised. **This is not a reason to emit `[BLOCKED]`.**
   - Never write a secret's value to disk.
   - **Import the dependency lazily** (inside the function that needs it, after
     argument parsing), so `--help` and the error paths run and print output
     with the package absent. That is what satisfies the API engine's
     script-verification gate (`SkillBuildSpec` Gate 2 — kept unchanged); a
     top-level import makes even `--help` fail, the gate keeps nudging, and the
     build runs out of turns — the likely path to the original failure.
2. The skill build's capability text says *"Do not install packages during this
   build; the agent installs at run time."* (Part C covers the run variant.)
3. The skill build sets a second marker, `ROOKERY_BUILD_KIND=skill`, alongside
   `buildphase.Generation` (which agent builds share). It is the **single
   source of truth** for "this is a skill build" on both doors: the API engine
   reads it from its extra env, the CLI subcommand from its process env. Agent
   builds and agent dry runs may install (they run real end-to-end tests).
4. `skill-creator`'s SKILL.md is updated to match: the `pip` install kind, the
   per-skill environment path, the install command, and that the builder does
   not install.

### Part C — installing at run time, contained

**One implementation, two doors** (the repo's standing bridge pattern, but no
bridge is needed): a `deps` package in `internal/skilldeps` does the work; the
API engine calls it in-process through a native tool, and a CLI coder calls the
`rookery deps …` subcommand from its own `bash`. Both end in the same Go
function, which spawns its own, *narrower* sandbox — Landlock restrictions
stack, so a process already confined by the CLI coder's sandbox can confine its
child further.

**Location.** `<workspace home>/.local/share/rookery/envs/<name>/`, a Python
venv created with host `python3 -m venv`. `<name>` is a slug (the skill name
for a skill's dependencies; any slug an agent chooses for ad-hoc needs).
A `rookery-env.json` manifest beside it records each install: requested
specs, resolved name/version list, timestamp, agent, and the malicious-package
check result.

**`install` — containment of install-time code:**
- Environment is built from scratch: no workspace secrets, no connector/MCP
  bridge tokens, `HOME` and `TMPDIR` inside the env directory,
  `PIP_CONFIG_FILE=/dev/null`, `PIP_NO_INPUT=1`, `PIP_DISABLE_PIP_VERSION_CHECK=1`.
- Landlock: read+write **only** the env directory; read-only system paths and
  the interpreter's directory. No workspace home (so no coder credentials, no
  other envs), no knowledge base.
- Requirement validation before pip sees anything: each spec must be a PEP 508
  name (optional extras and version specifier) or an `https://` /
  `git+https://` URL whose host is a DNS name (no IP literals, no `http:`,
  `file:`, or local paths); anything starting with `-` is rejected, so no pip
  option (`--index-url`, `--extra-index-url`, `--trusted-host`, `-r`) can be
  smuggled in. pip always runs with `--index-url https://pypi.org/simple`,
  which closes dependency-confusion via an extra index.
- Two pip passes: `pip install --dry-run --report <file>` resolves the full
  set (including transitive dependencies); the resolved PyPI name/version pairs
  are checked against OSV (`api.osv.dev/v1/querybatch`) for `MAL-` advisories
  (OSV's malicious-package feed). Any hit refuses the install, naming the
  package. Then the real install runs. *Note:* resolving a `git+https`
  requirement already executes its build backend; that happens inside the same
  contained sandbox, so it is no worse than the install itself.
- If OSV is unreachable, the install **proceeds** and the result and manifest
  say the check was skipped (my call; failing closed would make every install
  on a host without that endpoint impossible).
- Output is capped and returned as a milestone line in the run transcript
  (`📦 installed trading-agents: tradingagents 0.3.1 + 87 packages`).

**`run` — containment of import-time code:**
`rookery deps run --env <name> [--secret NAME]… -- <script> [args…]` runs a
script with the env's interpreter in a narrowed sandbox:
- Only the named secrets are passed (each must exist; the skill's
  `metadata.requires.env` is what the agent is told to pass). Every other
  workspace secret, and every bridge token, is absent.
- Landlock: read+write the env directory (which holds its own `TMPDIR`), the
  agent's working directory, and the knowledge base (skills write notes, as
  `trading-agents` does); read-only system paths and the skill's own
  directory. The workspace home is **not** granted, so coder credentials and
  other skills' envs are out of reach.
- `HOME` points into the env directory.
API engine: native tool `run_in_env(env, script, args, secrets)` — exec-gated
like `run_script`. CLI: the subcommand.

**Telling the agent.** `<skill_environment>` lists, for every declared skill
with `kind: pip` entries, the env name, whether it is ready (manifest present
and every requested spec recorded as installed), the exact install call for
both coder kinds, and the run call with the secrets to pass.

All three prompt lines that forbid installs change together (each alone leaves
one coder kind or phase still told "no"):
- tool-calling capability block (`prompts.go` "Do not install packages."),
- full-coder (CLI) capability block ("Do not pip-install anything"),
- `agentArchitectureGateBlock` ("Do NOT pip install or add any new package").
Each becomes: install Python packages only with `install_packages` /
`rookery deps install` into a named environment, run code that uses them with
`run_in_env` / `rookery deps run`, never `pip install` into the home or the
system. A skill build gets the "not during this build" variant (Part B).

**Details (builder-check answers, my calls):**
- **Secret values for `run`** come from the caller's own environment: the API
  engine's run env, or the CLI coder's inherited env. `--secret NAME` must name
  a variable present there; an absent one is an error naming it. No DB lookup.
- **Existing env, new requirements:** install *adds* to the existing venv
  (pip resolves upgrades/downgrades) and appends to the manifest. A conflict
  between two agents' pins is pip's error, surfaced verbatim (residual: shared
  envs).
- **Installs during an agent build or dry run persist.** They are a cache, like
  `$HOME/.local/bin`; a rejected build leaves a populated env that the next
  build reuses.
- **`run` on a missing env creates a bare venv** first, so a skill build's
  stand-in test can run its script with the env's interpreter.
- **`run` gets its own `TMPDIR`** (`<env>/.tmp/<random>`, removed afterwards),
  not the shared `<home>/tmp`, which is inside the home `run` does not grant.
- **`install` has its own 15-minute deadline**, independent of the call that
  invoked it, so a stuck resolve cannot eat the whole run budget silently.
- The OSV and pip requests are made by whichever process runs `install` — the
  host process for the API coder, the `rookery deps` child for the CLI coder.

**Where the tools exist.** Agent runs and agent builds/dry runs: yes. Skill
builds: `install_packages` refuses with a message pointing back to Part B;
`run_in_env` is still offered (a stand-in test may use an empty env). Chat:
no — it has no exec tools today and this does not change that.

### Part D — vetting

`BuildSkillVettingPrompt` and the `skill-vetter` core skill gain two checks:
1. **Secret written to disk → `❌ do not save`.** Any code path that writes an
   environment variable's value (or a variable named like a key/token) to a
   file, a note, a cache or a config. The finding names the file and line and
   says: store the value under Settings → Secrets and read it from
   `os.environ` at run time. The designer's blocked-by-vetting message relays
   it, and the next build attempt sees it in History.
2. **Install step review.** Each `metadata.install` entry and any install
   command in SKILL.md: a source that hides where it downloads from (shortened
   URL, IP literal, base64/obfuscated URL, `curl | sh` from an unknown host)
   → `❌`; an unpinned requirement or an unfamiliar host → `⚠️` with the reason.
   SKILL.md must route installs through `install_packages` / `rookery deps
   install`; a raw `pip install` instruction → `⚠️`.

### Residual exposure (stated, not hidden)

- **A model can still run raw `pip install` in `bash`.** That bypasses every
  install-time containment above. The prompt and the env block steer to the
  tools; nothing enforces it.
- **The agent's own process still holds every secret.** `run` narrows what the
  dependency's code sees, not what the agent sees. Narrowing injection for the
  whole run to declared secrets would break agents that rely on undeclared
  ones; out of scope here.
- **The knowledge base is writable and the network is open** during `run`, so
  a malicious dependency can read and exfiltrate notes. Restricting network by
  host is not possible with Landlock.
- **Envs are shared** across the workspace's agents: one agent's compromised
  env is reachable by another agent's `run` of the same env name.
- **Off Linux there is no sandbox** (`sandbox.Supported()` false): `install`
  and `run` still scrub the environment, but provide no filesystem confinement.
  On Windows, `bash`/`python3` resolution is an existing known gap.

## Constraints

- Go only, no new runtime dependencies (OSV is plain HTTPS + JSON).
- The OSV call uses an ordinary HTTP client (a fixed public host), not the
  private-address guard; the only model-controlled URL is the validated
  requirement, which pip fetches inside the install sandbox.
- Documentation sync obligations: a new CLI subcommand (`rookery deps`),
  new core-skill content, new prompt behaviour → `docs-sync` skill before the
  PR (README, CLAUDE.md, `reference/cli.md` on the website).

## Left to the builder

- Exact tool argument shapes and error wording.
- Test layout; the OSV client gets an injectable base URL for tests.
- Whether `install` and `run` share one sandbox-spec helper.

## Follow-ups (not in this change)

- UI listing a workspace's environments with delete.
- Masking secret values in run transcripts and run notes.
- Per-run secret scoping to declared secrets.
- `uv`-managed interpreters.
