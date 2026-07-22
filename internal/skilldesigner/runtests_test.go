package skilldesigner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRunTests_SkipsNonCheckableFilesWithoutReportingFailure is Finding 3's
// pinning test: runTests must never report a file it cannot statically check
// (references/*.md, and anything else that isn't .py/.sh) as a failure — a
// references/*.md appearing as a ❌ line would be a lie to the user. It must
// produce pass lines for the checkable files and no line at all mentioning the
// .md file.
func TestRunTests_SkipsNonCheckableFilesWithoutReportingFailure(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}

	dir := t.TempDir()
	scripts := map[string]string{
		"scripts/ok.py":       "print('hi')\n",
		"scripts/ok.sh":       "#!/bin/bash\necho hi\n",
		"references/guide.md": "# Guide\nSome prose, not code.\n",
	}
	for rel, content := range scripts {
		full := filepath.Join(dir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o640))
	}

	f := &Flow{}
	out := f.runTests(dir, scripts, "")

	require.Contains(t, out, "scripts/ok.py")
	require.Contains(t, out, "scripts/ok.sh")
	require.Contains(t, out, "✅", "checkable files should report a pass line")

	for _, line := range strings.Split(out, "\n") {
		require.NotContains(t, line, "guide.md", "a references/*.md must never appear as a reported line — checkable or not")
	}
}
