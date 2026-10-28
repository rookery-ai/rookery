# Skill Dependencies Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Skill builds stop discarding finished skills on a mentioned `[BLOCKED]`, write an install step instead of installing, and agents on both coder kinds install and run skill dependencies inside a narrower sandbox, with vetting as the gate.

**Architecture:** A new `internal/skilldeps` package owns per-skill Python venvs under the workspace home (validation, manifest, OSV malicious-package check, contained `Install` and `Run`). The API engine reaches it through two native tools; a CLI coder through `rookery deps install|run`, which nest a tighter Landlock sandbox inside the coder's own. Prompts, the runtime `<skill_environment>` block and the skill vetter are updated to steer to it.

**Tech Stack:** Go (stdlib, urfave/cli/v3, existing `internal/sandbox`), host `python3 -m venv` + pip, OSV HTTPS API.

**Spec:** `docs/superpowers/specs/2026-10-26-skill-dependencies-design.md`

## Global Constraints

- No new Go dependencies.
- Env location: `<workspace home>/.local/share/rookery/envs/<name>/`; manifest file `rookery-env.json` in it.
- Env name: `^[a-z0-9][a-z0-9-]{0,63}$`.
- pip always runs with `--index-url https://pypi.org/simple`, `PIP_CONFIG_FILE=/dev/null`, `PIP_NO_INPUT=1`, `PIP_DISABLE_PIP_VERSION_CHECK=1`.
- Install deadline: 15 minutes, independent of the caller's context deadline (still cancelled by the caller's cancel).
- OSV endpoint: `https://api.osv.dev/v1/querybatch`; a hit is any vuln id with prefix `MAL-`. Unreachable → proceed, `MalwareCheck = "skipped: <reason>"`.
- Skill-build marker: `ROOKERY_BUILD_KIND=skill` (set alongside `buildphase.Generation`), the single source of truth on both doors.
- Install sandbox: RW only the env dir; no secrets, no bridge tokens in env.
- Run sandbox: RW env dir (holding its own `TMPDIR` at `<env>/.tmp/<random>`, removed after), the caller's work dir, the vault; workspace home NOT granted; only named secrets passed.
- Conventional Commits; branch `feat/skill-dependencies`; `GOTOOLCHAIN=auto` for every `go` command.

## Review Focus

1. A single `packages` entry containing whitespace plus an option (`"requests --extra-index-url http://x"`) must be rejected, not split by pip. → Task 3 test.
2. An env name like `../../.claude` or `Trading Agents` must be rejected before any path is built. → Task 3 test.
3. OSV returning 5xx or timing out must not block installing; the result must say the check was skipped. → Task 4 test.
4. `run_in_env` naming a secret that isn't set must error with the NAME only, never echo any value. → Task 5 test.
5. A reply whose first line is `` `[BLOCKED]` is not needed here`` (decorated mention at line start) must not count as a block. → Task 1 test.

---

### Task 1: Line-anchored `[BLOCKED]` parsing in both designers

**Files:**
- Modify: `internal/skilldesigner/flow.go:1222-1233` (`parseBlockedOutput`)
- Modify: `internal/agentdesigner/flow.go` (`parseBlockedOutput`, ~line 2764)
- Test: `internal/skilldesigner/blocked_test.go` (create), `internal/agentdesigner/blocked_anchor_test.go` (create)

**Interfaces:**
- Produces: `parseBlockedOutput(text string) string` — unchanged signature in each package; returns "" unless some line, after `strings.TrimSpace`, starts with the literal `[BLOCKED]` (no decoration stripping). Reason = rest of that line plus following lines up to a line containing `[/BLOCKED]` or end of text, trimmed.

- [ ] **Step 1: Write the failing tests** (same table in both packages)

