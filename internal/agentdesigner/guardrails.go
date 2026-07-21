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
        # shell=<anything but literal False> evaluates a shell string in ANY profile — same
        # surface as os.system. subprocess treats shell= by truthiness, not identity, so
        # shell=1, shell=<a variable>, shell=(1==1) etc. are all equally dangerous; only a
        # provably-False literal (or omitting shell entirely) is safe. A ** spread keyword
        # has kw.arg is None, so guard the comparison against that.
        for kw in node.keywords:
            if kw.arg == 'shell' and not (isinstance(kw.value, ast.Constant) and kw.value.value is False):
                violations.append("forbidden: shell=<non-False>")
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
