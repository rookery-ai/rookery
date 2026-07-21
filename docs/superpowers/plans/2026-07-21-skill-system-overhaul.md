# Skill System Overhaul Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make skill creation work end to end on the API coder, let skill scripts invoke installed CLI tools, attach skills to every new agent without manual assignment, and refresh the core catalog to match the current architecture.

**Architecture:** Three phases against `docs/superpowers/specs/2026-07-21-skill-system-overhaul-design.md`. Phase 1 removes agent-shaped assumptions from the shared API build engine by giving it a caller-supplied `buildSpec`, splits the Python AST guardrail into two profiles, and fixes the staging-dir file handling. Phase 2 injects the skill catalog into the prompts that actually write AGENT.md and adds a selector-call fallback so attachment does not depend on a weak model emitting a header. Phase 3 rewrites the embedded core-skill catalog and locks it with an invariants test.

**Tech Stack:** Go 1.x, `modernc.org/sqlite`, `testify/require`, `go:embed`, Python 3 (AST guardrail, shelled out), Landlock sandbox.

## Global Constraints

- Go module path is `github.com/ilijad1/simple-agents`. All internal imports use that prefix.
- Tests: `go test ./... -count=1 -timeout 120s` must pass before every commit.
- Build: `go build -o bin/simple-agents ./cmd/simple-agents` must succeed before every commit.
- AST guardrail tests shell out to `python3`; guard new AST tests with `agentdesigner.PythonAvailable()` and `t.Skip` when absent, matching the existing tests.
- All prompt text lives in `internal/prompts`. No inline prompt strings anywhere else — this is an existing invariant of the codebase.
- Existing behaviour for agent builds must not change. Every guardrail and build-gate change is additive via an explicit profile/spec; the agent path keeps its current semantics.
- `agent_skills` is keyed by skill **name**, not id. Core skills have no `skills` table row.
- Never rename an existing core skill directory without checking `agent_skills` for orphaned rows (currently empty — verified 2026-07-21).
- Commit after every task with the shown message.

---

## File Structure

**Phase 1**

| File | Responsibility |
|---|---|
| `internal/agentdesigner/guardrails.go` (modify) | Adds `GuardrailProfile`, profile-parameterised `checkAST`, `shell=True` violation |
| `internal/agentdesigner/guardrails_test.go` (modify) | Profile matrix tests |
| `internal/coder/buildspec.go` (create) | `BuildSpec` type + the two canned specs (agent, skill) + `isAgentScriptPath`/`isSkillScriptPath` |
| `internal/coder/hosttools.go` (modify) | `verifyFinishNudge` reads the spec instead of hard-coding `AGENT.md`/`tools/*.py` |
| `internal/coder/coder.go` (modify) | `WithBuildSpec` modifier + `buildSpec` field |
| `internal/coder/api_engine.go` (modify) | Threads the spec into `hostToolSet`; spec-worded progress string |
| `internal/agentdesigner/toolstree.go` (modify) | Exports `IsTestArtifact` |
| `internal/skilldesigner/staging.go` (create) | `LocateSkillRoot`, `ReadSkillTree`, `SlugifySkillName` — all staging-dir file logic |
| `internal/skilldesigner/staging_test.go` (create) | Hoist / tree round-trip / slug tests |
| `internal/skilldesigner/flow.go` (modify) | Wires the spec, the profile, and the new staging helpers |
| `internal/skilldesigner/designer.go` (modify) | `SaveSkill` uses `ProfileSkillScript` and writes nested paths |
| `internal/prompts/prompts.go` (modify) | Pins the skill authoring layout |

**Phase 2**

| File | Responsibility |
|---|---|
| `internal/prompts/prompts.go` (modify) | `availableSkillsBlock` shared block; `ImplementationParams.Skills`; `BuildSkillSelectionPrompt` |
| `internal/agentdesigner/select_skills.go` (create) | `SelectSkills` — the fallback selector call |
| `internal/agentdesigner/select_skills_test.go` (create) | Selector parsing + failure modes |
| `internal/agentdesigner/flow.go` (modify) | `resolveAgentSkills` used by `saveAndFinish` + `updateAndFinish` |
| `internal/agentdesigner/skills_db_test.go` (modify) | Clobber-rule tests |

**Phase 3**

| File | Responsibility |
|---|---|
| `internal/skilllibrary/catalog_test.go` (create) | Catalog invariants over every embedded skill |
| `internal/skilllibrary/skills/pdf/scripts/*.py` (create) | The helpers `pdf/SKILL.md` documents |
| `internal/skilllibrary/skills/docx/scripts/*.py` (create) | The helpers `docx/SKILL.md` documents |
| `internal/skilllibrary/skills/skill-creator/SKILL.md` (modify) | Widened script contract + authoring-vs-published layout |
| `internal/skilllibrary/skills/skill-vetter/SKILL.md` (modify) | `shell=True` and exfil audit criteria |
| `internal/skilllibrary/skills/web-research/SKILL.md` (create, replaces two) | Merged research skill |
| `internal/skilllibrary/skills/git-and-github/SKILL.md` (create, replaces one) | Local git work, connector pointer |
| `internal/skilllibrary/skills/{kb-curation,change-detection,notification-writing,api-integration,agent-collaboration,resilient-runs,time-and-timezones}/SKILL.md` (create) | Behaviour skills |
| `internal/skilllibrary/skills/{email-triage,calendar-scheduling,image-ocr}/SKILL.md` (create) | Domain skills |

---

# Phase 1 — Skill pipeline correctness

## Task 1: Guardrail profiles

**Files:**
- Modify: `internal/agentdesigner/guardrails.go:41-72,109-159`
- Modify: `internal/agentdesigner/designer.go:81`
- Modify: `internal/agentdesigner/flow.go:872,1649`
- Modify: `internal/skilldesigner/designer.go:46`
- Modify: `internal/skilldesigner/flow.go:562`
- Test: `internal/agentdesigner/guardrails_test.go`
- Test: `internal/agentdesigner/toolstree_test.go:77-95`