```go
func TestParseBlockedOutputIsLineAnchored(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"real block", "[BLOCKED]\nno API key\n[/BLOCKED]", "no API key"},
		{"inline reason", "work done\n[BLOCKED] cannot reach host", "cannot reach host"},
		{"mention mid-sentence", "I considered whether a `[BLOCKED]` block is warranted: it is not.\n[TEST_OUTPUT]\nok\n[/TEST_OUTPUT]", ""},
		{"decorated mention at line start", "`[BLOCKED]` is not needed here", ""},
		{"bold mention", "**[BLOCKED]** would be wrong", ""},
		{"no marker", "all good", ""},
	}
	for _, c := range cases {
		if got := parseBlockedOutput(c.in); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure** — `GOTOOLCHAIN=auto go test ./internal/skilldesigner/ ./internal/agentdesigner/ -run TestParseBlockedOutputIsLineAnchored` → FAIL on "mention mid-sentence".
- [ ] **Step 3: Implement** the line scan in both packages (identical body; a comment in each points at the other and at `chat.CleanReply`'s line-anchoring rationale).
- [ ] **Step 4: Run** the two packages' full tests → PASS (existing agentdesigner block tests must still pass).
- [ ] **Step 5: Commit** — `fix(designers): only a line-opening [BLOCKED] is a block`

### Task 2: Skill designer reconciles a block against disk, and logs builds

**Files:**
- Modify: `internal/skilldesigner/flow.go` (`runGeneration` around lines 507-760)
- Test: `internal/skilldesigner/reconcile_test.go` (create)

**Interfaces:**
- Consumes: `parseBlockedOutput` (Task 1), existing `LocateSkillRoot(stagingDir) (string, error)`.
- Produces: `reconcileSkillBlock(stagingDir, blocked string) (proceed bool, caveat string)` — pure except a read of `stagingDir`: `proceed=true` iff `LocateSkillRoot` succeeds AND that SKILL.md's frontmatter parses via `skilllibrary.ParseMeta` with a non-empty name; `caveat` = `"Note: the builder reported a problem it could not resolve: " + blocked` when proceeding.

- [ ] **Step 1: Write failing tests** — `TestReconcileSkillBlockKeepsABuildOnDisk` (temp dir with valid SKILL.md → proceed true, caveat contains the reason), `TestReconcileSkillBlockDiscardsWhenNothingBuilt` (empty dir → false), `TestReconcileSkillBlockDiscardsUnparseableSkill` (SKILL.md without frontmatter → false). Plus a flow-level test using the package's existing fake coder: a coder reply containing `[BLOCKED]\nx` with a valid SKILL.md written to staging ends in `StateVerifying`, and the review message starts with the caveat.
- [ ] **Step 2: Run** → FAIL (function undefined).
- [ ] **Step 3: Implement** `reconcileSkillBlock` and call it where `parseBlockedOutput` is now handled: only when `proceed` is false keep the current cleanup+`markGenerationFailed` path; otherwise continue to guardrails/vetting and prepend `caveat + "\n\n"` to the returned review message.
- [ ] **Step 4: Add build logging** — mint `buildID` (same generator agentdesigner's `startGeneration` uses, copy the helper if unexported) and emit `slog.Info` lines `skilldesigner: build start` (workspace, skill, backend), `skilldesigner: coder returned` (stop_reason, script_verified, script_ran, blocked bool), `skilldesigner: build decision` (decision ∈ blocked|no_skill_md|vetting_blocked|ready), `skilldesigner: build finished` (duration_ms). Every line carries `build_id`. Test: capture `slog` via a test handler in the flow-level test and assert the four messages share one `build_id`.
- [ ] **Step 5: Run** `go test ./internal/skilldesigner/` → PASS. **Commit** — `fix(skilldesigner): keep a finished skill when the builder mentions a blocker; log builds`

### Task 3: `internal/skilldeps` — names, requirements, manifest

**Files:**
- Create: `internal/skilldeps/skilldeps.go`, `internal/skilldeps/manifest.go`
- Test: `internal/skilldeps/skilldeps_test.go`, `internal/skilldeps/manifest_test.go`
- Modify: `internal/buildphase/buildphase.go` (add constants)

**Interfaces:**
- Produces:
  - `buildphase.KindEnvVar = "ROOKERY_BUILD_KIND"`, `buildphase.KindSkill = "skill"`.
  - `skilldeps.EnvsRel = ".local/share/rookery/envs"`
  - `func ValidateEnvName(name string) error`
  - `func EnvDir(home, name string) (string, error)` — validates, returns `filepath.Join(home, EnvsRel, name)`.
  - `func ValidateRequirement(spec string) error`
  - `type Package struct { Name, Version string }` (json `name`,`version`)
  - `type InstallRecord struct { At time.Time; Agent string; Requested []string; Resolved []Package; MalwareCheck string }`
  - `type Manifest struct { Env string; Installs []InstallRecord }`
  - `func ReadManifest(envDir string) (Manifest, error)` — missing file → zero Manifest, nil.
  - `func AppendInstall(envDir string, rec InstallRecord) error` — atomic write (temp + rename).
  - `func (m Manifest) Satisfies(specs []string) bool` — every spec equals some `Requested` entry of some record.
  - `var ErrSkillBuild = errors.New("packages are installed by the agent at run time, not during a skill build — write the install step into SKILL.md instead")`
  - `func SkillBuildActive(getenv func(string) string) bool`

`ValidateRequirement` rules: trimmed non-empty; no whitespace anywhere; must not start with `-`; either (a) PEP 508-ish `^[A-Za-z0-9][A-Za-z0-9._-]*(\[[A-Za-z0-9._,-]+\])?((==|>=|<=|~=|!=|>|<)[A-Za-z0-9.*+!_-]+(,(==|>=|<=|~=|!=|>|<)[A-Za-z0-9.*+!_-]+)*)?$`, or (b) URL with scheme `https` or `git+https`, parsed host a DNS name (`net.ParseIP(host) == nil`, contains a dot), no userinfo.

- [ ] **Step 1: Failing tests**: accept `requests`, `requests==2.32.4`, `pandas>=3.0,<4`, `tradingagents[bedrock]==0.3.1`, `git+https://github.com/TauricResearch/TradingAgents.git@v0.3.1`; reject `""`, `-r req.txt`, `--extra-index-url=https://x`, `"requests --extra-index-url http://x"`, `http://example.com/x.whl`, `file:///tmp/x`, `git+https://1.2.3.4/x.git`, `https://user:pw@github.com/x`, `../local`. Env names: accept `trading-agents`; reject `../../.claude`, `Trading Agents`, `""`, 65-char name. Manifest: append two records, read back, `Satisfies` true for a requested spec and false for an unrequested one; `ReadManifest` on a missing dir returns zero value.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run** → PASS.
- [ ] **Step 5: Commit** — `feat(skilldeps): env names, requirement validation and install manifest`

