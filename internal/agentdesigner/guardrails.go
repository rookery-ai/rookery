// Package agentdesigner implements the conversational agent creation wizard.
package agentdesigner

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// alwaysForbiddenKeywords are never-legitimate intent markers — blocked in EVERY file,
// including plain-English documents (AGENT.md, SKILL.md). These describe malicious intent,
// not a mechanism, so they have no benign use in a document's prose either.
var alwaysForbiddenKeywords = []string{
	"bitcoin wallet", "steal", "exfil",
}

// destructiveCodeKeywords are dangerous only as EXECUTABLE code (a shell command or SQL a
// script actually runs). They are legitimate as descriptive prose in a document — an
// AGENT.md that says "drop the temp table at the end of each run" or "wipe stale entries"
// is describing behaviour, not executing it — so these are checked in code files ONLY, not
// in markdown documents. (Before this split they substring-matched AGENT.md prose and
// hard-blocked legitimate agents.)
var destructiveCodeKeywords = []string{
	"rm -rf", "format disk", "mkfs", "dd if=", "shred", "wipe",
	"drop table", "drop database", "truncate table",
	":(){:|:&};:", "/dev/sda", "/dev/nvme",
}

// ForbiddenKeywords is the union of both lists, retained for reference/back-compat.
var ForbiddenKeywords = append(append([]string{}, alwaysForbiddenKeywords...), destructiveCodeKeywords...)

// CheckEthics is the exported ethics-only guardrail used for MARKDOWN documents (AGENT.md,
// SKILL.md). It applies the always-forbidden intent keywords, but NOT
// the destructive-command keywords, which are legitimate as descriptive prose in a document
// (see destructiveCodeKeywords). Callers all pass markdown here.
func CheckEthics(code, _ string) error {
	return checkEthicsDoc(code)
}

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

// PythonAvailable returns true if python3 is in PATH.
func PythonAvailable() bool {
	_, err := exec.LookPath("python3")
	return err == nil
}

// ─── Internal checks ──────────────────────────────────────────────────────────

// checkEthicsDoc is the ethics gate for markdown documents: always-forbidden intent
// keywords only (no destructive-command keywords — those are legitimate prose in a doc).
func checkEthicsDoc(code string) error {
	return scanForbiddenKeywords(code, alwaysForbiddenKeywords)
}

// checkEthicsCode is the ethics gate for code files: the full keyword set (intent +
// destructive commands).
func checkEthicsCode(code string) error {
	if err := scanForbiddenKeywords(code, alwaysForbiddenKeywords); err != nil {
		return err
	}
	return scanForbiddenKeywords(code, destructiveCodeKeywords)
}

// scanForbiddenKeywords reports the first keyword in list that appears (case-insensitively)
// in code.
func scanForbiddenKeywords(code string, list []string) error {
	lower := strings.ToLower(code)
	for _, kw := range list {
		if strings.Contains(lower, strings.ToLower(kw)) {
			return fmt.Errorf("code contains forbidden pattern: %q", kw)
		}
	}
	return nil
}

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

// astCheckBody is the AST checker used by RunFullGuardrails/RunToolGuardrails.
//
// What this is: a best-effort, deterministic filter against the obvious shapes an LLM
// produces when it writes eval/exec/os.system/subprocess-with-a-shell-string code. It is
// cheap, fast, and catches the overwhelming majority of what a coder actually generates.
//
// What this is NOT: a security boundary. It is a static pattern match over one parse of the
// source, not a data-flow or taint analysis, and it can be defeated by anything that hides
// the call shape from a simple AST walk. The known, NOT-fixed-here bypass: an aliased import
// (`import subprocess as sp; sp.run(..., shell=True)`, `import os as o; o.system(...)`)
// slips past every rule in this file, in BOTH profiles — the checker only recognizes the
// literal names `subprocess`/`os`/`socket`. This is pre-existing, out of scope for this
// change, and logged for triage; do not assume this checker stops it.
//
// The actual enforcement boundaries are (a) the Landlock sandbox every coder subprocess runs
// under (internal/sandbox — confines the filesystem regardless of what the script does), and
// (b) for skills specifically, the skill-vetter LLM audit (internal/skilldesigner) that reads
// the whole script for intent, not just syntax shapes. Treat this AST check as a cheap first
// filter that raises the bar for a lazy/naive generation, not as the thing standing between a
// malicious script and the host.
//
// ALLOW_SUBPROCESS is prepended by astCheckScript.
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