**Interfaces:**
- Consumes: nothing.
- Produces: `agentdesigner.GuardrailProfile` (int enum) with `ProfileAgentTool` and `ProfileSkillScript`; `RunToolGuardrails(filename, code string, profile GuardrailProfile) error`; `RunFullGuardrails(code string, profile GuardrailProfile) error`. Task 3 and Task 5 call these with `ProfileSkillScript`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/agentdesigner/guardrails_test.go`:

```go
func TestGuardrailProfiles(t *testing.T) {
	if !agentdesigner.PythonAvailable() {
		t.Skip("python3 not available")
	}
	cases := []struct {
		name      string
		code      string
		agentOK   bool
		skillOK   bool
	}{
		{
			name:    "subprocess list form",
			code:    "import subprocess\nsubprocess.run(['pdftotext', 'a.pdf', '-'], check=True)\n",
			agentOK: false,
			skillOK: true,
		},
		{
			name:    "subprocess check_output list form",
			code:    "import subprocess\nout = subprocess.check_output(['jq', '.'])\n",
			agentOK: false,
			skillOK: true,
		},
		{
			name:    "subprocess with shell=True",
			code:    "import subprocess\nsubprocess.run('ls | wc -l', shell=True)\n",
			agentOK: false,
			skillOK: false,
		},
		{
			name:    "os.system",
			code:    "import os\nos.system('ls')\n",
			agentOK: false,
			skillOK: false,
		},
		{
			name:    "os.popen",
			code:    "import os\nos.popen('ls').read()\n",
			agentOK: false,
			skillOK: false,
		},
		{
			name:    "eval",
			code:    "eval('1+1')\n",
			agentOK: false,
			skillOK: false,
		},
		{
			name:    "__import__",
			code:    "__import__('os')\n",
			agentOK: false,
			skillOK: false,
		},
		{
			name:    "socket",
			code:    "import socket\ns = socket.socket()\n",
			agentOK: false,
			skillOK: false,
		},
		{
			name:    "plain code",
			code:    "import json\nprint(json.dumps({'a': 1}))\n",
			agentOK: true,
			skillOK: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agentErr := agentdesigner.RunFullGuardrails(tc.code, agentdesigner.ProfileAgentTool)
			if tc.agentOK {
				require.NoError(t, agentErr, "agent profile should allow")
			} else {
				require.Error(t, agentErr, "agent profile should block")
			}
			skillErr := agentdesigner.RunFullGuardrails(tc.code, agentdesigner.ProfileSkillScript)
			if tc.skillOK {
				require.NoError(t, skillErr, "skill profile should allow")
			} else {
				require.Error(t, skillErr, "skill profile should block")
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/agentdesigner/ -run TestGuardrailProfiles -count=1`
Expected: FAIL — compile error, `undefined: agentdesigner.ProfileAgentTool`.

- [ ] **Step 3: Add the profile type and parameterise the AST script**

In `internal/agentdesigner/guardrails.go`, replace the `astCheckScript` const (lines 109-147) and `checkAST` (149-159) with:

```go
// GuardrailProfile selects which AST rules apply to a piece of generated Python.
//
// ProfileAgentTool is the historical behaviour for an agent's tools/*.py: no
// subprocess at all, because an agent shells out through its coder's Bash tool
// rather than from inside a helper script.
//
// ProfileSkillScript applies to a skill's scripts/. A skill's entire purpose can be
// to drive an installed CLI tool (pdftotext, pandoc, tesseract), so list-form
// subprocess is permitted. Shell-string execution stays banned in BOTH profiles:
// os.system, os.popen and subprocess(..., shell=True) all evaluate a shell string,
// which is the injection surface the ban exists for. Skills carry two defences an
// agent tool does not — the skill-vetter LLM audit and the Landlock sandbox — which
// is what makes the wider profile acceptable at this boundary and not the other.
type GuardrailProfile int

const (
	ProfileAgentTool GuardrailProfile = iota
	ProfileSkillScript
)

// astCheckBody is the AST checker. ALLOW_SUBPROCESS is prepended by astCheckScript.
const astCheckBody = `
import ast, sys

code = sys.stdin.read()
try:
    tree = ast.parse(code)
except SyntaxError as e:
    print(f"SYNTAX_ERROR: {e}")
    sys.exit(1)

FORBIDDEN_NAMES = {'eval', 'exec', 'compile', '__import__'}
FORBIDDEN_OS_ATTRS = {'system', 'popen', 'execv', 'execve', 'execvp',
                      'spawnv', 'spawnve', 'spawnvp', 'spawnvpe'}

violations = []

class Checker(ast.NodeVisitor):
    def visit_Call(self, node):
        if isinstance(node.func, ast.Name) and node.func.id in FORBIDDEN_NAMES:
            violations.append(f"forbidden call: {node.func.id}()")
        # shell=True evaluates a shell string in ANY profile — same surface as os.system.
        for kw in node.keywords:
            if kw.arg == 'shell' and isinstance(kw.value, ast.Constant) and kw.value.value is True:
                violations.append("forbidden: shell=True")
        if isinstance(node.func, ast.Attribute):
            attr = node.func.attr
            if attr in FORBIDDEN_OS_ATTRS:
                violations.append(f"forbidden: os.{attr}()")
            if isinstance(node.func.value, ast.Name):
                val = node.func.value.id
                if val == 'subprocess' and not ALLOW_SUBPROCESS:
                    violations.append(f"forbidden: subprocess.{attr}()")
                if val == 'socket' and attr == 'socket':
                    violations.append(f"forbidden: socket.socket()")
        self.generic_visit(node)

Checker().visit(tree)
if violations:
    print("VIOLATIONS: " + "; ".join(violations))
    sys.exit(1)
sys.exit(0)
`

func astCheckScript(p GuardrailProfile) string {
	allow := "False"
	if p == ProfileSkillScript {
		allow = "True"
	}
	return "ALLOW_SUBPROCESS = " + allow + "\n" + astCheckBody
}

func checkAST(code string, p GuardrailProfile) error {
	cmd := exec.Command("python3", "-c", astCheckScript(p))
	cmd.Stdin = strings.NewReader(code)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(out.String()))
	}
	return nil
}
```

- [ ] **Step 4: Thread the profile through the exported guardrails**

Replace `RunFullGuardrails` and `RunToolGuardrails` (lines 41-72):

```go
// RunFullGuardrails runs ethics + AST checks on free-form Python (CODE), so it applies
// the FULL keyword set (intent + destructive commands). profile selects the AST rules
// (see GuardrailProfile).
func RunFullGuardrails(code string, profile GuardrailProfile) error {
	if err := checkEthicsCode(code); err != nil {
		return fmt.Errorf("ethics filter: %w", err)
	}
	if PythonAvailable() {
		if err := checkAST(code, profile); err != nil {
			return fmt.Errorf("ast check: %w", err)
		}
	}
	return nil
}

// RunToolGuardrails runs guardrails on a single generated FILE (an agent's tools/*.py or
// requirements.txt, a skill's scripts/*). All of it is code/config, so the full code-context
// ethics check applies to EVERY file; the Python AST check applies only to .py files (parsing
// a non-Python file as Python would spuriously fail). profile selects the AST rules.
func RunToolGuardrails(filename, code string, profile GuardrailProfile) error {
	if err := checkEthicsCode(code); err != nil {
		return fmt.Errorf("ethics filter: %w", err)
	}
	if strings.HasSuffix(filename, ".py") && PythonAvailable() {
		if err := checkAST(code, profile); err != nil {
			return fmt.Errorf("ast check: %w", err)
		}
	}
	return nil
}
```

- [ ] **Step 5: Update all five call sites and the two existing test files**

Agent paths pass `ProfileAgentTool`:

- `internal/agentdesigner/designer.go:81` → `RunToolGuardrails(filename, code, ProfileAgentTool)`
- `internal/agentdesigner/flow.go:872` → `RunToolGuardrails(name, code, ProfileAgentTool)`
- `internal/agentdesigner/flow.go:1649` → `RunToolGuardrails(filename, code, ProfileAgentTool)`

Skill paths pass `ProfileSkillScript`:

- `internal/skilldesigner/designer.go:46` → `agentdesigner.RunToolGuardrails(filename, code, agentdesigner.ProfileSkillScript)`
- `internal/skilldesigner/flow.go:562` → `agentdesigner.RunToolGuardrails(filename, code, agentdesigner.ProfileSkillScript)`

In `internal/agentdesigner/guardrails_test.go`, change every `RunFullGuardrails(x, "")` to `RunFullGuardrails(x, agentdesigner.ProfileAgentTool)` (lines 30, 36, 50, 59, 69, 79) and `RunToolGuardrails("tools/x.py", code)` to `RunToolGuardrails("tools/x.py", code, agentdesigner.ProfileAgentTool)` (line 104).

In `internal/agentdesigner/toolstree_test.go`, add `agentdesigner.ProfileAgentTool` as the third argument at lines 81, 85, 91.

- [ ] **Step 6: Run the full suite**

Run: `go build ./... && go test ./internal/agentdesigner/ ./internal/skilldesigner/ -count=1`
Expected: PASS, including the new `TestGuardrailProfiles`.

- [ ] **Step 7: Commit**

```bash
git add internal/agentdesigner internal/skilldesigner
git commit -m "feat(guardrails): split the AST check into agent-tool and skill-script profiles

A skill's purpose can be to drive an installed CLI tool, but checkAST banned
subprocess outright, so no generated skill script could invoke pdftotext,
pandoc or tesseract — the very binaries cli-tool-installer teaches the user to
install.

ProfileSkillScript permits list-form subprocess. Shell-string execution stays
banned in both profiles, and shell=True becomes an explicit violation rather
than an omission: it is the same injection surface os.system is banned for.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 2: Parameterize the build-finish guard

**Files:**
- Create: `internal/coder/buildspec.go`
- Modify: `internal/coder/hosttools.go:40-111` (struct), `:362-383` (`isAgentScriptPath`), `:416-472` (`verifyFinishNudge`)
- Modify: `internal/coder/coder.go:78-107` (struct), append modifier
- Modify: `internal/coder/api_engine.go:114-123` (progress string), `:395-415` (`buildHostTools`)
- Test: `internal/coder/buildspec_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `coder.BuildSpec` struct with fields `Deliverable string`, `IsScript func(string) bool`, `ProgressNoun string`, `MissingDeliverableNudge func(last bool) string`, `UnverifiedScriptNudge func(last bool) string`; package vars `coder.AgentBuildSpec` and `coder.SkillBuildSpec`; `(*Coder).WithBuildSpec(BuildSpec) *Coder`. Task 3 calls `WithBuildSpec(coder.SkillBuildSpec)`.

- [ ] **Step 1: Write the failing test**

Create `internal/coder/buildspec_test.go`:

```go
package coder

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// A skill build must be nudged toward SKILL.md, never AGENT.md — the bug that made
// every skill build fail (see the SP10 spec, §1.1).
func TestVerifyFinishNudgeUsesTheBuildSpecDeliverable(t *testing.T) {
	dir := t.TempDir()

	h := &hostToolSet{verifyBuild: true, workDir: dir, spec: SkillBuildSpec}
	nudge := h.verifyFinishNudge()
	require.Contains(t, nudge, "SKILL.md")
	require.NotContains(t, nudge, "AGENT.md")

	h2 := &hostToolSet{verifyBuild: true, workDir: dir, spec: AgentBuildSpec}
	require.Contains(t, h2.verifyFinishNudge(), "AGENT.md")
}

// Once the deliverable exists and no script is unverified, the build may finish.
func TestVerifyFinishNudgeAllowsFinishOnceDeliverableExists(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: x\n---\nbody\n"), 0o600))

	h := &hostToolSet{verifyBuild: true, workDir: dir, spec: SkillBuildSpec}
	require.Equal(t, "", h.verifyFinishNudge())
}

// Gate 2 applies to a skill's scripts/, which the agent-shaped predicate never matched.
func TestSkillBuildSpecRecognisesItsScripts(t *testing.T) {
	require.True(t, SkillBuildSpec.IsScript("scripts/extract.py"))
	require.True(t, SkillBuildSpec.IsScript("scripts/extract.sh"))
	require.False(t, SkillBuildSpec.IsScript("SKILL.md"))
	require.False(t, SkillBuildSpec.IsScript("references/api.md"))
	require.False(t, SkillBuildSpec.IsScript("scripts/tests/test_extract.py"))

	require.True(t, AgentBuildSpec.IsScript("tools/fetch.py"))
	require.False(t, AgentBuildSpec.IsScript("scripts/extract.py"))
}

// An unset spec must behave exactly as the agent build did before this change.
func TestZeroSpecDefaultsToAgent(t *testing.T) {
	dir := t.TempDir()
	h := &hostToolSet{verifyBuild: true, workDir: dir}
	require.Contains(t, h.verifyFinishNudge(), "AGENT.md")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/coder/ -run 'TestVerifyFinishNudge|TestSkillBuildSpec|TestZeroSpec' -count=1`
Expected: FAIL — `undefined: SkillBuildSpec`, `unknown field spec`.

- [ ] **Step 3: Create the build spec**

Create `internal/coder/buildspec.go`:

```go
package coder

import (
	"path/filepath"
	"strings"
)

// BuildSpec describes what the current BUILD must produce, so the API engine's
// finish-verification gates are not hard-coded to one caller's shape.
//
// Before this existed, verifyFinishNudge stat'd "AGENT.md" unconditionally. A skill
// build therefore spent its entire nudge budget being told to write the agent
// definition — a file a skill must never contain — and never produced SKILL.md. The
// engine is shared; what the build must produce is the caller's business.
type BuildSpec struct {
	// Deliverable is the file that must exist at the work-dir root before the build
	// may finish ("AGENT.md" / "SKILL.md").
	Deliverable string

	// IsScript reports whether a work-dir-relative path is an ENTRY-POINT helper the
	// script-verification gate should require real output from. Library modules, test
	// files and package scaffolding must return false.
	IsScript func(path string) bool

	// ProgressNoun goes in the live progress line, e.g. "the agent's script".
	ProgressNoun string

	// MissingDeliverableNudge and UnverifiedScriptNudge produce gate 1 / gate 2 text.
	// last is true on the final nudge, when the model must stop iterating and finish.
	MissingDeliverableNudge func(last bool) string
	UnverifiedScriptNudge   func(last bool) string
}

// AgentBuildSpec is the historical behaviour and the default when no spec is set.
var AgentBuildSpec = BuildSpec{
	Deliverable:  "AGENT.md",
	IsScript:     isAgentScriptPath,
	ProgressNoun: "the agent's script",
	MissingDeliverableNudge: func(last bool) string {
		if last {
			return "You still have not written AGENT.md — the agent's instructions — and you're out of attempts to " +
				"keep iterating. Write AGENT.md NOW with write_file (the agent's full instructions: what it does step " +
				"by step, how it calls the helper and uses the result, the [CHAT] message it sends the user, and any " +
				"schedule), then finish. Do NOT try to run or fix the helper script anymore — at build time it cannot " +
				"reach the live service (outbound is blocked), so its empty output is expected, not a failure."
		}
		return "Before you finish: you wrote a helper script but you have NOT written AGENT.md yet — the agent's full " +
			"instructions, which are the actual deliverable. Write AGENT.md now with write_file (what the agent does " +
			"step by step, how it calls the helper and uses the result, the [CHAT] message it sends the user, and any " +
			"schedule). Then finish. At build time the helper cannot reach the live service (outbound is blocked), so " +
			"its empty output is EXPECTED — do not keep trying to run or fix it; write AGENT.md and finish."
	},
	UnverifiedScriptNudge: func(last bool) string {
		if last {
			return "You have tried several times and the helper script still isn't returning real data. " +
				"Stop trying to fix it now and finish. Choose the honest option:\n" +
				"- If this genuinely cannot be done, emit a [BLOCKED] block explaining in PLAIN, NON-TECHNICAL " +
				"language what could not be done (for example: \"I wasn't able to read your emails\") and suggest " +
				"ONE alternative — no code, no file names, no technical terms.\n" +
				"- If the empty result is actually CORRECT right now (there truly is nothing to report), say that " +
				"plainly and finish normally.\n" +
				"- Or, if you can accomplish the goal WITHOUT that script (doing the work yourself from data you can " +
				"already obtain with a minimal fetch), do that now."
		}
		return "Before you finish: you wrote a helper script but it has not yet returned any real data. " +
			"An empty result almost always means it is BROKEN — do not ship it. Run it (run_script), read exactly " +
			"what it prints, and fix the cause (print the raw API response, check the field names, correct the " +
			"logic), then run it again — repeat until it returns real data. For a SINGLE small result, keep the " +
			"script THIN — load its secret from the environment, make the request, print the raw result — and do " +
			"the parsing, decisions, and formatting YOURSELF from what it printed. But when the task processes MANY " +
			"items or LARGE data (porting pages, exporting a dataset), do the OPPOSITE: have the script do the whole " +
			"job — fetch AND write each destination file itself (it already has the paths) — and print only a short " +
			"summary/manifest (counts + file paths), NEVER the full data. Routing a big payload through your reasoning " +
			"gets truncated and burns the run. Never print, log, or return a secret value."
	},
}

// SkillBuildSpec is the skill-creator build. Gate 2 is KEPT: skill-creator already
// mandates a smoke run (`python3 scripts/x.py --help`, or against a small fixture) and
// such a run satisfies the gate, so the gate enforces the contract the skill format
// already requires. What differs is the wording — the agent nudge talks about reaching a
// live service, which would send a skill build chasing data its script was never meant to
// fetch.
var SkillBuildSpec = BuildSpec{
	Deliverable:  "SKILL.md",
	IsScript:     isSkillScriptPath,
	ProgressNoun: "the skill's script",
	MissingDeliverableNudge: func(last bool) string {
		if last {
			return "You still have not written SKILL.md — the skill itself — and you're out of attempts to keep " +
				"iterating. Write SKILL.md NOW with write_file, at the ROOT of the current directory (not inside a " +
				"sub-folder). It needs YAML frontmatter with name and description, then the markdown body that " +
				"teaches an agent how to do the task. Then finish. Stop working on the scripts."
		}
		return "Before you finish: you have NOT written SKILL.md yet — the skill itself, which is the actual " +
			"deliverable. Write it now with write_file, at the ROOT of the current directory (not inside a " +
			"sub-folder named after the skill — that folder already exists). It needs YAML frontmatter with name " +
			"and description, then the markdown body that teaches an agent how to do the task. Then finish."
	},
	UnverifiedScriptNudge: func(last bool) string {
		if last {
			return "You have tried several times and the script still hasn't produced any output. Stop iterating " +
				"and finish now. If the script works but simply needs real input you don't have at build time, say " +
				"so plainly and finish. If it genuinely cannot work, emit a [BLOCKED] block explaining in plain, " +
				"non-technical language what could not be done and suggest ONE alternative."
		}
		return "Before you finish: you wrote a script but it has not produced any output yet. A skill's script is a " +
			"reusable tool, so smoke-test it the way it will actually be called — run it with `--help`, or against a " +
			"small fixture file you create in the current directory — and read exactly what it prints. Fix any error " +
			"and run it again. Do not ship a script you have never seen run. Never print, log, or return a secret value."
	},
}

// isSkillScriptPath reports whether path is an entry-point script in a skill's scripts/
// directory. Test files and library modules are excluded for the same reason they are in
// isAgentScriptPath: they are not the thing that must produce output.
func isSkillScriptPath(path string) bool {
	p := filepath.ToSlash(strings.TrimSpace(path))
	if !strings.HasSuffix(p, ".py") && !strings.HasSuffix(p, ".sh") {
		return false
	}
	if !strings.HasPrefix(p, "scripts/") && !strings.Contains(p, "/scripts/") {
		return false
	}
	base := filepath.Base(p)
	if base == "__init__.py" || base == "conftest.py" ||
		strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py") {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "lib" || seg == "tests" {
			return false
		}
	}
	return true
}
```

- [ ] **Step 4: Rewrite `verifyFinishNudge` to read the spec**

In `internal/coder/hosttools.go`, add to the `hostToolSet` struct next to `verifyBuild` (line 69):

```go
	// spec describes what this build must produce (deliverable file + which paths are
	// entry-point scripts + the nudge wording). The zero value is treated as
	// AgentBuildSpec, so an unset spec keeps the historical agent behaviour.
	spec BuildSpec
```

Add a resolver just above `verifyFinishNudge`:

```go
// buildSpec returns the build spec in force, defaulting to the agent shape so a caller
// that never sets one behaves exactly as before.
func (h *hostToolSet) buildSpec() BuildSpec {
	if h.spec.Deliverable == "" {
		return AgentBuildSpec
	}
	return h.spec
}
```

Replace the body of `verifyFinishNudge` (keep the existing doc comment, updating "AGENT.md" to "the build's deliverable"):

```go
func (h *hostToolSet) verifyFinishNudge() string {
	if !h.verifyBuild || h.verifyNudges >= maxVerifyNudges {
		return ""
	}
	spec := h.buildSpec()

	// Gate 1: the deliverable must exist before the build may finish.
	if md, err := os.Stat(filepath.Join(h.workDir, spec.Deliverable)); err != nil || md.Size() == 0 {
		h.verifyNudges++
		return spec.MissingDeliverableNudge(h.verifyNudges >= maxVerifyNudges)
	}

	// Gate 2: refuse to ship an authored entry-point script that never returned real output.
	if !h.needsScriptVerification() {
		return ""
	}
	h.verifyNudges++
	return spec.UnverifiedScriptNudge(h.verifyNudges >= maxVerifyNudges)
}
```

Change `isAgentScriptPath` (line 362) from a bare function to one the spec references — it keeps its current body and signature, no edit needed beyond leaving it in place. Replace every OTHER call to `isAgentScriptPath(...)` inside `hosttools.go` (in `trackScriptProgress`) with `h.buildSpec().IsScript(...)`.

Run `grep -n 'isAgentScriptPath' internal/coder/hosttools.go` and convert each call site inside a `hostToolSet` method; the function definition itself stays.

- [ ] **Step 5: Add the modifier and thread it through**

In `internal/coder/coder.go`, add to the `Coder` struct after `progress` (line 100):

```go
	buildSpec BuildSpec // what a BUILD must produce; zero value = AgentBuildSpec
```

Append the modifier after `WithSandbox`:

```go
// WithBuildSpec returns a shallow copy of the Coder whose API-engine build gates check
// the given deliverable and script shape. Unset means the agent build (see BuildSpec).
func (c *Coder) WithBuildSpec(spec BuildSpec) *Coder {
	c2 := *c
	c2.buildSpec = spec
	return &c2
}
```

In `internal/coder/api_engine.go`, inside `buildHostTools` (line 395), add to the returned struct literal next to `verifyBuild`:

```go
		spec: c.buildSpec,
```

And in `runToolLoop` (line 116), replace the hard-coded progress string:

```go
				if c.progress != nil {
					c.progress("🔁 verifying " + tools.buildSpec().ProgressNoun + " actually works…")
				}
```

- [ ] **Step 6: Run the tests**

Run: `go build ./... && go test ./internal/coder/ -count=1`
Expected: PASS — the new tests plus the existing `api_engine_test.go` cases at lines 829-884, which construct `hostToolSet{verifyBuild: true}` with no spec and must still see AGENT.md behaviour.

- [ ] **Step 7: Commit**

```bash
git add internal/coder
git commit -m "fix(coder): parameterize the API build-finish guard with a BuildSpec

verifyFinishNudge stat'd AGENT.md unconditionally, so a skill build spent its
entire nudge budget being told to write the agent definition and never produced
SKILL.md — the direct cause of every skill build failing since ad2c3d8.

BuildSpec makes the deliverable, the entry-point-script predicate and the nudge
wording caller-supplied. The zero value is the agent shape, so nothing changes
for agent builds. Gate 2 is kept for skills but reworded: the agent text talks
about reaching a live service, which would send a skill build chasing data its
script was never meant to fetch.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 3: Wire the skill build spec

**Files:**
- Modify: `internal/skilldesigner/flow.go:489-505`

**Interfaces:**
- Consumes: `coder.SkillBuildSpec`, `(*Coder).WithBuildSpec` from Task 2.
- Produces: nothing new.

- [ ] **Step 1: Apply the spec to the generation coder**

In `internal/skilldesigner/flow.go`, replace the `generationCoder` assignment (line 489):

```go
	generationCoder := coderSvc.WithDir(stagingDir).WithAllowedTools("Bash,Write,Edit,Read").
		// This build produces SKILL.md, not AGENT.md. Without the spec the API engine's
		// finish gate demands the agent definition and the build can never end correctly
		// (SP10 spec §1.1). No-op for the CLI engine.
		WithBuildSpec(coder.SkillBuildSpec).
		// Stream the API engine's per-tool-call milestones (🔧 run_script(...), 🔧
		// write_file(...)) to the build SSE, mirroring agentdesigner.runGeneration +
		// agent runs. No-op for the CLI engine.
		WithProgress(notify)
```

- [ ] **Step 2: Verify it builds and the suite is green**

Run: `go build ./... && go test ./internal/skilldesigner/ -count=1`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/skilldesigner/flow.go
git commit -m "fix(skilldesigner): tell the build engine it is building a skill

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 4: Locate SKILL.md wherever the model put it

**Files:**
- Create: `internal/skilldesigner/staging.go`
- Create: `internal/skilldesigner/staging_test.go`
- Modify: `internal/skilldesigner/flow.go:476-481` (drop the pre-created `scripts/`), `:534-549` (read via the new helper)
- Modify: `internal/prompts/prompts.go` — `BuildSkillImplementationPrompt` (line 2101)

**Interfaces:**
- Consumes: nothing.
- Produces: `LocateSkillRoot(stagingDir string) (string, error)` — returns the directory containing `SKILL.md`, either `stagingDir` itself or a unique one-level-down child. Task 5 calls it before reading the tree.

- [ ] **Step 1: Write the failing test**

Create `internal/skilldesigner/staging_test.go`:

```go
package skilldesigner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func TestLocateSkillRootAtRoot(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "SKILL.md"), "---\nname: x\n---\n")

	root, err := LocateSkillRoot(dir)
	require.NoError(t, err)
	require.Equal(t, dir, root)
}

// The observed failure: a weak model nests SKILL.md under <name>/ because that is the
// PUBLISHED layout skill-creator documents. A valid build must not be thrown away over
// a directory level (SP10 spec §1.1a).
func TestLocateSkillRootOneLevelDown(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "pretty-printer")
	writeFile(t, filepath.Join(nested, "SKILL.md"), "---\nname: pretty-printer\n---\n")
	writeFile(t, filepath.Join(nested, "scripts", "fmt.py"), "print('x')\n")

	root, err := LocateSkillRoot(dir)
	require.NoError(t, err)
	require.Equal(t, nested, root)
}

func TestLocateSkillRootAbsent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "notes.txt"), "nothing here\n")

	_, err := LocateSkillRoot(dir)
	require.ErrorIs(t, err, ErrNoSkillMD)
}

// Two candidates are ambiguous — guessing which one is the skill would be worse than
// soft-failing and asking the user.
func TestLocateSkillRootAmbiguous(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a", "SKILL.md"), "---\nname: a\n---\n")
	writeFile(t, filepath.Join(dir, "b", "SKILL.md"), "---\nname: b\n---\n")

	_, err := LocateSkillRoot(dir)
	require.ErrorIs(t, err, ErrNoSkillMD)
}

// A root SKILL.md wins even when a nested one also exists.
func TestLocateSkillRootPrefersRoot(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "SKILL.md"), "---\nname: root\n---\n")
	writeFile(t, filepath.Join(dir, "nested", "SKILL.md"), "---\nname: nested\n---\n")

	root, err := LocateSkillRoot(dir)
	require.NoError(t, err)
	require.Equal(t, dir, root)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/skilldesigner/ -run TestLocateSkillRoot -count=1`
Expected: FAIL — `undefined: LocateSkillRoot`.

- [ ] **Step 3: Implement**

Create `internal/skilldesigner/staging.go`:

```go
package skilldesigner

import (
	"errors"
	"os"
	"path/filepath"
)

// ErrNoSkillMD means the build produced no SKILL.md the flow can identify — either none
// exists, or several nested candidates make the choice ambiguous.
var ErrNoSkillMD = errors.New("no SKILL.md found in the staging dir")

// LocateSkillRoot finds the directory that holds the generated SKILL.md.
//
// The prompt tells the model to write SKILL.md at the root of its working directory, but
// a weak model sometimes nests it one level down under a folder named after the skill —
// because that IS the published layout (<name>/SKILL.md) that skill-creator documents.
// Discarding an otherwise valid build over a directory level is the wrong trade, so a
// single unambiguous nested candidate is accepted and its directory becomes the skill
// root. Zero candidates, or more than one, is a soft failure: guessing which of two
// skills the user meant would be worse than asking.
func LocateSkillRoot(stagingDir string) (string, error) {
	if fi, err := os.Stat(filepath.Join(stagingDir, "SKILL.md")); err == nil && !fi.IsDir() {
		return stagingDir, nil
	}

	entries, err := os.ReadDir(stagingDir)
	if err != nil {
		return "", ErrNoSkillMD
	}
	var found []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		child := filepath.Join(stagingDir, e.Name())
		if fi, err := os.Stat(filepath.Join(child, "SKILL.md")); err == nil && !fi.IsDir() {
			found = append(found, child)
		}
	}
	if len(found) == 1 {
		return found[0], nil
	}
	return "", ErrNoSkillMD
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/skilldesigner/ -run TestLocateSkillRoot -count=1 -v`
Expected: PASS, all five cases.

- [ ] **Step 5: Use it in `runGeneration` and stop pre-creating `scripts/`**

In `internal/skilldesigner/flow.go`, replace the staging-dir creation (lines 476-482):

```go
	// Staging dir under the user's vault: <vault>/<workspaceID>/skills/.staging-<name>/.
	// The live skill folder is only written in finalizeSkill after approval. No scripts/
	// dir is pre-created: it did not steer the model (it wrote its own tree anyway) and an
	// empty dir is indistinguishable from one the model chose to leave empty.
	stagingDir := skillstore.SkillDir(f.saver.SkillsDir(), workspaceID, ".staging-"+skillNameSnap)
	_ = os.RemoveAll(stagingDir)
	if err := os.MkdirAll(stagingDir, 0o750); err != nil {
		closeProgress()
		return "", false, "", fmt.Errorf("create staging dir: %w", err)
	}
	cleanupStaging := func() { _ = os.RemoveAll(stagingDir) }
```

Replace the SKILL.md read (lines 534-542):

```go
	// Ground truth: read what the coder actually wrote. The model may have nested the
	// skill one level down (see LocateSkillRoot).
	skillRoot, err := LocateSkillRoot(stagingDir)
	if err != nil {
		cleanupStaging()
		closeProgress()
		f.markGenerationFailed(workspaceID, "the coder didn't create SKILL.md")
		return "The coder didn't create SKILL.md. Tell me what to change and I'll try again.", false, "", nil
	}
	skillMDBytes, err := os.ReadFile(filepath.Join(skillRoot, "SKILL.md"))
	if err != nil {
		cleanupStaging()
		closeProgress()
		f.markGenerationFailed(workspaceID, "the coder didn't create SKILL.md")
		return "The coder didn't create SKILL.md. Tell me what to change and I'll try again.", false, "", nil
	}
	skillMD := strings.TrimSpace(string(skillMDBytes))
```

Then change the two later uses of `stagingDir` for content to `skillRoot`: the `readScriptsFromDisk(filepath.Join(stagingDir, "scripts"))` call at line 544 (replaced wholesale in Task 5) and `f.runTests(stagingDir, ...)` at line 572 → `f.runTests(skillRoot, ...)`.

- [ ] **Step 6: Pin the authoring layout in the prompt**

In `internal/prompts/prompts.go`, inside `BuildSkillImplementationPrompt` (line 2101), add this paragraph to the output-contract section of the prompt:

```go
	sb.WriteString("<output_layout>\n")
	sb.WriteString("Write SKILL.md at the ROOT of your current working directory. Do NOT create a folder named after the skill — the folder already exists and you are inside it.\n")
	sb.WriteString("- SKILL.md            ← at the root, right here\n")
	sb.WriteString("- scripts/<name>.py   ← only if the skill needs deterministic code\n")
	sb.WriteString("- references/<name>.md ← only if the skill needs on-demand reference docs\n")
	sb.WriteString("A published skill lives at <name>/SKILL.md, but you are ALREADY inside that <name> folder. Creating another one nests the skill and the build cannot be saved.\n")
	sb.WriteString("</output_layout>\n\n")
```

- [ ] **Step 7: Run the suite**

Run: `go build ./... && go test ./internal/skilldesigner/ ./internal/prompts/ -count=1`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/skilldesigner internal/prompts/prompts.go
git commit -m "fix(skilldesigner): find SKILL.md when the model nests it one level down

The stuck 'pretty printer' draft contained a VALID SKILL.md under
pretty-printer/, which runGeneration never saw because it reads an exact path.
The model nested it because <name>/SKILL.md is the published layout
skill-creator documents.

Fixed on both sides: the implementation prompt now pins the authoring layout to
the working-directory root, and a unique one-level-down SKILL.md is accepted and
its directory treated as the skill root. Zero or ambiguous candidates keep the
existing soft failure.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 5: Keep every generated file, not just top-level `.py`

**Files:**
- Modify: `internal/agentdesigner/toolstree.go:63` (export `IsTestArtifact`)
- Modify: `internal/agentdesigner/toolstree.go` + `internal/agentdesigner/flow.go` (call sites)
- Modify: `internal/skilldesigner/staging.go` (add `ReadSkillTree`)
- Modify: `internal/skilldesigner/staging_test.go`
- Modify: `internal/skilldesigner/flow.go:544-549,625-651,1029-1049`
- Modify: `internal/skilldesigner/designer.go:45-49`

**Interfaces:**
- Consumes: `LocateSkillRoot` from Task 4.
- Produces: `agentdesigner.IsTestArtifact(absPath, name, scriptRoot string) bool`; `skilldesigner.ReadSkillTree(skillRoot string) (map[string]string, error)` returning paths relative to the skill root with forward slashes, excluding `SKILL.md`.

- [ ] **Step 1: Write the failing test**

Append to `internal/skilldesigner/staging_test.go`:

```go
func TestReadSkillTreeKeepsEveryShippingFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "SKILL.md"), "---\nname: x\n---\n")
	writeFile(t, filepath.Join(dir, "scripts", "extract.py"), "print('hi')\n")
	writeFile(t, filepath.Join(dir, "scripts", "install.sh"), "#!/bin/bash\necho hi\n")
	writeFile(t, filepath.Join(dir, "scripts", "lib", "parse.py"), "X = 1\n")
	writeFile(t, filepath.Join(dir, "references", "api.md"), "# API\n")

	tree, err := ReadSkillTree(dir)
	require.NoError(t, err)

	require.Equal(t, map[string]string{
		"scripts/extract.py":   "print('hi')\n",
		"scripts/install.sh":   "#!/bin/bash\necho hi\n",
		"scripts/lib/parse.py": "X = 1\n",
		"references/api.md":    "# API\n",
	}, tree)
}

func TestReadSkillTreeExcludesSkillMDAndTestArtifacts(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "SKILL.md"), "---\nname: x\n---\n")
	writeFile(t, filepath.Join(dir, "scripts", "run.py"), "print(1)\n")
	writeFile(t, filepath.Join(dir, "sample.pdf"), "%PDF-1.4 binary\n")
	writeFile(t, filepath.Join(dir, "run.out"), "stdout capture\n")

	tree, err := ReadSkillTree(dir)
	require.NoError(t, err)

	require.Contains(t, tree, "scripts/run.py")
	require.NotContains(t, tree, "SKILL.md")
	require.NotContains(t, tree, "sample.pdf")
	require.NotContains(t, tree, "run.out")
}

func TestReadSkillTreeEmpty(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "SKILL.md"), "---\nname: x\n---\n")

	tree, err := ReadSkillTree(dir)
	require.NoError(t, err)
	require.Empty(t, tree)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/skilldesigner/ -run TestReadSkillTree -count=1`
Expected: FAIL — `undefined: ReadSkillTree`.

- [ ] **Step 3: Export `IsTestArtifact`**

In `internal/agentdesigner/toolstree.go`, rename `isTestArtifact` to `IsTestArtifact` and generalise the doc comment:

```go
// IsTestArtifact reports whether a file under a build work dir is a build-time test
// artifact (binary download, run output, scratch probe) rather than shipping source.
// scriptRoot is the absolute path to the build's script directory (an agent's tools/, a
// skill's scripts/); it is used to detect _-prefixed scratch probes that sit at that
// directory's top level (real modules are plain-named there; dunders like __init__.py
// and __main__.py are kept).
func IsTestArtifact(absPath, name, scriptRoot string) bool {
```

Rename its `toolsDir` parameter to `scriptRoot` throughout the body. Then update every caller inside `internal/agentdesigner/` — run `grep -rn 'isTestArtifact' internal/agentdesigner/` and change each to `IsTestArtifact` (call sites are in `toolstree.go`'s `ReadToolsTree` and `cleanupTestArtifacts`, plus `toolstree_test.go`).

- [ ] **Step 4: Implement `ReadSkillTree`**

Append to `internal/skilldesigner/staging.go`:

```go
// ReadSkillTree reads every shipping file under the skill root, keyed by its
// forward-slash path relative to that root. SKILL.md is excluded (the caller reads it
// separately) and build-time test artifacts are dropped.
//
// The old reader took only top-level scripts/*.py, so a .sh helper, a nested library
// module, or a references/ doc was silently lost between the staging dir and the saved
// skill. The skill format allows all three, so the whole tree travels.
func ReadSkillTree(skillRoot string) (map[string]string, error) {
	out := map[string]string{}
	scriptRoot := filepath.Join(skillRoot, "scripts")

	err := filepath.WalkDir(skillRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip dotfile dirs (e.g. the API engine's .sa_out spill dir).
			if path != skillRoot && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		rel, relErr := filepath.Rel(skillRoot, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if rel == "SKILL.md" {
			return nil
		}
		if agentdesigner.IsTestArtifact(path, d.Name(), scriptRoot) {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
```

Add `"strings"` and `"github.com/ilijad1/simple-agents/internal/agentdesigner"` to the imports.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/skilldesigner/ -run TestReadSkillTree -count=1 -v`
Expected: PASS, all three cases.

- [ ] **Step 6: Replace `readScriptsFromDisk` in the flow**

In `internal/skilldesigner/flow.go`, replace the scripts read (line 544):

```go
	scripts, err := ReadSkillTree(skillRoot)
	if err != nil {
		cleanupStaging()
		closeProgress()
		return "", false, "", fmt.Errorf("read skill files: %w", err)
	}
```

Delete `readScriptsFromDisk` (lines 1029-1049) — it has no remaining callers.

Update `runTests` (lines 625-651) to handle the wider set:

```go
func (f *Flow) runTests(skillRoot string, scripts map[string]string, coderTestOut string) string {
	var sb strings.Builder
	if coderTestOut != "" {
		sb.WriteString("Coder test output:\n")
		sb.WriteString(coderTestOut)
		sb.WriteString("\n\n")
	}

	// Only files we can statically check are reported. A references/*.md must never
	// appear as a failed test just because it isn't code.
	var checkable []string
	for _, name := range sortedScriptNames(scripts) {
		if strings.HasSuffix(name, ".py") || strings.HasSuffix(name, ".sh") {
			checkable = append(checkable, name)
		}
	}
	if len(checkable) == 0 {
		if sb.Len() == 0 {
			sb.WriteString("Prompt-only skill — no scripts to run. Validated frontmatter parses and the description reads as a trigger.\n")
		}
		return strings.TrimSpace(sb.String())
	}

	sb.WriteString("Script smoke check:\n")
	for _, name := range checkable {
		path := filepath.Join(skillRoot, name)
		var cmd *exec.Cmd
		if strings.HasSuffix(name, ".sh") {
			cmd = exec.Command("bash", "-n", path)
		} else {
			cmd = exec.Command("python3", "-m", "py_compile", path)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			sb.WriteString(fmt.Sprintf("- ❌ %s: %s\n", name, strings.TrimSpace(string(out))))
		} else {
			sb.WriteString(fmt.Sprintf("- ✅ %s: parses cleanly\n", name))
		}
	}
	return strings.TrimSpace(sb.String())
}
```

- [ ] **Step 7: Make `SaveSkill` write the whole tree**

In `internal/skilldesigner/designer.go`, the guardrail loop (lines 45-49) already iterates every entry; change the profile and let non-`.py` files through the AST check by extension (already handled by `RunToolGuardrails`):

```go
	for filename, code := range scripts {
		if err := agentdesigner.RunToolGuardrails(filename, code, agentdesigner.ProfileSkillScript); err != nil {
			return nil, fmt.Errorf("guardrails (%s): %w", filename, err)
		}
	}
```

Replace the scripts-writing block (lines 56-86) so paths are relative to the skill root rather than forced under `scripts/`:

```go
	// Wipe and recreate the generated subtrees so a revision that drops a file takes
	// effect — the generated set is the full intended set, not a merge with the prior
	// one. SKILL.md is rewritten below; everything else lives under these dirs.
	for _, sub := range []string{"scripts", "references"} {
		if err := os.RemoveAll(filepath.Join(skillDir, sub)); err != nil {
			return nil, fmt.Errorf("clear %s dir: %w", sub, err)
		}
	}
	names := make([]string, 0, len(scripts))
	for n := range scripts {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		// Reject any path escape; generated files must stay inside the skill dir.
		clean := filepath.Clean(n)
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			return nil, fmt.Errorf("unsafe skill file path: %s", n)
		}
		dest := filepath.Join(skillDir, clean)
		if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
			return nil, fmt.Errorf("create skill subdir: %w", err)
		}
		mode := os.FileMode(0o640)
		if strings.HasSuffix(clean, ".sh") {
			mode = 0o750 // a shell helper must be executable to be invokable
		}
		if err := os.WriteFile(dest, []byte(scripts[n]), mode); err != nil {
			return nil, fmt.Errorf("write skill file %s: %w", n, err)
		}
	}
