package coder

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/buildphase"
	"github.com/rookery-ai/rookery/internal/llm"
	"github.com/rookery-ai/rookery/internal/skilldeps"
)

func offeredTools(h *hostToolSet) map[string]bool {
	out := map[string]bool{}
	for _, t := range h.tools() {
		out[t.Name] = true
	}
	return out
}

func TestDepsToolsOfferedOnlyWhereCodeRuns(t *testing.T) {
	run := offeredTools(&hostToolSet{includeExecTools: true})
	if !run["install_packages"] || !run["run_in_env"] {
		t.Error("agent runs and builds must be offered install_packages and run_in_env")
	}
	chat := offeredTools(&hostToolSet{includeExecTools: false})
	if chat["install_packages"] || chat["run_in_env"] {
		t.Error("chat has no exec tools and must not get these")
	}
}

func TestInstallPackagesRefusedDuringASkillBuild(t *testing.T) {
	h := &hostToolSet{includeExecTools: true, subprocessEnv: map[string]string{buildphase.KindEnvVar: buildphase.KindSkill}}
	out := h.execute(context.Background(), llm.ToolCall{Name: "install_packages", Args: []byte(`{"env":"x","packages":["requests"]}`)})
	if !strings.HasPrefix(out, "error:") || !strings.Contains(out, skilldeps.ErrSkillBuild.Error()) {
		t.Fatalf("got %q, want the skill-build refusal", out)
	}
}

// fakeVenvPython: `-m venv DIR` copies itself to DIR/bin/python; anything else runs the
// real interpreter. Enough to exercise run_in_env without network or a real venv.
func fakeVenvPython(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	p := filepath.Join(t.TempDir(), "python3")
	src := "#!/usr/bin/env python3\nimport os, sys, shutil\na = sys.argv[1:]\nif a[:2] == ['-m', 'venv']:\n    os.makedirs(os.path.join(a[2], 'bin'), exist_ok=True)\n    d = os.path.join(a[2], 'bin', 'python')\n    shutil.copy(os.path.realpath(__file__), d)\n    os.chmod(d, 0o755)\n    sys.exit(0)\nos.execv(sys.executable, [sys.executable] + a)\n"
	if err := os.WriteFile(p, []byte(src), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunInEnvPassesOnlyNamedSecrets(t *testing.T) {
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "s.py"), []byte("import os\nprint(sorted(k for k in os.environ if k.endswith('_SECRET')))\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &hostToolSet{
		includeExecTools: true, workDir: work, homesDir: t.TempDir(), workspaceID: "ws",
		subprocessEnv: map[string]string{"A_SECRET": "1", "B_SECRET": "2"},
		deps:          skilldeps.Runner{Python: fakeVenvPython(t)},
	}
	out := h.execute(context.Background(), llm.ToolCall{Name: "run_in_env", Args: []byte(`{"env":"x","script":"s.py","secrets":["A_SECRET"]}`)})
	if strings.TrimSpace(out) != "['A_SECRET']" {
		t.Fatalf("script saw %q, want only A_SECRET", out)
	}
}

func TestRunInEnvUnknownSecretNamesItWithoutValues(t *testing.T) {
	h := &hostToolSet{includeExecTools: true, workDir: t.TempDir(), homesDir: t.TempDir(), workspaceID: "ws",
		subprocessEnv: map[string]string{"A_SECRET": "s3cr3t-value"}}
	out := h.execute(context.Background(), llm.ToolCall{Name: "run_in_env", Args: []byte(`{"env":"x","script":"s.py","secrets":["NOPE_SECRET","A_SECRET"]}`)})
	if !strings.HasPrefix(out, "error:") || !strings.Contains(out, "NOPE_SECRET") {
		t.Fatalf("got %q, want an error naming NOPE_SECRET", out)
	}
	if strings.Contains(out, "s3cr3t-value") {
		t.Fatal("error leaked a secret value")
	}
}

func TestBashNoLongerForbidsInstallsOutright(t *testing.T) {
	for _, tool := range (&hostToolSet{includeExecTools: true}).tools() {
		if tool.Name == "bash" && strings.Contains(tool.Description, "Do NOT install packages") {
			t.Fatal("bash still forbids installs instead of pointing at install_packages")
		}
	}
}
