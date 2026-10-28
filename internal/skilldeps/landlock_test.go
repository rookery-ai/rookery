package skilldeps

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/sandbox"
)

// TestMain lets this test binary act as the sandbox helper, so the confinement below is
// exercised for real (Landlock applied, then exec) rather than only described by a Spec.
func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == sandbox.HelperCommand {
		spec, err := sandbox.DecodeSpec(os.Args[2])
		if err == nil {
			err = sandbox.Exec(spec)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// The property the whole design rests on, checked against the kernel: a script run
// from an environment cannot write the workspace home or the coder's credentials,
// can write its working directory, and sees only the secret it was given.
func TestRunIsConfinedUnderLandlock(t *testing.T) {
	if !sandbox.Supported() {
		t.Skip("Landlock not available on this kernel")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home, work := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(work, "s.py")
	src := fmt.Sprintf(`import os
print("secrets", sorted(k for k in os.environ if k.endswith("_SECRET")))
for target in (%q, %q):
    try:
        open(target, "w").close()
        print("WROTE", target)
    except OSError:
        print("blocked")
open("out.txt", "w").close()
print("workdir ok")
`, filepath.Join(home, "probe"), filepath.Join(home, ".claude", "x"))
	if err := os.WriteFile(script, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("B_SECRET", "2")

	r := Runner{SelfExe: self, Sandbox: true}
	out, stderr, err := r.Run(context.Background(), RunRequest{Home: home, Env: "x", WorkDir: work, Script: script, Secrets: map[string]string{"A_SECRET": "1"}})
	if err != nil {
		t.Fatalf("run: %v\nstdout: %s\nstderr: %s", err, out, stderr)
	}
	if strings.Contains(out, "WROTE") {
		t.Errorf("confined script wrote into the workspace home:\n%s", out)
	}
	if strings.Count(out, "blocked") != 2 || !strings.Contains(out, "workdir ok") || !strings.Contains(out, "secrets ['A_SECRET']") {
		t.Errorf("unexpected output:\n%s", out)
	}
}