```

- [ ] **Step 8: Run the full suite**

Run: `go build ./... && go test ./... -count=1 -timeout 120s`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/agentdesigner internal/skilldesigner
git commit -m "fix(skilldesigner): keep every generated file, not just top-level .py

readScriptsFromDisk read only scripts/*.py at the top level, so a .sh helper, a
nested library module or a references/ doc was silently dropped between the
staging dir and the saved skill — even though the skill format allows all three.

ReadSkillTree walks the whole skill root, drops SKILL.md and build-time test
artifacts, and preserves relative paths through to SaveSkill. runTests gains
bash -n for .sh and skips files it cannot statically check rather than reporting
them as failures.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 6: Slugify skill names

**Files:**
- Modify: `internal/skilldesigner/staging.go` (add `SlugifySkillName`)
- Modify: `internal/skilldesigner/staging_test.go`
- Modify: `internal/skilldesigner/flow.go:875-885` (`validateSkillName`), `:164-181` (`Start`), `:183-198` (`StartDesign`), `:697-720` (`finalizeSkill`)

**Interfaces:**
- Consumes: nothing.
- Produces: `SlugifySkillName(name string) string`.

- [ ] **Step 1: Write the failing test**

Append to `internal/skilldesigner/staging_test.go`:

```go
func TestSlugifySkillName(t *testing.T) {
	cases := map[string]string{
		"pretty printer":     "pretty-printer",
		"Pretty Printer":     "pretty-printer",
		"PDF → Email":        "pdf-email",
		"my_skill":           "my-skill",
		"  spaced  out  ":    "spaced-out",
		"already-fine":       "already-fine",
		"a--b":               "a-b",
		"CSV/JSON converter": "csv-json-converter",
	}
	for in, want := range cases {
		require.Equal(t, want, SlugifySkillName(in), "input %q", in)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/skilldesigner/ -run TestSlugifySkillName -count=1`
Expected: FAIL — `undefined: SlugifySkillName`.

- [ ] **Step 3: Implement**

Append to `internal/skilldesigner/staging.go`:

```go
// SlugifySkillName normalises a user-typed or model-generated skill name into the
// lowercase-hyphen form the skill convention (and the filesystem) expect.
//
// The name becomes a directory name and the frontmatter `name:` field, so a value like
// "pretty printer" produces a path with a space in it and frontmatter that does not match
// the convention. Everything outside [a-z0-9-] is collapsed to a single hyphen.
func SlugifySkillName(name string) string {
	var sb strings.Builder
	lastHyphen := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			sb.WriteRune(r)
			lastHyphen = false
		default:
			if !lastHyphen && sb.Len() > 0 {
				sb.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	return strings.Trim(sb.String(), "-")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/skilldesigner/ -run TestSlugifySkillName -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Apply it at both entry points and at finalize**

In `internal/skilldesigner/flow.go`, make `validateSkillName` return the slug:

```go
// validateSkillName normalises the name to its slug form and rejects reserved core-skill
// names and empty/invalid names. The slug is what every path and the frontmatter use.
func (f *Flow) validateSkillName(workspaceID, name string) (string, error) {
	slug := SlugifySkillName(name)
	if slug == "" {
		return "", fmt.Errorf("give the skill a name first")
	}
	if skilllibrary.IsCoreSkill(slug) {
		return "", fmt.Errorf("%q is a reserved core-skill name; choose a different name", slug)
	}
	return slug, nil
}
```

In `Start` (line 171), replace the validation and use the slug for the session and the reply:

```go
	slug, err := f.validateSkillName(workspaceID, skillName)
	if err != nil {
		return "", err
	}

	f.sessions[workspaceID] = f.newSession(workspaceID, slug, StateDescribing)

	return fmt.Sprintf(
		"Starting skill \"%s\".\n\nDescribe what this skill should do. Be specific: what task does it handle, and when should it kick in?",
		slug,
	), nil
```

In `StartDesign` (line 189):

```go
	slug, err := f.validateSkillName(workspaceID, skillName)
	if err != nil {
		f.mu.Unlock()
		return "", err
	}
	sess := f.newSession(workspaceID, slug, StateDesigning)
```

In `finalizeSkill` (lines 711-716), re-slugify and re-validate the generated frontmatter name — today it is trusted verbatim:

```go
	meta, _ := skilllibrary.ParseMeta(skillMD)
	// The generated frontmatter name is not the validated one: re-slugify it, and fall
	// back to the session's name if the model produced nothing usable. SaveSkill's
	// core-skill check remains the backstop.
	name := SlugifySkillName(meta.Name)
	if name == "" || skilllibrary.IsCoreSkill(name) {
		name = skillName
	}
```

- [ ] **Step 6: Run the full suite**

Run: `go build ./... && go test ./... -count=1 -timeout 120s`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/skilldesigner
git commit -m "fix(skilldesigner): slugify skill names before they become paths

validateSkillName accepted 'pretty printer', which became a staging directory
with a space in it and frontmatter that violates the skill-name convention. The
slug is now applied at both entry points and re-applied to the model-generated
frontmatter name at finalize, which was previously trusted verbatim.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 7: Phase 1 end-to-end verification

**Files:**
- No source changes expected. If a defect surfaces, fix it in the package that owns it and note it in the commit.

**Interfaces:**
- Consumes: Tasks 1-6.
- Produces: a confirmed-working skill build.

- [ ] **Step 1: Build and deploy**

```bash
make deploy
make status
```
Expected: the server process is running and `logs/server.log` ends with `listening addr=0.0.0.0:8080`.

- [ ] **Step 2: Clear the stuck draft**

The `pretty printer` draft is mid-FSM from the broken build and its history contains a failure note that would bias the retry.

```bash
sqlite3 ~/.simple-agents-v2/simple-agents.db "DELETE FROM skill_drafts WHERE skill_name = 'pretty printer';"
rm -rf ~/.simple-agents-v2/vaults/*/skills/'.staging-pretty printer'
```

- [ ] **Step 3: Create a skill that must shell out**

In the SPA, create a skill named `pdf-page-count` with the description:

> Count the pages in a PDF file. Use when asked "how many pages is this pdf", "page count", or "how long is this document". It should call the pdfinfo or pdftotext command line tool rather than parsing the PDF itself.

Approve when the designer asks.

- [ ] **Step 4: Verify the build reached review**

Expected: the session reaches `StateVerifying` and shows a vetting report plus a script smoke check. Confirm on disk:

```bash
ls ~/.simple-agents-v2/vaults/*/skills/.staging-pdf-page-count/
```
Expected: `SKILL.md` at the root and **no** `AGENT.md` anywhere in the tree.

- [ ] **Step 5: Approve and verify the save**

Approve, then:

```bash
find ~/.simple-agents-v2/vaults/*/skills/pdf-page-count -type f
sqlite3 ~/.simple-agents-v2/simple-agents.db "SELECT name FROM skills;"
grep -rn "subprocess" ~/.simple-agents-v2/vaults/*/skills/pdf-page-count/ || echo "no subprocess (acceptable if the skill is prompt-only)"
```
Expected: `SKILL.md` present, the DB row exists, and any script that shells out survived the guardrail.

- [ ] **Step 6: Record the result**

If all five acceptance points hold, Phase 1 is done. If the build failed, capture `logs/server.log` and the staging tree before changing anything — the failure mode identifies which task regressed.

- [ ] **Step 7: Commit any fixes**

```bash
git add -A
git commit -m "fix(skilldesigner): <what the live run surfaced>

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

If nothing needed fixing, skip this step.

---

# Phase 2 — Skill autodetection

## Task 8: Inject the skill catalog into the implementation prompts

**Files:**
- Modify: `internal/prompts/prompts.go:711-730` (extract the shared block), `:910-940` (`ImplementationParams`), `BuildImplementationPrompt` (line 1125), `BuildEditImplementationPrompt` (line 1360)
- Modify: `internal/agentdesigner/flow.go` — every `prompts.ImplementationParams{...}` literal
- Test: `internal/prompts/prompts_test.go`

**Interfaces:**
- Consumes: `prompts.SkillRef` (existing).
- Produces: `prompts.ImplementationParams.Skills []SkillRef`; the rendered prompt contains an `<available_skills>` block and the `# Skills:` header requirement. Task 10 relies on the header being requested.

- [ ] **Step 1: Write the failing test**

Append to `internal/prompts/prompts_test.go`:

```go
func TestImplementationPromptOffersSkills(t *testing.T) {
	p := prompts.ImplementationParams{
		BackendType: prompts.BackendToolCalling,
		Skills: []prompts.SkillRef{
			{Name: "pdf", Description: "Read and extract text from PDF files."},
			{Name: "csv", Description: "Read, filter and aggregate CSV data."},
		},
	}
	out := prompts.BuildImplementationPrompt("reader", nil, p)

	require.Contains(t, out, "<available_skills>")
	require.Contains(t, out, "pdf")
	require.Contains(t, out, "Read and extract text from PDF files.")
	require.Contains(t, out, "# Skills:")
}

func TestEditImplementationPromptOffersSkills(t *testing.T) {
	p := prompts.ImplementationParams{
		BackendType: prompts.BackendToolCalling,
		Skills:      []prompts.SkillRef{{Name: "pdf", Description: "Read PDFs."}},
	}
	out := prompts.BuildEditImplementationPrompt("reader", nil, p)

	require.Contains(t, out, "<available_skills>")
	require.Contains(t, out, "# Skills:")
}

// With no skills in the pool the block must be omitted entirely rather than rendering
// an empty section that invites the model to invent names.
func TestImplementationPromptOmitsEmptySkillBlock(t *testing.T) {
	out := prompts.BuildImplementationPrompt("x", nil, prompts.ImplementationParams{})
	require.NotContains(t, out, "<available_skills>")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/prompts/ -run 'ImplementationPrompt' -count=1`
Expected: FAIL — `unknown field Skills in struct literal`.

- [ ] **Step 3: Extract the shared block**

In `internal/prompts/prompts.go`, add above `BuildDesignSystemPrompt`:

```go
// availableSkillsBlock renders the skill catalog and the header contract. It is the
// SINGLE source shared by the design prompt and both implementation prompts.
//
// The header requirement used to live only in the design system prompt — the text-only
// conversation that writes nothing to disk — while the prompt that actually authors
// AGENT.md never mentioned skills at all. parseSkillsLine was therefore looking for
// something no prompt had asked the file's author to produce, and no agent on the install
// had a single skill attached.
func availableSkillsBlock(skills []SkillRef) string {
	if len(skills) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("<available_skills>\n")
	sb.WriteString("These skills are available to the agent. A skill is a reusable capability whose full instructions are loaded into the agent's context at run time when the agent declares it.\n\n")
	sb.WriteString("- You MUST include a `# Skills: skill-one, skill-two` header line in AGENT.md (alongside the schedule line) declaring EXACTLY the skills this agent needs.\n")
	sb.WriteString("- List ONLY the skills the agent actually uses at run time — never list all of them, and never omit the line. If the agent genuinely needs none, write `# Skills: none`.\n\n")
	for _, sk := range skills {
		sb.WriteString("- **")
		sb.WriteString(sk.Name)
		sb.WriteString("** — ")
		sb.WriteString(sk.Description)
		sb.WriteString("\n")
	}
	sb.WriteString("</available_skills>\n\n")
	return sb.String()
}
```

Replace the inline skills section in `BuildDesignSystemPrompt` (lines 711-730) with:

```go
	sb.WriteString(availableSkillsBlock(p.Skills))
```

- [ ] **Step 4: Add the field and render it in both implementation prompts**

Add to `ImplementationParams` (line 910), after `ConnectorBin`:

```go
	// Skills is the pool (core + user) offered to the build coder so it can declare the
	// agent's `# Skills:` header. Without this the header is never emitted.
	Skills []SkillRef
```

In `ImplementationParams.capabilitySpec()` (line 924), append before the closing `return`:

```go
	sb.WriteString(availableSkillsBlock(p.Skills))
```

Both `BuildImplementationPrompt` and `BuildEditImplementationPrompt` already call `capabilitySpec()`, so this reaches both. Verify with `grep -n 'capabilitySpec()' internal/prompts/prompts.go` — if either does not call it, add `sb.WriteString(p.capabilitySpec())` at the same position the other uses.

- [ ] **Step 5: Populate the field at every construction site**

Run `grep -n 'prompts.ImplementationParams{' internal/agentdesigner/flow.go` and add `Skills: sess.Skills,` (or `skillRefs`, matching the local variable in scope) to each literal. The session already carries the pool — `DesignSession.Skills` is loaded on start.

- [ ] **Step 6: Run the tests**

Run: `go build ./... && go test ./internal/prompts/ ./internal/agentdesigner/ -count=1`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/prompts internal/agentdesigner
git commit -m "feat(prompts): offer the skill catalog to the prompts that write AGENT.md

The '# Skills:' header requirement lived only in the design system prompt — the
text-only conversation that writes nothing to disk. BuildImplementationPrompt,
which actually authors AGENT.md, had no Skills field and never mentioned skills,
so parseSkillsLine was looking for something no prompt had asked for. Across the
whole install, agent_skills is empty.

availableSkillsBlock is now the single source shared by the design prompt and
both implementation prompts.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 9: The selector fallback

**Files:**
- Create: `internal/agentdesigner/select_skills.go`
- Create: `internal/agentdesigner/select_skills_test.go`
- Modify: `internal/prompts/prompts.go` (add `BuildSkillSelectionPrompt`)

**Interfaces:**
- Consumes: `matchSkillNames`, `splitSkillCandidates`, `bulletCandidates` (existing, unexported, same package); `coder.Coder.Chat`.
- Produces: `SelectSkills(ctx context.Context, c *coder.Coder, workspaceID, agentMD string, pool []prompts.SkillRef) []string` — always returns a non-nil slice; empty on any failure. Task 10 calls it.

- [ ] **Step 1: Write the failing test**

Create `internal/agentdesigner/select_skills_test.go`:

```go
package agentdesigner

import (
	"testing"

	"github.com/ilijad1/simple-agents/internal/prompts"
	"github.com/stretchr/testify/require"
)

var testPool = []prompts.SkillRef{
	{Name: "pdf", Description: "Read PDFs."},
	{Name: "csv", Description: "Read CSVs."},
	{Name: "web-research", Description: "Research on the web."},
}

func TestParseSelectorResponse(t *testing.T) {
	cases := []struct {
		name string
		resp string
		want []string
	}{
		{"bare list", "pdf, csv", []string{"pdf", "csv"}},
		{"prose wrapped", "This agent reads PDFs, so: pdf", []string{"pdf"}},
		{"bullet list", "- pdf\n- web-research\n", []string{"pdf", "web-research"}},
		{"backticked", "`pdf`, `csv`", []string{"pdf", "csv"}},
		{"none", "none", []string{}},
		{"hallucinated dropped", "pdf, quantum-flux", []string{"pdf"}},
		{"empty", "", []string{}},
		{"all hallucinated", "alpha, beta", []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseSelectorResponse(tc.resp, testPool)
			require.NotNil(t, got, "must never return nil")
			require.Equal(t, tc.want, got)
		})
	}
}

// A nil coder must not panic — it degrades to "attach nothing".
func TestSelectSkillsNilCoder(t *testing.T) {
	got := SelectSkills(t.Context(), nil, "ws", "# Agent\nreads pdfs\n", testPool)
	require.NotNil(t, got)
	require.Empty(t, got)
}

func TestSelectSkillsEmptyPool(t *testing.T) {
	got := SelectSkills(t.Context(), nil, "ws", "# Agent\n", nil)
	require.NotNil(t, got)
	require.Empty(t, got)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agentdesigner/ -run 'Selector|SelectSkills' -count=1`
Expected: FAIL — `undefined: parseSelectorResponse`.

- [ ] **Step 3: Add the prompt**

In `internal/prompts/prompts.go`, add near the other skill prompts:

```go
// BuildSkillSelectionPrompt asks a model to pick, from the catalog, the skills an agent
// needs — the fallback for when the build coder omitted the `# Skills:` header.
//
// It is deliberately narrow: one job, no conversation, output constrained to a single
// line of names so the tolerant parser has the least possible drift to absorb.
func BuildSkillSelectionPrompt(agentMD string, skills []SkillRef) string {
	var sb strings.Builder
	sb.WriteString("You are selecting which reusable skills an automated agent needs.\n\n")
	sb.WriteString("Here are the available skills:\n\n")
	for _, sk := range skills {
		sb.WriteString("- ")
		sb.WriteString(sk.Name)
		sb.WriteString(": ")
		sb.WriteString(sk.Description)
		sb.WriteString("\n")
	}
	sb.WriteString("\nHere are the agent's instructions:\n\n---\n")
	sb.WriteString(agentMD)
	sb.WriteString("\n---\n\n")
	sb.WriteString("Which of the skills above does this agent actually need to do its job?\n\n")
	sb.WriteString("Rules:\n")
	sb.WriteString("- Answer with ONLY a comma-separated list of skill names, copied exactly from the list above.\n")
	sb.WriteString("- Include a skill only if the agent's work genuinely requires it. Most agents need none or one or two.\n")
	sb.WriteString("- Do not invent names. Do not explain. Do not add any other text.\n")
	sb.WriteString("- If the agent needs no skills at all, answer exactly: none\n")
	return sb.String()
}
```

- [ ] **Step 4: Implement the selector**

Create `internal/agentdesigner/select_skills.go`:

```go
package agentdesigner

import (
	"context"
	"log/slog"
	"strings"

	"github.com/ilijad1/simple-agents/internal/coder"
	"github.com/ilijad1/simple-agents/internal/prompts"
)

// selectorRetries is how many times the selector call is attempted before giving up.
// One retry absorbs a transient provider hiccup; more would delay a save the user is
// waiting on for a fallback that is already best-effort.
const selectorRetries = 2

// SelectSkills picks the skills an agent needs by asking the model directly. It is the
// fallback for when the build coder omitted the `# Skills:` header entirely.
//
// Tier 1 (the header, requested in the implementation prompt) leans on a weak model
// emitting a specific line. That is exactly the unreliability this exists to cover: on
// the live install, no agent had a single skill attached. Selection now happens whether
// or not the build model cooperated.
//
// It fails CLOSED and loudly: any error, or a response with nothing recognisable in it,
// returns an empty slice and logs a warning. Attaching a guessed skill would be worse
// than attaching none. The return is always non-nil.
func SelectSkills(ctx context.Context, c *coder.Coder, workspaceID, agentMD string, pool []prompts.SkillRef) []string {
	if c == nil || len(pool) == 0 || strings.TrimSpace(agentMD) == "" {
		return []string{}
	}

	prompt := prompts.BuildSkillSelectionPrompt(agentMD, pool)
	var lastErr error
	for attempt := 0; attempt < selectorRetries; attempt++ {
		result, err := c.WithNoTools().Chat(ctx, workspaceID, nil, "", prompt)
		if err != nil {
			lastErr = err
			continue
		}
		names := parseSelectorResponse(result.Text, pool)
		if len(names) > 0 {
			slog.Info("agentdesigner: selector picked skills", "workspace_id", workspaceID, "skills", names)
			return names
		}
		// An explicit "none" and an unparseable answer are indistinguishable here, and
		// both mean "attach nothing" — so stop rather than burning a second call.
		return []string{}
	}

	slog.Warn("agentdesigner: skill selector call failed; attaching no skills",
		"workspace_id", workspaceID, "err", lastErr)
	return []string{}
}

// parseSelectorResponse extracts canonical skill names from the model's answer, reusing
// the same tolerant matcher parseSkillsLine uses so formatting drift (backticks, bullets,
// prose wrapping, alternate delimiters) is already handled and unknown names are dropped.
func parseSelectorResponse(resp string, pool []prompts.SkillRef) []string {
	byLower := make(map[string]string, len(pool))
	for _, s := range pool {
		byLower[strings.ToLower(s.Name)] = s.Name
	}
	if len(byLower) == 0 {
		return []string{}
	}

	// A bullet list is the other common shape; try it first, then the inline split.
	lines := strings.Split(resp, "\n")
	if names := matchSkillNames(bulletCandidates(lines), byLower); len(names) > 0 {
		return names
	}
	return matchSkillNames(splitSkillCandidates(resp), byLower)
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/agentdesigner/ -run 'Selector|SelectSkills' -count=1 -v`
Expected: PASS, all cases.

If the `prose wrapped` case fails, the inline splitter is producing a token like `This agent reads PDFs, so: pdf` — extend `parseSelectorResponse` to also try the last non-empty line of the response before falling through, and add that line to the doc comment.

- [ ] **Step 6: Commit**

```bash
git add internal/agentdesigner/select_skills.go internal/agentdesigner/select_skills_test.go internal/prompts/prompts.go
git commit -m "feat(agentdesigner): add a skill-selector fallback call

Tier 1 (the # Skills header) leans on a weak model emitting a specific line,
which is the unreliability this covers: no agent on the install has a single
skill attached. SelectSkills asks the model directly, reusing parseSkillsLine's
tolerant matcher for the response, and fails closed with a warning rather than
guessing.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 10: Wire selection into save and edit

**Files:**
- Modify: `internal/agentdesigner/flow.go:1949-1966` (`saveAndFinish`), `:2036-2049` (`updateAndFinish`)
- Modify: `internal/agentdesigner/skills_db_test.go`

**Interfaces:**
- Consumes: `SelectSkills` (Task 9), `parseSkillsLine` (existing), `db.ListAgentSkillNames`.
- Produces: `(*Flow).resolveAgentSkills(ctx, workspaceID, agentID, agentMD string, pool []prompts.SkillRef, isEdit bool) []string`.

- [ ] **Step 1: Write the failing test**

Append to `internal/agentdesigner/skills_db_test.go`:

```go
// An explicit "# Skills: none" is a decision. Overriding it would make attachment
// unpredictable, so the selector must not fire.
func TestResolveAgentSkillsRespectsExplicitNone(t *testing.T) {
	f := &Flow{}
	pool := []prompts.SkillRef{{Name: "pdf", Description: "Read PDFs."}}

	got := f.resolveAgentSkills(t.Context(), "ws", "agent-1", "# Skills: none\n\nDo a thing.\n", pool, nil, false)
	require.NotNil(t, got)
	require.Empty(t, got)
}

// A present header is used verbatim; no selector call.
func TestResolveAgentSkillsUsesHeader(t *testing.T) {
	f := &Flow{}
	pool := []prompts.SkillRef{
		{Name: "pdf", Description: "Read PDFs."},
		{Name: "csv", Description: "Read CSVs."},
	}

	got := f.resolveAgentSkills(t.Context(), "ws", "agent-1", "# Skills: pdf\n\nDo a thing.\n", pool, nil, false)
	require.Equal(t, []string{"pdf"}, got)
}

// On an edit, existing attachments the user may have curated by hand are never replaced.
func TestResolveAgentSkillsEditKeepsExisting(t *testing.T) {
	f := &Flow{}
	pool := []prompts.SkillRef{{Name: "pdf", Description: "Read PDFs."}}
	existing := []string{"csv"}

	got := f.resolveAgentSkills(t.Context(), "ws", "agent-1", "Do a thing with no header.\n", pool, existing, true)
	require.Equal(t, []string{"csv"}, got)
}

// With no coder wired, the selector degrades to attaching nothing rather than panicking.
func TestResolveAgentSkillsNoCoderAttachesNothing(t *testing.T) {
	f := &Flow{}
	pool := []prompts.SkillRef{{Name: "pdf", Description: "Read PDFs."}}

	got := f.resolveAgentSkills(t.Context(), "ws", "agent-1", "Do a thing with no header.\n", pool, nil, false)
	require.NotNil(t, got)
	require.Empty(t, got)
}
```

Add `"github.com/ilijad1/simple-agents/internal/prompts"` to the test file's imports if absent. Note these tests are in package `agentdesigner` (internal), not `agentdesigner_test` — check the existing file's package clause and match it; if it is the external test package, move these four tests into a new `internal/agentdesigner/resolve_skills_internal_test.go` with `package agentdesigner`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agentdesigner/ -run TestResolveAgentSkills -count=1`
Expected: FAIL — `f.resolveAgentSkills undefined`.

- [ ] **Step 3: Implement the resolver**

Add to `internal/agentdesigner/flow.go`, in the skills-helpers section (near line 2260):

```go
// resolveAgentSkills decides which skills to attach to an agent at save time.
//
// Three contracts, each a place this could go subtly wrong:
//
//  1. nil vs empty is load-bearing. parseSkillsLine returns nil ONLY when no header
//     exists at all; a present "# Skills: none" returns a non-nil empty slice. The
//     selector fires on nil only — an explicit "none" is a decision, and silently
//     overriding it would make attachment unpredictable.
//  2. An edit never clobbers. If the agent already has attachments, they stand: the user
//     may have curated them on the agent page, and a re-edit must not undo that. Same
//     rule AutoBindTargets uses for connections.
//  3. It fails closed. SelectSkills returns empty on any failure, which is today's
//     behaviour — not a guess.
//
// The return is always non-nil.
func (f *Flow) resolveAgentSkills(ctx context.Context, workspaceID, agentID, agentMD string, pool []prompts.SkillRef, existing []string, isEdit bool) []string {
	if isEdit && len(existing) > 0 {
		return existing
	}

	if declared := parseSkillsLine(agentMD, pool); declared != nil {
		return declared
	}

	// No header at all — the common case on a weak build model. Ask directly.
	var coderSvc *coder.Coder
	if f.coderFor != nil {
		coderSvc = f.coderFor(workspaceID)
	}
	return SelectSkills(ctx, coderSvc, workspaceID, agentMD, pool)
}
```

Confirm the `Flow` field that resolves a coder is named `coderFor` (`grep -n 'coderFor' internal/agentdesigner/flow.go`); if it differs, use the actual name.

- [ ] **Step 4: Call it from both finish paths**

In `saveAndFinish` (lines 1959-1966), replace the parse-and-default block:

```go
	// A brand-new agent has no existing attachments, so no clobber check is needed.
	skillsSnap := f.resolveAgentSkills(ctx, workspaceID, agentIDSnap, agentMD, skillRefs, nil, false)
```

In `updateAndFinish` (lines 2044-2049):

```go
	var existingSkills []string
	if f.db != nil {
		existingSkills, _ = f.db.ListAgentSkillNames(agentIDSnap)
	}
	skillsSnap := f.resolveAgentSkills(ctx, workspaceID, agentIDSnap, agentMD, skillRefs, existingSkills, true)
```

If `ListAgentSkillNames` is not on the flow's `dbStore` interface, add it:

```go
	ListAgentSkillNames(agentID string) ([]string, error)
```

- [ ] **Step 5: Run the tests**

Run: `go build ./... && go test ./internal/agentdesigner/ -count=1`
Expected: PASS.

- [ ] **Step 6: Run the full suite**

Run: `go test ./... -count=1 -timeout 120s`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/agentdesigner
git commit -m "feat(agentdesigner): attach skills on save without manual assignment

resolveAgentSkills runs on both finish paths: a present header wins, an absent
one falls through to the selector call, and an edit with existing attachments is
left alone so a hand-curated set survives a re-edit.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 11: Phase 2 end-to-end verification

**Files:**
- No source changes expected.

**Interfaces:**
- Consumes: Tasks 8-10.
- Produces: a confirmed non-empty `agent_skills`.

- [ ] **Step 1: Deploy**

```bash
make deploy && make status
```

- [ ] **Step 2: Record the baseline**

```bash
sqlite3 ~/.simple-agents-v2/simple-agents.db "SELECT COUNT(*) FROM agent_skills;"
```
Expected: `0` — the documented baseline.

- [ ] **Step 3: Create an agent whose work needs a skill**

In the SPA, create an agent named `pdf-digest` described as:

> Every morning, look for any new PDF files in my knowledge base, read them, and send me a short summary of what each one says.

Approve through to save.

- [ ] **Step 4: Verify a row was written**

```bash
sqlite3 ~/.simple-agents-v2/simple-agents.db \
  "SELECT a.name, s.skill_name FROM agent_skills s JOIN agents a ON a.id = s.agent_id;"
grep -n "^# Skills:" ~/.simple-agents-v2/vaults/*/agents/*/AGENT.md
```
Expected: at least one `agent_skills` row for `pdf-digest`. Whether the header is present tells you which tier fired — both are a pass, but note which in the commit.

- [ ] **Step 5: Confirm the selector path specifically**

If the header WAS present, the fallback is untested live. Force the other path:

```bash
# Pick the agent dir, strip the header, then edit the agent through the SPA and re-save.
sed -i '/^# Skills:/d' ~/.simple-agents-v2/vaults/*/agents/<agent-id>/AGENT.md
sqlite3 ~/.simple-agents-v2/simple-agents.db "DELETE FROM agent_skills WHERE agent_id = '<agent-id>';"
```

Then run an edit through the designer and save. Expected: a row reappears, and `logs/server.log` contains `agentdesigner: selector picked skills`.

- [ ] **Step 6: Confirm the no-clobber rule**

On the agent page, uncheck every skill and save, then run another designer edit and save. Expected: the skill set you chose is unchanged — the selector did not re-add anything.

- [ ] **Step 7: Commit any fixes**

```bash
git add -A
git commit -m "fix(agentdesigner): <what the live run surfaced>

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

Skip if nothing needed fixing.

---

# Phase 3 — Core skill catalog

## Task 12: Catalog invariants test

**Files:**
- Create: `internal/skilllibrary/catalog_test.go`
- Create: `internal/skilllibrary/skills/pdf/scripts/pdf_text.py`
- Create: `internal/skilllibrary/skills/docx/scripts/docx_convert.py`

**Interfaces:**
- Consumes: `skilllibrary.LoadBundled`, `skilllibrary.ParseMeta`, `skilllibrary.CoreSkillContent`, `agentdesigner.RunToolGuardrails`, `agentdesigner.ProfileSkillScript`.
- Produces: the invariants every later Phase 3 task must satisfy.

- [ ] **Step 1: Write the failing test**

Create `internal/skilllibrary/catalog_test.go`:

```go
package skilllibrary_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ilijad1/simple-agents/internal/agentdesigner"
	"github.com/ilijad1/simple-agents/internal/skilllibrary"
	"github.com/stretchr/testify/require"
)

const skillsRoot = "skills"

// Every embedded core skill must parse, be self-consistent, and hold to the same bar as
// a user-generated skill. This is the guard against the drift that shipped pdf/ and docx/
// with a documented scripts/ directory that did not exist.
func TestCoreCatalogInvariants(t *testing.T) {
	entries, err := os.ReadDir(skillsRoot)
	require.NoError(t, err)

	seen := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := e.Name()
		t.Run(dir, func(t *testing.T) {
			content, ok := skilllibrary.CoreSkillContent(dir)
			require.True(t, ok, "skill dir %q is not loadable via CoreSkillContent", dir)

			// ParseMeta returns (SkillMeta, body) — it does not error; an unparseable
			// frontmatter surfaces as an empty Name, which the next assertion catches.
			meta, body := skilllibrary.ParseMeta(content)
			require.NotEmpty(t, strings.TrimSpace(body), "SKILL.md must have a body, not just frontmatter")
			require.NotEmpty(t, meta.Name, "name is required")
			require.NotEmpty(t, meta.Description, "description is required")
			require.Equal(t, dir, meta.Name,
				"frontmatter name must equal the directory name (agent_skills is keyed by name)")
			require.False(t, seen[meta.Name], "duplicate skill name %q", meta.Name)
			seen[meta.Name] = true

			// A description is the trigger signal — a one-liner with no trigger phrases
			// makes the skill invisible to both the designer and the selector.
			require.GreaterOrEqual(t, len(meta.Description), 80,
				"description must state what the skill does AND when it triggers")

			// Every scripts/ path the body references must exist.
			for _, ref := range referencedScriptPaths(content) {
				_, statErr := os.Stat(filepath.Join(skillsRoot, dir, ref))
				require.NoError(t, statErr, "SKILL.md references %q but it does not exist", ref)
			}

			// Every shipped script must pass the skill guardrail profile.
			scriptsDir := filepath.Join(skillsRoot, dir, "scripts")
			walkErr := filepath.Walk(scriptsDir, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					if os.IsNotExist(err) {
						return nil // no scripts/ is fine
					}
					return err
				}
				if info.IsDir() {
					return nil
				}
				data, readErr := os.ReadFile(path)
				require.NoError(t, readErr)
				require.NoError(t,
					agentdesigner.RunToolGuardrails(info.Name(), string(data), agentdesigner.ProfileSkillScript),
					"shipped script %q must pass the skill guardrail profile", path)
				return nil
			})
			if !os.IsNotExist(walkErr) {
				require.NoError(t, walkErr)
			}
		})
	}

	require.NotEmpty(t, seen, "the catalog must not be empty")
}

// referencedScriptPaths finds scripts/<file> paths mentioned in a SKILL.md body.
func referencedScriptPaths(content string) []string {
	var out []string
	seen := map[string]bool{}
	for _, field := range strings.Fields(content) {
		f := strings.Trim(field, "`'\"(),.:;*")
		if !strings.HasPrefix(f, "scripts/") || strings.HasSuffix(f, "/") {
			continue
		}
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

// LoadBundled must agree with what is on disk.
func TestLoadBundledMatchesDisk(t *testing.T) {
	entries, err := os.ReadDir(skillsRoot)
	require.NoError(t, err)
	onDisk := 0
	for _, e := range entries {
		if e.IsDir() {
			onDisk++
		}
	}
	require.Len(t, skilllibrary.LoadBundled(), onDisk)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/skilllibrary/ -run TestCoreCatalogInvariants -count=1 -v`
Expected: FAIL on `pdf` and `docx` — "SKILL.md references scripts/… but it does not exist". If those bodies reference scripts only by prose rather than a `scripts/<file>` path, the test may pass; in that case check `grep -n 'scripts/' internal/skilllibrary/skills/pdf/SKILL.md` and make the body reference the real path in Step 3.

- [ ] **Step 3: Write the missing pdf helper**

Create `internal/skilllibrary/skills/pdf/scripts/pdf_text.py`:

```python
#!/usr/bin/env python3
"""Extract text from a PDF using the pdftotext CLI, falling back to pdfplumber.

Usage:
    python3 pdf_text.py <file.pdf> [--pages 1-5]

Prints the extracted text to stdout. Exits non-zero with a message on stderr if
no extraction backend is available.
"""
import argparse
import os
import shutil
import subprocess
import sys


def find_pdftotext():
    """Return the absolute path to pdftotext, or None.

    Agents run sandboxed with PATH pointing at the operator's directories, so a tool
    installed by cli-tool-installer lives in $HOME/.local/bin and must be found there
    first.
    """
    local = os.path.join(os.path.expanduser("~"), ".local", "bin", "pdftotext")
    if os.path.isfile(local) and os.access(local, os.X_OK):
        return local
    return shutil.which("pdftotext")


def extract_with_cli(binary, path, first, last):
    cmd = [binary, "-layout"]
    if first:
        cmd += ["-f", str(first)]
    if last:
        cmd += ["-l", str(last)]
    cmd += [path, "-"]
    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        raise RuntimeError(result.stderr.strip() or "pdftotext failed")
    return result.stdout


def extract_with_pdfplumber(path, first, last):
    import pdfplumber

    chunks = []
    with pdfplumber.open(path) as pdf:
        pages = pdf.pages
        if first or last:
            pages = pages[(first or 1) - 1 : (last or len(pages))]
        for page in pages:
            chunks.append(page.extract_text() or "")
    return "\n\n".join(chunks)


def parse_pages(spec):
    if not spec:
        return None, None
    if "-" in spec:
        a, _, b = spec.partition("-")
        return int(a), int(b)
    n = int(spec)
    return n, n


def main():
    ap = argparse.ArgumentParser(description="Extract text from a PDF.")
    ap.add_argument("pdf", help="path to the PDF file")
    ap.add_argument("--pages", help="page or range, e.g. 3 or 1-5")
    args = ap.parse_args()

    if not os.path.isfile(args.pdf):
        print(f"no such file: {args.pdf}", file=sys.stderr)
        return 1

    first, last = parse_pages(args.pages)

    binary = find_pdftotext()
    if binary:
        try:
            sys.stdout.write(extract_with_cli(binary, args.pdf, first, last))
            return 0
        except RuntimeError as exc:
            print(f"pdftotext failed ({exc}); trying pdfplumber", file=sys.stderr)

    try:
        sys.stdout.write(extract_with_pdfplumber(args.pdf, first, last))
        return 0
    except ImportError:
        print(
            "no PDF backend available. Install poppler's pdftotext via the "
            "cli-tool-installer skill, or: python3 -m pip install --user pdfplumber",
            file=sys.stderr,
        )
        return 2


if __name__ == "__main__":
    sys.exit(main())
```

- [ ] **Step 4: Write the missing docx helper**

Create `internal/skilllibrary/skills/docx/scripts/docx_convert.py`:

```python
#!/usr/bin/env python3
"""Convert a .docx file to markdown (or another pandoc format) using the pandoc CLI.

Usage:
    python3 docx_convert.py <file.docx> [--to markdown] [--out result.md]

Prints the converted text to stdout unless --out is given.
"""
import argparse
import os
import shutil
import subprocess
import sys


def find_pandoc():
    """Return the absolute path to pandoc, or None.

    cli-tool-installer places binaries in $HOME/.local/bin, which is not on the
    sandboxed agent's PATH, so that location is checked first.
    """
    local = os.path.join(os.path.expanduser("~"), ".local", "bin", "pandoc")
    if os.path.isfile(local) and os.access(local, os.X_OK):
        return local
    return shutil.which("pandoc")


def main():
    ap = argparse.ArgumentParser(description="Convert a .docx via pandoc.")
    ap.add_argument("docx", help="path to the .docx file")
    ap.add_argument("--to", default="markdown", help="pandoc output format (default: markdown)")
    ap.add_argument("--out", help="write to this file instead of stdout")
    args = ap.parse_args()

    if not os.path.isfile(args.docx):
        print(f"no such file: {args.docx}", file=sys.stderr)
        return 1

    binary = find_pandoc()
    if not binary:
        print(
            "pandoc is not installed. Install it with the cli-tool-installer skill, "
            "then call it at $HOME/.local/bin/pandoc",
            file=sys.stderr,
        )
        return 2

    cmd = [binary, args.docx, "-t", args.to]
    if args.out:
        cmd += ["-o", args.out]

    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        print(result.stderr.strip() or "pandoc failed", file=sys.stderr)
        return result.returncode

    if args.out:
        print(f"wrote {args.out}")
    else:
        sys.stdout.write(result.stdout)
    return 0


if __name__ == "__main__":
    sys.exit(main())
```

- [ ] **Step 5: Make both SKILL.md bodies reference the real paths**

In `internal/skilllibrary/skills/pdf/SKILL.md`, add a section:

```markdown
## Helper script

`scripts/pdf_text.py` extracts text with `pdftotext` and falls back to `pdfplumber`:

```bash
python3 scripts/pdf_text.py report.pdf --pages 1-5
```
```

In `internal/skilllibrary/skills/docx/SKILL.md`:

```markdown
## Helper script

`scripts/docx_convert.py` converts a .docx through pandoc:

```bash
python3 scripts/docx_convert.py notes.docx --to markdown
```
```

- [ ] **Step 6: Run the tests**

Run: `go test ./internal/skilllibrary/ -count=1 -v`
Expected: PASS. If `TestCoreCatalogInvariants` fails on a description length for an existing skill, lengthen that description to state both what it does and its triggers rather than lowering the threshold.

- [ ] **Step 7: Commit**

```bash
git add internal/skilllibrary
git commit -m "test(skilllibrary): lock the core catalog with invariants, fix pdf/docx drift

pdf/ and docx/ documented a scripts/ directory that shipped empty (go:embed
skips empty dirs), so the helpers their bodies promised did not exist. Both now
ship real helpers that shell out to pdftotext and pandoc — possible for the
first time now that skill scripts may use subprocess.

The invariants test holds every core skill to the same bar as a user-generated
one: frontmatter parses, name matches the directory, description carries trigger
phrases, referenced script paths exist, and every shipped script passes the
skill guardrail profile.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 13: Update the two meta skills

**Files:**
- Modify: `internal/skilllibrary/skills/skill-creator/SKILL.md`
- Modify: `internal/skilllibrary/skills/skill-vetter/SKILL.md`

**Interfaces:**
- Consumes: the guardrail profile from Task 1; the layout rule from Task 4.
- Produces: nothing in code. These two SKILL.md bodies drive every future generated skill, so their content is the contract.

- [ ] **Step 1: Update `skill-creator`'s script contract**

In `internal/skilllibrary/skills/skill-creator/SKILL.md`, replace the section describing `scripts/` (around line 58 and lines 105-106) with:

```markdown
## Layout: authoring vs published

A PUBLISHED skill lives at `<name>/SKILL.md`. When you are AUTHORING one you are
already inside that folder — write files at the root of your current working
directory. Creating another folder named after the skill nests it and the build
cannot be saved.

```
SKILL.md              ← at the root, right here
scripts/<name>.py     ← optional: deterministic code (.py or .sh)
references/<name>.md  ← optional: on-demand reference docs
```

## Scripts

Scripts may be Python (`.py`) or shell (`.sh`). A script may drive a command line
tool — that is often the whole point of a skill.

- Call CLI tools with `subprocess` and an ARGUMENT LIST:
  `subprocess.run(["pdftotext", path, "-"], capture_output=True, text=True)`.
- NEVER use `shell=True`, `os.system`, or `os.popen`. They evaluate a shell string,
  which is rejected by the safety check and is an injection risk.
- A tool installed by the cli-tool-installer skill lives at `$HOME/.local/bin/<tool>`,
  which is NOT on the sandboxed agent's `PATH`. Resolve it there first, then fall back
  to `shutil.which`.
- Refer to tools by their BARE name in the SKILL.md body; the runtime environment block
  supplies the real path.
- Fail with a clear message naming the missing tool and how to install it, rather than
  crashing with a traceback.

## Testing

Run every script before you finish — `python3 scripts/x.py --help`, or against a small
fixture you create in the working directory. A script you have never seen run must not
ship. Never print, log or return a secret value.
```

- [ ] **Step 2: Update `skill-vetter`'s audit criteria**

In `internal/skilllibrary/skills/skill-vetter/SKILL.md`, add to the checklist section:

```markdown
### Shell execution

- `shell=True` on any `subprocess` call, `os.system`, or `os.popen` — a shell string is
  an injection surface. FLAG. List-form `subprocess.run([...])` is expected and fine:
  driving a CLI tool is what many skills are for.
- A command built by concatenating or interpolating untrusted input into a single string.
  FLAG even in list form.

### Data exfiltration

- Any read of `memory/USER.md`, `memory/SOUL.md`, or the secrets store that is not
  required by the skill's stated purpose. FLAG.
- Network calls to a raw IP address, a URL-shortener, or a hard-coded host unrelated to
  the skill's stated purpose. FLAG.
- Base64, hex, or otherwise obfuscated payloads that are decoded and then executed or
  sent. FLAG unconditionally.

### Scope

- Writes outside the skill's own directory and the paths its description names. FLAG.
- `sudo`, package-manager installs to system paths, or writes to `/usr`, `/etc`, `/bin`.
  FLAG — installs belong in `$HOME/.local/bin` via cli-tool-installer.
```

- [ ] **Step 3: Verify the invariants still hold**

Run: `go test ./internal/skilllibrary/ -count=1`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/skilllibrary/skills/skill-creator internal/skilllibrary/skills/skill-vetter
git commit -m "docs(skills): teach skill-creator and skill-vetter the widened contract

skill-creator still said scripts were .py-only and conflated the published
layout (<name>/SKILL.md) with the authoring layout — the exact confusion that
made a model nest SKILL.md one level down and lose the build. It now separates
the two and teaches list-form subprocess with an explicit shell=True ban.

skill-vetter gains the matching audit criteria so the widened profile is checked
where it is now permitted.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 14: Refocus the three redundant skills

**Files:**
- Create: `internal/skilllibrary/skills/web-research/SKILL.md`
- Create: `internal/skilllibrary/skills/git-and-github/SKILL.md`
- Delete: `internal/skilllibrary/skills/web-search/`, `internal/skilllibrary/skills/web-scraper/`, `internal/skilllibrary/skills/github-integration/`

**Interfaces:**
- Consumes: the invariants from Task 12.
- Produces: two skills replacing three.

- [ ] **Step 1: Confirm no attachments would be orphaned**

```bash
sqlite3 ~/.simple-agents-v2/simple-agents.db \
  "SELECT * FROM agent_skills WHERE skill_name IN ('web-search','web-scraper','github-integration');"
```
Expected: no rows. If any appear, add an `UPDATE agent_skills SET skill_name = 'web-research' WHERE skill_name IN ('web-search','web-scraper');` migration before proceeding — `agent_skills` is keyed by name.

- [ ] **Step 2: Write `web-research`**

Create `internal/skilllibrary/skills/web-research/SKILL.md`:

```markdown
---
name: web-research
description: Use this skill when the user wants something researched on the web — finding current facts, comparing sources, checking documentation, or extracting structured data (a table, a price, an article body) out of a page you fetched. Triggers include "research this", "look up", "what's the latest on", "find recent info on", "scrape this page", "extract the table from", "get the article text", "compare these options".
version: 1.0.0
license: MIT-0
category: Web & Research
---

# Web Research

Search, fetch and extract. This skill covers the JUDGEMENT — the fetching itself is
built in.

## What is already built in

You have `web_search` and `web_fetch` as native tools. Do not write a script to make a
plain public request; call the tool. `web_fetch` already reduces HTML to readable text.

Write a script only when the request needs a secret, a session, or authentication.

## Research strategy

1. **Search broadly first.** One query rarely settles a question. Run two or three
   phrasings — a general one and a specific one — before concluding anything.
2. **Prefer primary sources.** Official docs, the project's own repository, the
   organisation's own site. A blog summarising a doc can be stale or wrong.
3. **Check dates.** A confident answer from three years ago is a wrong answer. Prefer
   pages that state when they were written, and say so when you cannot tell.
4. **Corroborate anything surprising.** If one source claims something the others do not,
   treat it as unverified and say so rather than reporting it as fact.
5. **Report uncertainty plainly.** "I found two conflicting figures" is a useful answer.
   A confident wrong number is not.

## Extracting from a fetched page

`web_fetch` gives you readable text, which is usually enough. When you need STRUCTURE —
a table's rows, every link, a repeated card — parse the HTML.

- Locate the data by a stable anchor (a heading, a label, an id), not by position. Page
  layouts change; "the third div" breaks silently.
- A page that comes back nearly empty is usually JavaScript-rendered. Switch to the
  playwright-browser skill rather than retrying the fetch.
- Extract what was asked for and stop. Dumping the whole page into your reasoning wastes
  the run and truncates the real answer.

## Reporting

Give the answer first, then the sources as links. State what you could not confirm.
```

- [ ] **Step 3: Write `git-and-github`**

Create `internal/skilllibrary/skills/git-and-github/SKILL.md`:

```markdown
---
name: git-and-github
description: Use this skill for work inside a git repository on this machine — cloning, reading history, diffing, branching, committing, and inspecting what changed. Triggers include "clone this repo", "what changed in", "show me the diff", "commit these changes", "check the git log", "which branch", "read the repo". For GitHub's API (issues, pull requests, releases, CI status) use the connected GitHub service instead.
version: 1.0.0
license: MIT-0
category: Development
---

# Git and GitHub

Local repository work. For anything that talks to GitHub's API, use the connected
GitHub account instead — see the bottom of this document.

## Local git

Run git through an argument list, never a shell string:

```python
import subprocess
result = subprocess.run(["git", "-C", repo, "log", "--oneline", "-20"],
                        capture_output=True, text=True)
```

Useful invocations:

| Goal | Command |
|---|---|
| Clone | `git clone --depth 1 <url> <dir>` |
| Recent history | `git -C <dir> log --oneline -20` |
| What changed | `git -C <dir> diff --stat HEAD~1` |
| Current branch | `git -C <dir> rev-parse --abbrev-ref HEAD` |
| Who changed a line | `git -C <dir> blame -L 10,20 <file>` |
| Search history | `git -C <dir> log -S "<string>" --oneline` |

Rules:

- `--depth 1` unless history is the point. A full clone of a large repo wastes the run.
- Clone into `$TMPDIR`, not the knowledge base, unless the user asked to keep it.
- NEVER push, force-push, or rewrite history unless the user explicitly asked in this
  conversation. Reading is safe; writing to a remote is not reversible.
- Read `git status` before committing anything, and report what you are about to commit.

## GitHub's API — use the connection

Issues, pull requests, releases, CI status and code search go through the connected
GitHub account, which is already authenticated. Do NOT hunt for a token, and do not
`pip install` a GitHub SDK — the connection exposes these as tools directly.

If no GitHub account is connected, say so and tell the user to connect one on the
connections page. Do not fall back to unauthenticated scraping.
```

- [ ] **Step 4: Delete the three replaced skills**

```bash
git rm -r internal/skilllibrary/skills/web-search internal/skilllibrary/skills/web-scraper internal/skilllibrary/skills/github-integration
```

- [ ] **Step 5: Run the tests**

Run: `go build ./... && go test ./internal/skilllibrary/ -count=1 -v`
Expected: PASS — `TestLoadBundledMatchesDisk` now counts 12 skills (13 − 3 + 2).

- [ ] **Step 6: Commit**

```bash
git add -A internal/skilllibrary
git commit -m "feat(skills): merge web-search+web-scraper into web-research, refocus github

All three duplicated capability the platform already has: web_search and
web_fetch are native API-engine tools, and GitHub is one of the 28 connectors.
web-research now teaches the judgement the tools do not (source quality, dating,
corroboration, structured extraction), and git-and-github covers local repo work
while pointing at the connector for API calls.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 15: The seven agent-behaviour skills

**Files:**
- Create: `internal/skilllibrary/skills/kb-curation/SKILL.md`
- Create: `internal/skilllibrary/skills/change-detection/SKILL.md`
- Create: `internal/skilllibrary/skills/notification-writing/SKILL.md`
- Create: `internal/skilllibrary/skills/api-integration/SKILL.md`
- Create: `internal/skilllibrary/skills/agent-collaboration/SKILL.md`
- Create: `internal/skilllibrary/skills/resilient-runs/SKILL.md`
- Create: `internal/skilllibrary/skills/time-and-timezones/SKILL.md`

**Interfaces:**
- Consumes: the invariants from Task 12 — each file must satisfy all five.
- Produces: seven core skills.

Every file uses this frontmatter shape. `description` must state what the skill does AND
the phrases that trigger it, and must be at least 80 characters (the invariants test):

```markdown
---
name: <exactly the directory name>
description: Use this skill when <what>. Triggers include "<phrase>", "<phrase>", "<phrase>".
version: 1.0.0
license: MIT-0
category: <Agent Behaviour | Integrations | Productivity>
---
```

- [ ] **Step 1: Write `kb-curation`**

Body must cover: the vault layout (`notes/`, `memory/USER.md`, `memory/SOUL.md`,
`memory/GENERAL.md`, `agents/<id>/`); that `.kb/`, `chats/` and other agents' directories
are off limits; heading structure and `[[wikilink]]` usage; when to append to an existing
note versus create a new one; that `memory/` files are injected into EVERY context so they
must stay short and factual; and that a note's filename should describe its content, not
its date.

Frontmatter description:

```
description: Use this skill whenever writing, updating or organising files in the user's knowledge base — deciding where a note belongs, structuring it in clean markdown, linking it to related notes, and keeping memory files short. Triggers include "save this to my notes", "write this down", "update my notes on", "remember this", "organise my knowledge base", "format this file".
```

- [ ] **Step 2: Write `change-detection`**

Body must cover: the `[STATE]...[/STATE]` protocol and that state merges into `state.md`'s
json fence; storing seen IDs as a list with a bounded size; storing a cursor or timestamp
rather than a full snapshot when the source is ordered; comparing before reporting;
emitting `[SILENT]` when nothing changed; and the first-run problem — a first run with an
empty state should record the baseline and stay silent rather than reporting everything as
new.

Frontmatter description:

```
description: Use this skill for any scheduled agent that should report only what is NEW since its last run — watching a feed, a page, an inbox, a listing or a dataset. Covers storing seen IDs and cursors in state, comparing before reporting, and staying silent when nothing changed. Triggers include "notify me when", "watch for new", "alert me if", "check for updates", "only tell me what changed".
```

- [ ] **Step 3: Write `notification-writing`**

Body must cover: `[CHAT]` carries the whole message and blank lines are part of it; lead
with the answer, not the process; a phone-screen length budget; no internal file paths,
script names or tool names in a user-facing message; when to send nothing (`[SILENT]`);
and that a failed run should say what failed in plain language rather than pasting a stack
trace.

Frontmatter description:

```
description: Use this skill when composing the message an agent sends to the user — deciding what belongs in a notification, how short it should be, when to stay silent, and how to report a failure in plain language. Triggers include "notify me", "send me a summary", "message me when", "what should the alert say", "keep it brief".
```

- [ ] **Step 4: Write `api-integration`**

Body must cover: checking whether the service is already a connected account FIRST (the
28 connectors) before writing any HTTP code; reading a secret from an environment variable
and never printing it; pagination; honouring `Retry-After` and backing off on 429; treating
4xx and 5xx differently; and keeping the script thin — fetch and print raw, reason in the
agent — except for bulk jobs, where the script should do the whole job and print a summary.

Frontmatter description:

```
description: Use this skill when calling a REST API that is not one of the connected services — authenticating with a stored secret, paginating, handling rate limits, and failing cleanly. Triggers include "call the API", "fetch from their endpoint", "use my API key for", "integrate with", "pull data from their service".
```

- [ ] **Step 5: Write `agent-collaboration`**

Body must cover: the `[CALL: <agent-name>]` marker, that it is synchronous, the max depth
of 3 and cycle detection; when splitting work across agents is right (a genuinely separate
schedule or a reusable capability) versus wrong (one task artificially split); that the
called agent's output comes back into the caller's context; and that a called agent's
notification behaviour is its own.

Frontmatter description:

```
description: Use this skill when one agent needs to invoke another to do part of its job, or when deciding whether a task should be one agent or several. Covers the call protocol, depth limits, and when splitting work across agents helps versus hurts. Triggers include "have it call", "use the other agent", "should this be two agents", "reuse that agent".
```

- [ ] **Step 6: Write `resilient-runs`**

Body must cover: distinguishing a transient failure (network, 429, 5xx) from a permanent
one (404, bad credentials, malformed input); retrying the first with backoff and never the
second; reporting partial results explicitly rather than silently dropping them; never
claiming success that did not happen; recording in state what was completed so the next run
resumes rather than repeats; and that a run which failed should tell the user what failed
and what it will do next time.

Frontmatter description:

```
description: Use this skill for any agent that runs unattended on a schedule — deciding when to retry versus give up, reporting partial results honestly, degrading when a service is down, and never claiming success it did not have. Triggers include "what if it fails", "handle errors", "make it reliable", "retry", "what happens when the service is down".
```

- [ ] **Step 7: Write `time-and-timezones`**

Body must cover: the workspace has a configured timezone and all user-facing times must be
in it; storing timestamps in UTC and converting only for display; the cron fields and their
meaning; that "every day at 9" means 9 in the user's timezone across DST changes; catch-up
behaviour after the server was down (use state, not "since one hour ago"); and never
computing a date by adding 86400 seconds.

Frontmatter description:

```
description: Use this skill whenever an agent works with dates, times or schedules — converting to the user's timezone, handling DST, writing a cron expression, computing "yesterday" or "next Tuesday", or catching up after downtime. Triggers include "every morning at", "schedule this for", "what time", "last week", "since yesterday", "in my timezone".
```

- [ ] **Step 8: Run the invariants**

Run: `go test ./internal/skilllibrary/ -count=1 -v`
Expected: PASS. `TestLoadBundledMatchesDisk` now counts 19.

- [ ] **Step 9: Commit**

```bash
git add internal/skilllibrary/skills
git commit -m "feat(skills): add seven agent-behaviour core skills

kb-curation, change-detection, notification-writing, api-integration,
agent-collaboration, resilient-runs and time-and-timezones teach how an agent
should behave on this platform. None of it was taught anywhere: the '[CALL:]'
protocol had no coverage at all, and the user's own attempt to build a
'pretty printer' skill was really a request for kb-curation.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 16: The three domain skills

**Files:**
- Create: `internal/skilllibrary/skills/email-triage/SKILL.md`
- Create: `internal/skilllibrary/skills/calendar-scheduling/SKILL.md`
- Create: `internal/skilllibrary/skills/image-ocr/SKILL.md`
- Create: `internal/skilllibrary/skills/image-ocr/scripts/ocr.py`

**Interfaces:**
- Consumes: the invariants from Task 12; `cli-tool-installer`'s `$HOME/.local/bin` convention.
- Produces: three core skills, one of which exercises install-and-invoke end to end.

- [ ] **Step 1: Write `email-triage`**

Frontmatter description:

```
description: Use this skill for working through a mailbox with the connected Gmail or Outlook account — summarising what arrived, sorting by importance, pulling out action items, finding a specific thread, or drafting a reply. Triggers include "check my email", "what's in my inbox", "anything important", "summarise my mail", "draft a reply to", "find the email about".
```

Body must cover: the connected account provides the tools — never hunt for an API key or
install an email SDK; searching before listing (a query is cheaper than paging an inbox);
reading the thread before replying to it; that a draft is created but NOT sent unless the
user explicitly asked to send; never sending mail during a build; and summarising by sender
and action needed rather than dumping subject lines.

- [ ] **Step 2: Write `calendar-scheduling`**

Frontmatter description:

```
description: Use this skill for calendar work with the connected Google Calendar or Outlook account — reading the day or week ahead, finding a free slot, creating or moving an event, and spotting conflicts. Triggers include "what's on my calendar", "am I free", "schedule a meeting", "book time for", "when's my next", "any conflicts".
```

Body must cover: the connected account provides the tools; always working in the workspace
timezone (cross-reference `time-and-timezones`); reading before writing so a conflict is
caught; that all-day events and timed events compare differently; confirming before
creating or moving anything on a shared calendar; and reporting a day as a short ordered
list, not raw event JSON.

- [ ] **Step 3: Write `image-ocr`**

Frontmatter description:

```
description: Use this skill to read text out of an image — a screenshot, a scanned page, a photo of a receipt or a whiteboard. Runs OCR via the tesseract command line tool, installing it first if it is missing. Triggers include "what does this screenshot say", "read this scan", "extract the text from this image", "ocr this", "what's written in the photo".
```

Body must cover: checking for `tesseract` at `$HOME/.local/bin/tesseract` then on `PATH`;
installing it via the cli-tool-installer skill when absent; that OCR quality depends on
resolution and that a blurry source produces garbage which should be reported rather than
guessed at; and using `--psm 6` for a block of text versus the default for a full page.

Frontmatter must declare the requirement:

```yaml
metadata:
  openclaw:
    requires:
      bins: [tesseract]
```

- [ ] **Step 4: Write the OCR helper**

Create `internal/skilllibrary/skills/image-ocr/scripts/ocr.py`:

```python
#!/usr/bin/env python3
"""Read text out of an image using the tesseract CLI.

Usage:
    python3 ocr.py <image> [--psm 6] [--lang eng]

Prints the recognised text to stdout. Exits 2 with an install hint if tesseract
is not available.
"""
import argparse
import os
import shutil
import subprocess
import sys


def find_tesseract():
    """Return the absolute path to tesseract, or None.

    A tool installed by the cli-tool-installer skill lives in $HOME/.local/bin, which
    is not on the sandboxed agent's PATH, so that location is checked first.
    """
    local = os.path.join(os.path.expanduser("~"), ".local", "bin", "tesseract")
    if os.path.isfile(local) and os.access(local, os.X_OK):
        return local
    return shutil.which("tesseract")


def main():
    ap = argparse.ArgumentParser(description="OCR an image with tesseract.")
    ap.add_argument("image", help="path to the image file")
    ap.add_argument("--psm", help="tesseract page segmentation mode, e.g. 6 for a text block")
    ap.add_argument("--lang", default="eng", help="language code (default: eng)")
    args = ap.parse_args()

    if not os.path.isfile(args.image):
        print(f"no such file: {args.image}", file=sys.stderr)
        return 1

    binary = find_tesseract()
    if not binary:
        print(
            "tesseract is not installed. Install it with the cli-tool-installer skill, "
            "then call it at $HOME/.local/bin/tesseract",
            file=sys.stderr,
        )
        return 2

    # stdout as the output target keeps the result in the pipe rather than on disk.
    cmd = [binary, args.image, "stdout", "-l", args.lang]
    if args.psm:
        cmd += ["--psm", args.psm]

    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        print(result.stderr.strip() or "tesseract failed", file=sys.stderr)
        return result.returncode

    text = result.stdout.strip()
    if not text:
        print("tesseract produced no text — the image may be too low-resolution, "
              "rotated, or not contain readable text", file=sys.stderr)
        return 3

    sys.stdout.write(result.stdout)
    return 0


if __name__ == "__main__":
    sys.exit(main())
```

Reference it from the SKILL.md body as `scripts/ocr.py` so the invariants test checks it.

- [ ] **Step 5: Run the invariants**

Run: `go test ./internal/skilllibrary/ -count=1 -v`
Expected: PASS. `TestLoadBundledMatchesDisk` now counts 22. `ocr.py` must pass
`ProfileSkillScript` — it uses list-form `subprocess.run` and no `shell=True`, so this is
also the first end-to-end proof that Task 1's profile split works on real shipped code.

- [ ] **Step 6: Commit**

```bash
git add internal/skilllibrary/skills
git commit -m "feat(skills): add email-triage, calendar-scheduling and image-ocr

image-ocr is deliberately the acceptance test for install-and-use: it declares
requires.bins [tesseract], installs through cli-tool-installer, and its script
invokes the binary by absolute path with list-form subprocess — which is only
possible because of the guardrail profile split.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 17: Group the catalog by category

**Files:**
- Modify: `internal/prompts/prompts.go` (`availableSkillsBlock`)
- Modify: `internal/prompts/prompts_test.go`
- Modify: `internal/agentdesigner/flow.go`, `internal/skilldesigner/flow.go` (populate `Category` on `SkillRef`)
- Modify: `internal/prompts/prompts.go` (`SkillRef`)

**Interfaces:**
- Consumes: `skilllibrary.SkillMeta.Category` (already present on the struct; `LoadBundled() []SkillMeta` returns it).
- Produces: `prompts.SkillRef.Category string`; `availableSkillsBlock` renders grouped output.

- [ ] **Step 1: Write the failing test**

Append to `internal/prompts/prompts_test.go`:

```go
func TestAvailableSkillsGroupedByCategory(t *testing.T) {
	p := prompts.ImplementationParams{
		Skills: []prompts.SkillRef{
			{Name: "pdf", Description: "Read PDFs.", Category: "File Processing"},
			{Name: "kb-curation", Description: "Write notes.", Category: "Agent Behaviour"},
			{Name: "csv", Description: "Read CSVs.", Category: "File Processing"},
		},
	}
	out := prompts.BuildImplementationPrompt("x", nil, p)

	require.Contains(t, out, "Agent Behaviour")
	require.Contains(t, out, "File Processing")

	// Both File Processing skills sit under one heading, not two.
	require.Equal(t, 1, strings.Count(out, "File Processing"))
}

// A skill with no category still appears — it must never be silently dropped.
func TestAvailableSkillsUncategorised(t *testing.T) {
	p := prompts.ImplementationParams{
		Skills: []prompts.SkillRef{{Name: "loose", Description: "No category set."}},
	}
	out := prompts.BuildImplementationPrompt("x", nil, p)
	require.Contains(t, out, "loose")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/prompts/ -run AvailableSkills -count=1`
Expected: FAIL — `unknown field Category`.

- [ ] **Step 3: Add the field and group**

In `internal/prompts/prompts.go`, add to `SkillRef` (line 31):

```go
	Category string // e.g. "File Processing"; empty renders under "Other"
```

Replace the loop in `availableSkillsBlock`:

```go
	// Grouped by category so the model scans a structured list rather than a flat wall.
	// With 22 core skills the descriptions alone run to ~900 words; they stay at full
	// length because the trigger phrases ARE the matching signal, and truncating them
	// would undercut the selector that depends on them.
	byCat := map[string][]SkillRef{}
	var order []string
	for _, sk := range skills {
		cat := sk.Category
		if cat == "" {
			cat = "Other"
		}
		if _, seen := byCat[cat]; !seen {
			order = append(order, cat)
		}
		byCat[cat] = append(byCat[cat], sk)
	}
	sort.Strings(order)
	for _, cat := range order {
		sb.WriteString("\n")
		sb.WriteString(cat)
		sb.WriteString(":\n")
		for _, sk := range byCat[cat] {
			sb.WriteString("- **")
			sb.WriteString(sk.Name)
			sb.WriteString("** — ")
			sb.WriteString(sk.Description)
			sb.WriteString("\n")
		}
	}
	sb.WriteString("</available_skills>\n\n")
	return sb.String()
```

Add `"sort"` to the imports if absent.

- [ ] **Step 4: Populate `Category` where skill refs are built**

Run `grep -rn 'prompts.SkillRef{' internal/` and set `Category` from the source:

- Core skills: `skilllibrary.LoadBundled()` returns metadata carrying `Category` — pass it through in `agentdesigner.Flow.loadSkillNames` and `skilldesigner.Flow.loadSkillNames` (line 886).
- User skills: `db.Skill` has no category column. Use `"User skills"` as the literal.

Example, in `internal/skilldesigner/flow.go:886`:

```go
	for _, s := range skilllibrary.LoadBundled() {
		refs = append(refs, prompts.SkillRef{Name: s.Name, Description: s.Description, Category: s.Category})
	}
	...
	for _, s := range skills {
		refs = append(refs, prompts.SkillRef{Name: s.Name, Description: s.Description, Category: "User skills"})
	}
```

`skilllibrary.SkillMeta` already carries `Category` (`library.go:52`), so no change is needed there.

- [ ] **Step 5: Run the tests**

Run: `go build ./... && go test ./... -count=1 -timeout 120s`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/prompts internal/agentdesigner internal/skilldesigner
git commit -m "feat(prompts): group the skill catalog by category

22 core skills at ~40 words of trigger phrases each is ~900 words in every
design turn, implementation prompt and selector call. The descriptions stay at
full length — they are the matching signal the selector depends on — so the
block is grouped by category instead, giving the model a structured list rather
than a flat wall.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 18: Phase 3 end-to-end verification

**Files:**
- No source changes expected.

**Interfaces:**
- Consumes: Tasks 12-17.
- Produces: the install-and-use acceptance proof.

- [ ] **Step 1: Deploy**

```bash
make deploy && make status
```

- [ ] **Step 2: Confirm the catalog is live**

Open the SPA skills page. Expected: 22 core skills, grouped, with no `web-search`,
`web-scraper` or `github-integration`.

- [ ] **Step 3: Create the OCR agent**

Create an agent named `screenshot-reader` described as:

> When I put an image in my knowledge base, read the text out of it and save that text as a note next to it.

Approve through to save.

- [ ] **Step 4: Verify skills were auto-attached**

```bash
sqlite3 ~/.simple-agents-v2/simple-agents.db \
  "SELECT a.name, s.skill_name FROM agent_skills s JOIN agents a ON a.id = s.agent_id WHERE a.name = 'screenshot-reader';"
```
Expected: `image-ocr`, plausibly with `kb-curation`. No manual assignment.

- [ ] **Step 5: Run it against a real image**

Put a PNG containing legible text into the workspace vault's `notes/` directory, then run
the agent from the SPA.

```bash
ls ~/.local/bin/tesseract || echo "not yet installed — the agent should install it"
```

Expected: the run installs `tesseract` if missing, reads the text, and writes a note. Check
the run log in the SPA for the install step and the OCR output.

- [ ] **Step 6: Record the outcome against the spec's acceptance list**

Confirm all five acceptance points in §8 of the design doc. Note any that did not hold and
why.

- [ ] **Step 7: Update the memory index**

```bash
cat >> ~/.claude/projects/-home-rookie-simple-agents-v2/memory/MEMORY.md <<'EOF'
- [Skill system overhaul (SP10)](project_skill_system_overhaul.md) — buildSpec fixes the AGENT.md misfire, guardrail profiles allow subprocess in skills, selector call attaches skills automatically, 22-skill catalog
EOF
```

Write the memory file itself with the frontmatter shape the memory system expects, recording
what was non-obvious: that the API build engine's finish gate was agent-shaped and silently
broke every skill build, and that the `# Skills:` header was requested only in the prompt
that writes nothing to disk.

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "chore: SP10 verification results

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Self-Review Notes

**Spec coverage:**

| Spec section | Task |
|---|---|
| §4.1 build-finish guard | 2, 3 |
| §4.2 guardrail profiles | 1 |
| §4.3 script file handling | 5 |
| §4.3a locating SKILL.md | 4 |
| §4.4 skill naming | 6 |
| §4.5 Phase 1 testing | 1, 2, 4, 5, 6 |
| §5.1 tier 1 header | 8 |
| §5.2 tier 2 selector + three contracts | 9, 10 |
| §5.3 Phase 2 testing | 9, 10 |
| §6.1 pdf/docx drift | 12 |
| §6.2 meta skills | 13 |
| §6.3 refocus three | 14 |
| §6.4 ten new skills | 15, 16 |
| §6.5 catalog size / grouping | 17 |
| §6.6 catalog invariants | 12 |
| §8 acceptance 1-3 | 7 |
| §8 acceptance 4 | 11 |
| §8 acceptance 5 | 18 |

**Naming consistency check:** `BuildSpec` / `AgentBuildSpec` / `SkillBuildSpec` / `WithBuildSpec` / `hostToolSet.spec` / `buildSpec()` are used consistently from Task 2 onward. `GuardrailProfile` / `ProfileAgentTool` / `ProfileSkillScript` from Task 1. `LocateSkillRoot` / `ReadSkillTree` / `SlugifySkillName` / `ErrNoSkillMD` from Tasks 4-6. `IsTestArtifact` (exported in Task 5) is used by Task 5 only. `availableSkillsBlock` (Task 8) is extended in Task 17. `SelectSkills` / `parseSelectorResponse` (Task 9) are consumed by `resolveAgentSkills` (Task 10). `SkillRef.Category` (Task 17) is the only late field addition and its consumers are updated in the same task.
