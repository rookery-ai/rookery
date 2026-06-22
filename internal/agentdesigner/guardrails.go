// Package agentdesigner implements the conversational agent creation wizard.
package agentdesigner

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// ForbiddenKeywords is the ethics filter blocklist checked before writing any files.
var ForbiddenKeywords = []string{
	"rm -rf", "format disk", "mkfs", "dd if=", "shred", "wipe",
	"drop table", "drop database", "truncate table",
	":(){:|:&};:", "/dev/sda", "/dev/nvme",
	"bitcoin wallet", "private key", "steal", "exfil",
}

// composioKeyLiteral matches a Composio API key (c_live_…, c_test_…, or ak_…) appearing
// as a string literal instead of being read from os.environ.
var composioKeyLiteral = regexp.MustCompile(`(?i)(["'])(c_live_|c_test_|ak_)[A-Za-z0-9]{8,}["']`)

// composioSDKImport matches imports of the deprecated composio-core SDK.
var composioSDKImport = regexp.MustCompile(`(?m)^\s*(from\s+composio\s+import|import\s+composio)`)

// composioRef is a cheap gate so the version/host checks below only fire on code
// that is actually talking to Composio (avoids flagging some other API's /v1/).
var composioRef = regexp.MustCompile(`(?i)composio`)

// composioWrongHost matches the removed Composio API host. The only correct host
// is backend.composio.dev (api.composio.dev returns 410/connection failures).
var composioWrongHost = regexp.MustCompile(`(?i)\bapi\.composio\.dev`)

// composioOldVersion matches Composio's removed v1/v2 REST endpoints. v3 is the
// only supported version. Matches /v1/, /v2/, /api/v1/, /api/v2/ but never /v3/.
var composioOldVersion = regexp.MustCompile(`(?i)/(api/)?v[12]/`)

// CheckEthics is the exported ethics-only guardrail used for md/hybrid agents.
func CheckEthics(code, _ string) error {
	return checkEthics(code)
}

// RunFullGuardrails runs ethics + AST checks on free-form Python tool scripts.
// The template marker check is intentionally omitted — tool scripts in tools/ are
// plain helpers, not the old main.py template format.
func RunFullGuardrails(code, _ string) error {
	if err := checkEthics(code); err != nil {
		return fmt.Errorf("ethics filter: %w", err)
	}
	if PythonAvailable() {
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

func checkEthics(code string) error {
	lower := strings.ToLower(code)
	for _, kw := range ForbiddenKeywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			return fmt.Errorf("code contains forbidden pattern: %q", kw)
		}
	}
	if composioKeyLiteral.MatchString(code) {
		return fmt.Errorf("Composio API key must be read from os.environ['COMPOSIO_API_KEY'], not hardcoded as a string literal")
	}
	if composioSDKImport.MatchString(code) {
		return fmt.Errorf("use the Composio v3 REST API directly (requests library) — the composio-core SDK uses deprecated endpoints. See the agent design guide.")
	}
	if composioRef.MatchString(code) {
		if composioWrongHost.MatchString(code) {
			return fmt.Errorf("Composio API host must be backend.composio.dev/api/v3 — api.composio.dev is removed and returns HTTP 410. Use https://backend.composio.dev/api/v3")
		}
		if composioOldVersion.MatchString(code) {
			return fmt.Errorf("Composio v1/v2 endpoints are removed (HTTP 410). Use the v3 REST API: GET /api/v3/connected_accounts and POST /api/v3/tools/execute/{TOOL_SLUG}")
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