# A ** spread keyword (subprocess.run(['ls'], **something)) is an ast.keyword node whose
# .arg is None — it doesn't name 'shell' directly, so a simple per-call walk can't see into
# it. Rule chosen: resolve what's being spread wherever that's statically possible, and only
# fall back to "block it" when it genuinely can't be resolved.
#   1. **{'shell': True, ...}   — a dict literal right at the call site: inspect its keys
#      directly for a truthy 'shell' entry, same truthiness rule as a named keyword.
#   2. **some_var, where some_var = {...} is the ONLY assignment to that name anywhere in
#      the module, and that one assignment is a dict literal — resolved via one pre-pass
#      (DICT_LITERALS below) and inspected exactly like case 1. This is what lets a benign
#      helper like opts = {'capture_output': True}; subprocess.run(['ls'], **opts) pass under
#      the skill profile while kw = {'shell': True}; subprocess.run(['ls'], **kw) still blocks.
#      Deliberately narrow: a name is resolved ONLY if it is bound EXACTLY ONCE module-wide
#      (ASSIGN_COUNTS below) — never the nearest-preceding or last-wins binding, and never a
#      guess across scopes. A name assigned more than once (e.g. a trailing reassignment
#      after the spread, or two different bindings in two branches) is deliberately treated
#      as unresolvable and falls to case 3, because "pick one of several bindings" is exactly
#      the kind of guess that lets a spread's dangerous value hide behind an innocuous-looking
#      later assignment. This also means a name that collides with a function parameter of
#      the same name, or a dict literal assigned inside a different function, is NOT
#      resolved — the collector counts every textual name = {...} assignment in the module
#      regardless of scope, so a same-named single binding elsewhere would (correctly, if
#      conservatively) still count as more-than-once against a same-named parameter, since
#      real scope resolution is out of reach for a checker this simple.
#   3. Anything else spread into a subprocess.*/os.* call (a function parameter, a function
#      call's return value, a name bound zero or more-than-once, a dict built via .update(),
#      a merge of two dicts, ...) is NOT resolvable by this pre-pass. The checker cannot prove
#      shell is absent, so it is treated as a violation rather than a silent pass. This does
#      mean an unusual-but-legitimate pattern (e.g. spreading **kwargs forwarded from an
#      outer function, or a dict reassigned more than once) is over-blocked under the skill
#      profile — accepted, because the alternative is a hole any bypass can be laundered
#      through by one extra reassignment or one more level of indirection than case 2 covers.
class DictLiteralCollector(ast.NodeVisitor):
    def __init__(self):
        self.bindings = {}
        self.counts = {}
    def visit_Assign(self, node):
        if len(node.targets) == 1 and isinstance(node.targets[0], ast.Name):
            name = node.targets[0].id
            self.counts[name] = self.counts.get(name, 0) + 1
            if isinstance(node.value, ast.Dict):
                self.bindings[name] = node.value
        self.generic_visit(node)

_collector = DictLiteralCollector()
_collector.visit(tree)
# Only resolve names bound EXACTLY ONCE, module-wide, to a dict literal — see case 2 above.
DICT_LITERALS = {
    name: node for name, node in _collector.bindings.items()
    if _collector.counts.get(name, 0) == 1
}

def _dict_has_truthy_shell(dict_node):
    for k, v in zip(dict_node.keys, dict_node.values):
        if isinstance(k, ast.Constant) and k.value == 'shell':
            return not (isinstance(v, ast.Constant) and v.value is False)
    return False

class Checker(ast.NodeVisitor):
    def visit_Call(self, node):
        if isinstance(node.func, ast.Name) and node.func.id in FORBIDDEN_NAMES:
            violations.append(f"forbidden call: {node.func.id}()")

        is_subprocess_or_os_call = (
            isinstance(node.func, ast.Attribute)
            and isinstance(node.func.value, ast.Name)
            and node.func.value.id in ('subprocess', 'os')
        )

        # shell=<anything but literal False> evaluates a shell string in ANY profile — same
        # surface as os.system. subprocess treats shell= by truthiness, not identity, so
        # shell=1, shell=<a variable>, shell=(1==1) etc. are all equally dangerous; only a
        # provably-False literal (or omitting shell entirely) is safe.
        for kw in node.keywords:
            if kw.arg == 'shell':
                if not (isinstance(kw.value, ast.Constant) and kw.value.value is False):
                    violations.append("forbidden: shell=<non-False>")
            elif kw.arg is None:
                resolved = None
                if isinstance(kw.value, ast.Dict):
                    resolved = kw.value
                elif isinstance(kw.value, ast.Name) and kw.value.id in DICT_LITERALS:
                    resolved = DICT_LITERALS[kw.value.id]

                if resolved is not None:
                    if _dict_has_truthy_shell(resolved):
                        violations.append("forbidden: shell=<non-False> via ** dict spread")
                elif is_subprocess_or_os_call:
                    violations.append("forbidden: ** spread of unresolvable kwargs into subprocess/os call (cannot verify shell= is absent)")

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