### Task 4: OSV malicious-package check

**Files:**
- Create: `internal/skilldeps/osv.go`; Test: `internal/skilldeps/osv_test.go`

**Interfaces:**
- Produces: `type Finding struct { Package Package; ID, Summary string }`; `type MalwareChecker interface { Malicious(ctx context.Context, pkgs []Package) ([]Finding, error) }`; `func NewOSVClient(baseURL string, hc *http.Client) *OSVClient` (empty baseURL → `https://api.osv.dev`); `(*OSVClient).Malicious` POSTs `/v1/querybatch` with `{"queries":[{"package":{"name":n,"ecosystem":"PyPI"},"version":v}…]}` in batches of ≤1000 and returns a Finding for each result vuln whose `id` starts with `MAL-` (summary may be empty; batch results only carry ids). Timeout per request 20s.

- [ ] **Step 1: Failing tests** with `httptest`: a response marking one package with `MAL-2024-1234` and another with `GHSA-x` → one Finding (the MAL one); a 503 → non-nil error; request body contains ecosystem `PyPI`.
- [ ] **Step 2-4:** run → FAIL, implement, run → PASS.
- [ ] **Step 5: Commit** — `feat(skilldeps): check resolved packages against OSV malicious-package advisories`

### Task 5: Contained `Install` and `Run`

**Files:**
- Create: `internal/skilldeps/runner.go`, `internal/skilldeps/sandboxspec.go`
- Test: `internal/skilldeps/runner_test.go`, `internal/skilldeps/sandboxspec_test.go`

