package agentdesigner_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ilijad1/simple-agents/internal/agentdesigner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestToolsTreeRoundTrip proves a real multi-file project — nested modules, a
// tests/ folder, and a non-.py requirements.txt — survives a write→read round trip
// with its relative paths intact. This is the core of the "agents can be whole
// projects, not single scripts" change.
func TestToolsTreeRoundTrip(t *testing.T) {
	dir := t.TempDir()
	toolsDir := filepath.Join(dir, "tools")

	in := map[string]string{
		"fetch.py":             "print('fetch')\n",
		"lib/parser.py":        "def parse(x):\n    return x\n",
		"tests/test_parser.py": "from lib.parser import parse\n\ndef test_parse():\n    assert parse(1) == 1\n",
		"requirements.txt":     "requests\n",
	}

	require.NoError(t, agentdesigner.WriteToolsTree(toolsDir, in))

	// Files landed at their nested paths on disk.
	assert.FileExists(t, filepath.Join(toolsDir, "lib", "parser.py"))
	assert.FileExists(t, filepath.Join(toolsDir, "tests", "test_parser.py"))
	assert.FileExists(t, filepath.Join(toolsDir, "requirements.txt"))

	out, err := agentdesigner.ReadToolsTree(toolsDir)
	require.NoError(t, err)
	assert.Equal(t, in, out)
}

// TestToolsTreeReadSkipsCacheAndMissing confirms generated cruft is excluded and a
// missing tools/ dir yields an empty map (not an error).
func TestToolsTreeReadSkipsCacheAndMissing(t *testing.T) {
	dir := t.TempDir()
	toolsDir := filepath.Join(dir, "tools")

	require.NoError(t, agentdesigner.WriteToolsTree(toolsDir, map[string]string{"a.py": "x=1\n"}))
	// Simulate Python writing a cache dir + compiled file during a test run.
	require.NoError(t, os.MkdirAll(filepath.Join(toolsDir, "__pycache__"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(toolsDir, "__pycache__", "a.cpython-312.pyc"), []byte("binary"), 0o640))
	require.NoError(t, os.WriteFile(filepath.Join(toolsDir, "a.pyc"), []byte("binary"), 0o640))

	out, err := agentdesigner.ReadToolsTree(toolsDir)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"a.py": "x=1\n"}, out)

	// Missing dir → empty map, no error.
	empty, err := agentdesigner.ReadToolsTree(filepath.Join(dir, "does-not-exist"))
	require.NoError(t, err)
	assert.Empty(t, empty)
}

// TestWriteToolsTreeRejectsEscapes is the path-safety guarantee: a key that tries
// to escape tools/ via ".." traversal must be refused. (A leading "/" is clamped to
// within tools/, mirroring vault.Resolve — that is safe, not an escape.)
func TestWriteToolsTreeRejectsEscapes(t *testing.T) {
	toolsDir := filepath.Join(t.TempDir(), "tools")
	for _, bad := range []string{"../evil.py", "../../etc/passwd", "a/../../b.py"} {
		err := agentdesigner.WriteToolsTree(toolsDir, map[string]string{bad: "x=1\n"})
		assert.Error(t, err, "expected %q to be rejected", bad)
	}

	// A leading slash is clamped inside tools/, not rejected.
	require.NoError(t, agentdesigner.WriteToolsTree(toolsDir, map[string]string{"/etc/passwd": "x=1\n"}))
	assert.FileExists(t, filepath.Join(toolsDir, "etc", "passwd"))
}

// TestRunToolGuardrailsByExtension confirms the AST check only applies to .py: a
// non-Python file passes (ethics-only) while a dangerous .py still fails.
func TestRunToolGuardrailsByExtension(t *testing.T) {
	// requirements.txt is not valid Python — must NOT be AST-parsed.
	require.NoError(t, agentdesigner.RunToolGuardrails("requirements.txt", "requests==2.31.0\n"))

	// A .py using subprocess must still be blocked when python3 is available.
	if agentdesigner.PythonAvailable() {
		err := agentdesigner.RunToolGuardrails("bad.py", "import subprocess\nsubprocess.run(['ls'])\n")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ast check")
	}

	// Ethics applies to every file regardless of extension.
	err := agentdesigner.RunToolGuardrails("notes.txt", "please rm -rf / now")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ethics filter")
}
