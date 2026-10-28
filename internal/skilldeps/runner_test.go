package skilldeps

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakePython writes an interpreter stand-in: `-m venv DIR` copies itself to
// DIR/bin/python; `-m pip install --dry-run … --report FILE` writes report as FILE;
// `-m pip install …` touches <prefix>/installed; anything else runs the real python3.
// The report is embedded in the script because install environments are scrubbed and
// cannot carry it in.
func fakePython(t *testing.T, report string) string {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	src := `#!/usr/bin/env python3
import os, sys, shutil
args = sys.argv[1:]
REPORT = ` + "'''" + report + "'''" + `
if args[:2] == ["-m", "venv"]:
    d = args[2]
    os.makedirs(os.path.join(d, "bin"), exist_ok=True)
    dst = os.path.join(d, "bin", "python")
    shutil.copy(os.path.realpath(__file__), dst)
    os.chmod(dst, 0o755)
    sys.exit(0)
if args[:3] == ["-m", "pip", "install"]:
    prefix = os.path.dirname(os.path.dirname(os.path.realpath(__file__)))
    if "--dry-run" in args:
        with open(args[args.index("--report") + 1], "w") as f:
            f.write(REPORT)
    else:
        open(os.path.join(prefix, "installed"), "w").close()
    print("pip ok")
    sys.exit(0)
os.execv(sys.executable, [sys.executable] + args)
`
	p := filepath.Join(t.TempDir(), "python3")
	if err := os.WriteFile(p, []byte(src), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

const evilReport = `{"install":[{"metadata":{"name":"evilpkg","version":"1.0"}},{"metadata":{"name":"requests","version":"2.32.4"}}]}`

type fakeChecker struct {
	findings []Finding
	err      error
}

func (f fakeChecker) Malicious(context.Context, []Package) ([]Finding, error) {
	return f.findings, f.err
}

func TestInstallRejectsInvalidSpecBeforeRunningAnything(t *testing.T) {
	home := t.TempDir()
	r := Runner{Python: "/nonexistent/python3", Checker: fakeChecker{}}
	if _, err := r.Install(context.Background(), InstallRequest{Home: home, Env: "x", Specs: []string{"requests --extra-index-url http://x"}}); err == nil {
		t.Fatal("invalid spec accepted")
	}
	if _, err := os.Stat(filepath.Join(home, EnvsRel, "x")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("env dir created for a refused install: %v", err)
	}
}

func TestInstallRefusesMaliciousPackage(t *testing.T) {
	home := t.TempDir()
	r := Runner{Python: fakePython(t, evilReport), Checker: fakeChecker{findings: []Finding{{Package: Package{"evilpkg", "1.0"}, ID: "MAL-2024-1"}}}}
	_, err := r.Install(context.Background(), InstallRequest{Home: home, Env: "x", Specs: []string{"requests"}})
	if err == nil || !strings.Contains(err.Error(), "evilpkg") || !strings.Contains(err.Error(), "MAL-2024-1") {
		t.Fatalf("err = %v, want a refusal naming evilpkg and its advisory", err)
	}
	env := filepath.Join(home, EnvsRel, "x")
	if _, err := os.Stat(filepath.Join(env, "installed")); !errors.Is(err, os.ErrNotExist) {
		t.Error("the real install ran after a malicious finding")
	}
	if m, _ := ReadManifest(env); len(m.Installs) != 0 {
		t.Errorf("a refused install was recorded: %+v", m)
	}
}

func TestInstallProceedsWhenCheckerUnreachable(t *testing.T) {
	home := t.TempDir()
	r := Runner{Python: fakePython(t, evilReport), Checker: fakeChecker{err: errors.New("osv.dev answered 503")}}
	res, err := r.Install(context.Background(), InstallRequest{Home: home, Env: "x", Agent: "a", Specs: []string{"requests"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.MalwareCheck, "skipped:") {
		t.Errorf("MalwareCheck = %q, want skipped:…", res.MalwareCheck)
	}
	env := filepath.Join(home, EnvsRel, "x")
	if _, err := os.Stat(filepath.Join(env, "installed")); err != nil {
		t.Error("the install did not run")
	}
	m, _ := ReadManifest(env)
	if len(m.Installs) != 1 || !m.Satisfies([]string{"requests"}) || len(m.Installs[0].Resolved) != 2 {
		t.Errorf("manifest = %+v", m)
	}
}

// The install keeps its own deadline but must still stop when the caller cancels: a
// cancelled build has to stay cancelled.
func TestInstallStopsWhenTheCallerCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := Runner{Python: fakePython(t, evilReport), Checker: fakeChecker{}}
	if _, err := r.Install(ctx, InstallRequest{Home: t.TempDir(), Env: "x", Specs: []string{"requests"}}); err == nil {
		t.Fatal("an install whose caller already cancelled ran to completion")
	}
}

// Up to three scheduled agents run at once, and two using one skill install into one
// environment. Each install must check the set IT resolved — never another install's —
// and every install must be recorded.
func TestConcurrentInstallsCheckTheirOwnResolution(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	// Dry run: report a single package named after the requested spec, then linger so
	// another install's report write lands before this one is read.
	src := `#!/usr/bin/env python3
import os, sys, shutil, time, json
a = sys.argv[1:]
if a[:2] == ["-m", "venv"]:
    os.makedirs(os.path.join(a[2], "bin"), exist_ok=True)
    d = os.path.join(a[2], "bin", "python")
    shutil.copy(os.path.realpath(__file__), d)
    os.chmod(d, 0o755)
    sys.exit(0)
if a[:3] == ["-m", "pip", "install"]:
    if "--dry-run" in a:
        with open(a[a.index("--report") + 1], "w") as f:
            json.dump({"install": [{"metadata": {"name": a[-1], "version": "1"}}]}, f)
        time.sleep(0.3)
    sys.exit(0)
`
	py := filepath.Join(t.TempDir(), "python3")
	if err := os.WriteFile(py, []byte(src), 0o755); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	r := Runner{Python: py, Checker: fakeChecker{}}
	specs := []string{"alpha", "bravo", "charlie", "delta"}
	errs := make(chan error, len(specs))
	for _, s := range specs {
		go func() {
			res, err := r.Install(context.Background(), InstallRequest{Home: home, Env: "shared", Specs: []string{s}})
			if err == nil && (len(res.Resolved) != 1 || res.Resolved[0].Name != s) {
				err = fmt.Errorf("install of %s checked %+v", s, res.Resolved)
			}
			errs <- err
		}()
	}
	for range specs {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
	m, _ := ReadManifest(filepath.Join(home, EnvsRel, "shared"))
	if len(m.Installs) != len(specs) || !m.Satisfies(specs) {
		t.Errorf("manifest lost installs: %+v", m.Installs)
	}
}

func TestRunPassesOnlyNamedSecrets(t *testing.T) {
	t.Setenv("B_SECRET", "2")
	home, work := t.TempDir(), t.TempDir()
	script := filepath.Join(work, "s.py")
	if err := os.WriteFile(script, []byte("import os\nprint(sorted(k for k in os.environ if k.endswith('_SECRET')))\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Runner{Python: fakePython(t, "{}")}
	out, _, err := r.Run(context.Background(), RunRequest{Home: home, Env: "x", WorkDir: work, Script: script, Secrets: map[string]string{"A_SECRET": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "['A_SECRET']" {
		t.Errorf("script saw %q, want only A_SECRET", out)
	}
}

// A skill script was left to find the knowledge base by searching for a notes/ folder,
// and found the agent's own agents/<id>/notes/ instead — the trading note landed in the
// wrong place. The knowledge base root is a path, not a secret: hand it over.
func TestRunTellsTheScriptWhereTheKnowledgeBaseIs(t *testing.T) {
	work := t.TempDir()
	script := filepath.Join(work, "s.py")
	if err := os.WriteFile(script, []byte("import os\nprint(repr(os.environ.get('ROOKERY_VAULT_ROOT')))\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Runner{Python: fakePython(t, "{}")}
	out, _, err := r.Run(context.Background(), RunRequest{Home: t.TempDir(), Env: "x", WorkDir: work, VaultRoot: "/kb", Script: script})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "'/kb'" {
		t.Errorf("script saw %s, want '/kb'", out)
	}
	out, _, err = r.Run(context.Background(), RunRequest{Home: t.TempDir(), Env: "x", WorkDir: work, Script: script})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "None" {
		t.Errorf("with no knowledge base known the variable must be absent, script saw %s", out)
	}
}

func TestRunRemovesItsTmp(t *testing.T) {
	home, work := t.TempDir(), t.TempDir()
	script := filepath.Join(work, "s.py")
	if err := os.WriteFile(script, []byte("import os\nprint(os.environ['TMPDIR'])\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Runner{Python: fakePython(t, "{}")}
	out, _, err := r.Run(context.Background(), RunRequest{Home: home, Env: "x", WorkDir: work, Script: script})
	if err != nil {
		t.Fatal(err)
	}
	tmp := strings.TrimSpace(out)
	if !strings.HasPrefix(tmp, filepath.Join(home, EnvsRel, "x")) {
		t.Errorf("TMPDIR %q is not inside the env", tmp)
	}
	if _, err := os.Stat(tmp); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("run left its TMPDIR behind: %v", err)
	}
}