**Interfaces:**
- Consumes: Tasks 3-4; `sandbox.Spec`, `sandbox.Wrap`, `sandbox.Supported`, `sandbox.SystemReadOnlyPaths`, `sandbox.SystemReadWriteFiles`.
- Produces:
  - `type Runner struct { SelfExe string; Sandbox bool; Python string /* "" → LookPath("python3") */; Checker MalwareChecker; InstallTimeout time.Duration /* 0 → 15m */ }`
  - `type InstallRequest struct { Home, Env, Agent string; Specs []string }`
  - `type InstallResult struct { Env string; Resolved []Package; MalwareCheck, Output string }`
  - `func (r Runner) Install(ctx context.Context, req InstallRequest) (InstallResult, error)`
  - `type RunRequest struct { Home, Env, WorkDir, VaultRoot, Script, Stdin string; Args []string; Secrets map[string]string }`
  - `func (r Runner) Run(ctx context.Context, req RunRequest) (stdout, stderr string, err error)`
  - `func installSpec(envDir string, cmd []string, python, git string) sandbox.Spec` and `func runSpec(envDir, tmpDir, workDir, vaultRoot string, cmd, env []string, python string) sandbox.Spec` (pure; tested directly).

Install sequence: validate env + every spec (any invalid → error naming it, nothing run); `MkdirAll(envDir)`; if `<env>/bin/python` missing, `python3 -m venv <env>` (run through the install sandbox); `<env>/bin/python -m pip install --dry-run --ignore-installed --report <env>/.report.json --index-url https://pypi.org/simple <specs…>`; parse `install[].metadata.{name,version}`; `Checker.Malicious` → any finding = error `refused: <name> <version> is flagged as malicious (<ID>)`; checker error = proceed with `MalwareCheck = "skipped: "+err.Error()`; real install with the same flags minus dry-run/report; `AppendInstall`. Env for every step built from scratch: `PATH=<env>/bin:/usr/bin:/bin`, `HOME=<env>`, `TMPDIR=<env>/.tmp`, `PIP_CONFIG_FILE=/dev/null`, `PIP_NO_INPUT=1`, `PIP_DISABLE_PIP_VERSION_CHECK=1`, `PIP_CACHE_DIR=<env>/.cache`. Output capped at 8 KiB tail.

Run sequence: validate env; create venv if missing (bare); `tmp := <env>/.tmp/<random hex>` removed via defer; env = `PATH=<env>/bin:/usr/bin:/bin`, `HOME=<env>`, `TMPDIR=tmp`, `VIRTUAL_ENV=<env>`, plus exactly `req.Secrets`; cmd = `[<env>/bin/python, Script, Args…]`, `Dir=WorkDir`.

When `!Sandbox || !sandbox.Supported() || SelfExe == ""`, run unwrapped with the same scrubbed env.

- [ ] **Step 1: Failing tests (pure):** `TestInstallSpecGrantsOnlyTheEnv` — `ReadWritePaths == []string{envDir}`, `Env` contains no key from a seeded secret map and contains `PIP_CONFIG_FILE=/dev/null`. `TestRunSpecDoesNotGrantTheHome` — given home `/h` and env `/h/.local/share/rookery/envs/x`, no RW/RO path equals `/h` or is `/h/.claude`; RW contains env, workDir, vaultRoot.
- [ ] **Step 2: Failing tests (behaviour, skipped when `python3` is absent):** `TestInstallRejectsInvalidSpecBeforeRunningAnything` (no venv dir created); `TestInstallRefusesMaliciousPackage` — fake `Python` script (written to `t.TempDir()`) that emulates `-m venv` and writes a report with `evilpkg 1.0`, fake checker returning a finding → error contains `evilpkg` and manifest unchanged; `TestInstallProceedsWhenCheckerUnreachable` → `MalwareCheck` starts with `skipped:` and manifest has one record; `TestRunPassesOnlyNamedSecrets` — script prints `os.environ` keys; with `Secrets{"A":"1"}` and process env carrying `B=2`, output contains `A` and not `B`; `TestRunRemovesItsTmp`. `Sandbox: false` in these tests.
- [ ] **Step 3: Implement.** **Step 4: Run** `go test ./internal/skilldeps/` → PASS.
- [ ] **Step 5: Commit** — `feat(skilldeps): install and run skill dependencies in a narrower sandbox`

