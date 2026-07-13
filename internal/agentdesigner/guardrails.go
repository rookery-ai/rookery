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

// RunFullGuardrails runs ethics + AST checks on free-form Python tool scripts (CODE), so it
// applies the FULL keyword set (intent + destructive commands). The template marker check is
// intentionally omitted — tool scripts in tools/ are plain helpers, not the old main.py
// template format.
func RunFullGuardrails(code, _ string) error {
	if err := checkEthicsCode(code); err != nil {
		return fmt.Errorf("ethics filter: %w", err)
	}
	if PythonAvailable() {
		if err := checkAST(code); err != nil {
			return fmt.Errorf("ast check: %w", err)
		}
	}
	return nil
}

// RunToolGuardrails runs guardrails on a single agent project FILE (tools/*.py,
// requirements.txt, …) — all code/config, so it applies the full code-context ethics check
// (intent + destructive commands) to EVERY file, and the Python AST check
// only on .py files (parsing a non-Python file as Python would spuriously fail). This is the
// guardrail used for multi-file agent projects.
func RunToolGuardrails(filename, code string) error {
	if err := checkEthicsCode(code); err != nil {
		return fmt.Errorf("ethics filter: %w", err)
	}
	if strings.HasSuffix(filename, ".py") && PythonAvailable() {
		if err := checkAST(code); err != nil {
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

// astCheckScript is inlined Python that checks for forbidden AST nodes.
const astCheckScript = `
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
        if isinstance(node.func, ast.Attribute):
            attr = node.func.attr
            if attr in FORBIDDEN_OS_ATTRS:
                violations.append(f"forbidden: os.{attr}()")
            if isinstance(node.func.value, ast.Name):
                val = node.func.value.id
                if val == 'subprocess':
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

func checkAST(code string) error {
	cmd := exec.Command("python3", "-c", astCheckScript)
	cmd.Stdin = strings.NewReader(code)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(out.String()))
	}
	return nil
}