### Task 6: `rookery deps install|run`

**Files:**
- Create: `cmd/rookery/deps_cmd.go`; Modify: `cmd/rookery/main.go` (add `depsCmd()` to `Commands` after `stateCmd()`)
- Test: `cmd/rookery/deps_cmd_test.go`

**Interfaces:**
- Consumes: Task 5 `Runner`, Task 3 `SkillBuildActive`, `ErrSkillBuild`.
- Produces: `depsCmd() *cli.Command` with
  - `install --env NAME --package SPEC` (repeatable, ≥1) → `Runner.Install` with `Home=$HOME`; refuses with `ErrSkillBuild` when `SkillBuildActive(os.Getenv)`; prints `installed <env>: <n> packages (malware check: <MalwareCheck>)` then the output tail.
  - `run --env NAME [--secret NAME]… -- <script> [args…]` → `Runner.Run` with `WorkDir=cwd`, `VaultRoot=$ROOKERY_VAULT_ROOT` (empty → not granted), secrets taken from `os.Getenv`; an unset name → error `secret NAME is not set in this run`. Prints stdout; stderr to stderr; exit non-zero on failure.
  - `Runner.SelfExe` from `os.Executable()`, `Sandbox` from `config` env `ROOKERY_SANDBOX` (same parsing as serve), `Checker = NewOSVClient("", nil)`.

- [ ] **Step 1: Failing tests:** `install` with `ROOKERY_BUILD_KIND=skill` set returns `ErrSkillBuild`; `run --secret MISSING -- x.py` errors naming `MISSING`; command is registered in the root `Commands`.
- [ ] **Step 2-4:** FAIL → implement → PASS.
- [ ] **Step 5:** Coder side: the CLI coder's runs already grant `Bash`; additionally set `ROOKERY_VAULT_ROOT` in the run env where the runner assembles `extraEnv` (`internal/agentrunner/runner.go` ~line 489) — test in agentrunner asserting the key is present in the env passed to the coder (use the existing fake-coder env capture).
- [ ] **Step 6: Commit** — `feat(cli): rookery deps install|run for CLI coders`

### Task 7: API engine tools `install_packages` and `run_in_env`

**Files:**
- Modify: `internal/coder/hosttools.go` (declare under `includeExecTools`, dispatch cases, args fields `Env`, `Packages`, `Script`, `Secrets`); Create: `internal/coder/depstools.go`
- Test: `internal/coder/depstools_test.go`

**Interfaces:**
- Consumes: Task 5 `Runner`; `h.userHomeDir()`, `h.subprocessEnv`, `h.vaultRootPath()`, `h.agentName`, `h.selfExe`, `h.sandbox`.
- Produces: `func (h *hostToolSet) installPackages(ctx context.Context, env string, pkgs []string) (string, error)` and `func (h *hostToolSet) runInEnv(ctx context.Context, env, script string, args, secretNames []string, stdin string) (string, error)`; field `deps skilldeps.Runner` (zero → built from h's fields; tests inject a fake Python/checker).
- Tool schemas: `install_packages {env: string, packages: string[]}` (both required); `run_in_env {env: string, script: string, args?: string[], secrets?: string[], stdin?: string}`. `install_packages` returns `error: `+`ErrSkillBuild` text when `h.subprocessEnv[buildphase.KindEnvVar] == buildphase.KindSkill`. Success emits progress milestone `📦 installed <env>: <n> packages`.
- Also replace the `bash` tool description's "Do NOT install packages (no internet-backed pip/apt) — use tools that are already present." with "Install Python packages with install_packages, never pip in bash."

- [ ] **Step 1: Failing tests:** both tools are offered when exec tools are on and absent in chat (`includeExecTools=false`) and in `readOnly`; `install_packages` refused during a skill build; `run_in_env` passes only the named secrets from `subprocessEnv` (fake python as in Task 5); unknown secret name → `error:` result naming it and containing no value from `subprocessEnv`; `install_packages` and `run_in_env` added to `mutatingToolNames`.
- [ ] **Step 2-4:** FAIL → implement → `go test ./internal/coder/` PASS.
- [ ] **Step 5: Commit** — `feat(coder): install_packages and run_in_env host tools`

### Task 8: Prompts — install policy, skill build `<dependencies>`, skill-build marker

**Files:**
- Modify: `internal/prompts/prompts.go` (`coderCapabilitiesBlock` lines ~647-750, `agentArchitectureGateBlock` ~797, `BuildSkillImplementationPrompt` ~2766), `internal/skilldesigner/flow.go:599` (marker), `internal/skilllibrary/skills/skill-creator/SKILL.md`
- Test: `internal/prompts/installpolicy_test.go` (create), `internal/skilldesigner/flow_test.go` (marker assertion), `internal/skilllibrary/catalog_test.go` (existing tests must pass)

**Interfaces:**
- Produces: `type InstallPolicy int` with `InstallAtRuntime` (zero) and `InstallNotDuringBuild`; `func coderCapabilitiesBlockFor(backendType string, p InstallPolicy) string`; `coderCapabilitiesBlock(bt)` = `coderCapabilitiesBlockFor(bt, InstallAtRuntime)`; `const installAtRuntimeLine` and `installNotDuringBuildLine` (exact copy below).

Exact copy:
- `installAtRuntimeLine`: "Install Python packages ONLY with install_packages (CLI: `rookery deps install --env <name> --package <spec>`) into a named environment, and run code that uses them with run_in_env (CLI: `rookery deps run --env <name> --secret <NAME> -- <script>`). Never pip install into your home or the system."
- `installNotDuringBuildLine`: "Do NOT install packages during this build — the agent that uses this skill installs them at run time. Declare them and write the install step into SKILL.md instead."

Replace "Do not install packages." (tool-calling), "Do not pip-install anything." (full coder) and agentArchitectureGateBlock's "Do NOT pip install or add any new package." with the policy line. `BuildSkillImplementationPrompt` calls `coderCapabilitiesBlockFor(p.BackendType, InstallNotDuringBuild)` and gains a `<dependencies>` section covering: `metadata.install` `kind: pip` with pinned `package:`; the SKILL.md must tell the agent to call install_packages with env = the skill name before first use and run scripts with run_in_env passing the secrets from `metadata.requires.env`; lazy import of the dependency after argument parsing so `--help` and the "missing dependency" path print output; test without the dependency and say in `[TEST_OUTPUT]` what was not exercised — "that is expected and is NOT a reason to emit [BLOCKED]"; never write a secret's value to disk.
Skill designer adds `buildphase.KindEnvVar: buildphase.KindSkill` to the `extraEnv` map at `flow.go:599`. skill-creator SKILL.md: add a "Python dependencies" section with the same rules and a worked frontmatter example (`kind: pip`, `package: requests==2.32.4`), and fix the platform-conventions bullet list accordingly.

- [ ] **Step 1: Failing tests:** `TestNoPromptForbidsInstallingOutright` — none of `BuildCoderPrompt`, `BuildImplementationPrompt`, `BuildSkillImplementationPrompt` (for each backend type) contains "Do not install packages", "Do not pip-install", or "Do NOT pip install"; runtime/implementation prompts contain `install_packages`; skill implementation prompt contains `installNotDuringBuildLine`, `lazy`/"after argument parsing", and "NOT a reason to emit [BLOCKED]". skilldesigner: captured coder env contains `ROOKERY_BUILD_KIND=skill`.
- [ ] **Step 2-4:** FAIL → implement → `go test ./internal/prompts/ ./internal/skilldesigner/ ./internal/skilllibrary/` PASS (update any existing test that pinned the old sentences — they pinned a policy that changed, not a regression).
- [ ] **Step 5: Commit** — `feat(prompts): skills declare dependencies, agents install them at run time`

### Task 9: `<skill_environment>` lists Python environments

**Files:**
- Modify: `internal/prompts/prompts.go` (`SkillEnvBlock`), `internal/agentrunner/runner.go:389-391` and `resolveSkillBins` neighbour
- Test: `internal/prompts/skillenv_test.go` (create or extend), `internal/agentrunner/skillenv_test.go`

**Interfaces:**
- Produces: `type SkillPyEnv struct { Skill, Env string; Packages, Secrets []string; Ready bool }`; `func SkillEnvBlock(bins []SkillBin, envs []SkillPyEnv, homeDir, vaultRoot, rookeryBin string) string`; `func (r *Runner) resolveSkillEnvs(homeDir string, declaredSkills []string, declaredContent map[string]string) []prompts.SkillPyEnv` — for each declared skill whose `meta.Install` has `kind: pip` entries: `Env = name`, `Packages` = their `Package` values, `Secrets = meta.RequiresEnv`, `Ready = ReadManifest(EnvDir(homeDir,name)).Satisfies(Packages)`.
- Rendering per env: ready → "Python environment `<env>` for skill <skill> is ready." not ready → "… is NOT installed yet. Before first use call install_packages(env=\"<env>\", packages=[…]) — CLI: `<rookeryBin> deps install --env <env> --package <p>…`." Then the run form with `--secret` per `Secrets`.

- [ ] **Step 1: Failing tests:** block for a not-ready env contains the exact CLI install line with each package; ready env contains "is ready"; no envs and no bins and empty vault → "" (existing behaviour). Runner test: skill content with `metadata.install: [{kind: pip, package: requests==2.32.4}]` and `requires.env: [K]` yields one env with `Secrets=[K]`, `Ready=false`, then true after `AppendInstall` with that spec.
- [ ] **Step 2-4:** FAIL → implement (runner passes `connectorBinPath()` as `rookeryBin`) → PASS.
- [ ] **Step 5: Commit** — `feat(agentrunner): tell agents which skill environments need installing`

### Task 10: Vetting checks

**Files:**
- Modify: `internal/skilllibrary/skills/skill-vetter/SKILL.md`, `internal/prompts/prompts.go` (`BuildSkillVettingPrompt` task text)
- Test: `internal/prompts/vetting_test.go` (create or extend), `internal/skilllibrary/catalog_test.go`

**Interfaces:** none new; content only.

Add to the vetter protocol:
- Under "Data exfiltration": **secret written to disk** — any code writing an env var's value (or a variable named like key/token/secret/password) to a file, note, cache or config → `❌ do not save`; the finding names file and line and says "Store this value under Settings → Secrets and read it from os.environ at run time; never write it to disk."
- New "#### Dependencies" under step 2: each `metadata.install` entry / install instruction — hidden source (shortened URL, IP literal, encoded URL, `curl … | sh` from an unknown host) → `❌`; unpinned requirement or unfamiliar host → `⚠️`; SKILL.md instructing raw `pip install` instead of `install_packages`/`rookery deps install` → `⚠️`.
- `BuildSkillVettingPrompt`'s red-flag list gains "secrets written to disk" and "install sources that hide where they download from".

- [ ] **Step 1: Failing test:** vetting prompt contains "written to disk" and "install_packages"; skill-vetter body contains "Settings → Secrets".
- [ ] **Step 2-4:** FAIL → edit → PASS (`go test ./internal/prompts/ ./internal/skilllibrary/`).
- [ ] **Step 5: Commit** — `feat(skill-vetter): block secrets on disk, review install sources`

### Task 11: Documentation sync and full targeted checks

**Files:** `README.md`, `CLAUDE.md`, website `reference/cli.md` (via the `docs-sync` skill), `scripts/docs-sync-internal-env.txt` if `ROOKERY_BUILD_KIND`/`ROOKERY_VAULT_ROOT` must be listed as internal.

- [ ] **Step 1:** Run the `docs-sync` skill; add a `## deps` section to the website CLI reference (`check_cli_coverage` requires it), a CLAUDE.md section "Skill dependencies" summarising the containment and the residuals from the spec, and the key-packages table row for `internal/skilldeps`.
- [ ] **Step 2:** `make ci-fmt ci-vet ci-docs` and `GOTOOLCHAIN=auto go test ./internal/skilldeps/ ./internal/coder/ ./internal/prompts/ ./internal/skilldesigner/ ./internal/agentdesigner/ ./internal/agentrunner/ ./internal/skilllibrary/ ./cmd/rookery/ -count=1` → all PASS.
- [ ] **Step 3: Commit** — `docs: skill dependencies`
