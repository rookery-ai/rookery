package onboard

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPathWithAppendsOnlyWhatIsMissing(t *testing.T) {
	got, changed := PathWith("/usr/bin:/bin", []string{"/opt/x", "/usr/bin", "/opt/y"}, ":")

	if !changed {
		t.Fatal("adding two new directories reported no change")
	}
	if got != "/usr/bin:/bin:/opt/x:/opt/y" {
		t.Errorf("got %q", got)
	}
}

// A deliberate operator override must stay in front of anything this package
// infers, which is the whole reason these are appended rather than prepended.
func TestPathWithDoesNotShadowExistingEntries(t *testing.T) {
	got, _ := PathWith("/my/bin", []string{"/opt/x"}, ":")
	if !strings.HasPrefix(got, "/my/bin") {
		t.Errorf("the existing PATH must stay in front: %q", got)
	}
}

// Reporting a change when there is none would make every start grow the
// variable with entries that do nothing.
func TestPathWithReportsNoChangeWhenNothingIsNew(t *testing.T) {
	if _, changed := PathWith("/usr/bin", []string{"/usr/bin"}, ":"); changed {
		t.Error("re-adding an existing entry reported a change")
	}
}

func TestPathWithHandlesAnEmptyPath(t *testing.T) {
	got, changed := PathWith("", []string{"/opt/x"}, ":")
	if !changed || got != "/opt/x" {
		t.Errorf("got %q changed=%v", got, changed)
	}
}

// writeFakeTools puts a runnable stand-in for each host tool in dir. python3's
// stand-in has to actually answer --version, because that tool is verified by
// running it.
func writeFakeTools(t *testing.T, dir string) {
	t.Helper()
	for _, tool := range HostTools {
		body := "#!/bin/sh\nexit 0\n"
		if tool.Bin == "python3" {
			body = "#!/bin/sh\necho 'Python 3.13.0'\n"
		}
		p := filepath.Join(dir, tool.Bin)
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
}

// The mechanism, end to end: a tool that PATH cannot reach becomes reachable
// through exec.LookPath — which is the call every consumer in the codebase
// makes.
func TestAugmentProcessPathMakesAnOffPathToolResolvable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake tools are shell scripts")
	}
	home := t.TempDir()
	localBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeTools(t, localBin)

	// An empty PATH is the point: it removes every dependency on what happens to
	// be installed on the machine running this test.
	t.Setenv("HOME", home)
	t.Setenv("PATH", "")

	if _, err := exec.LookPath("tesseract"); err == nil {
		t.Fatal("precondition: the tool must not be resolvable before augmenting")
	}

	added := AugmentProcessPath()
	if len(added) == 0 {
		t.Fatal("nothing was added to PATH though every tool was off it")
	}
	if _, err := exec.LookPath("tesseract"); err != nil {
		t.Errorf("the tool is still unresolvable after augmenting PATH: %v", err)
	}
}

// Coder subprocesses, the sandbox helper and every script an agent runs are
// children of this process. If the augmentation did not survive the fork it
// would fix the report and none of the behaviour.
func TestTheAugmentedPathIsInheritedByChildren(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake tools are shell scripts")
	}
	home := t.TempDir()
	localBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeTools(t, localBin)

	t.Setenv("HOME", home)
	t.Setenv("PATH", "/nonexistent")
	AugmentProcessPath()

	out, err := exec.Command("/bin/sh", "-c", "command -v tesseract").CombinedOutput()
	if err != nil {
		t.Fatalf("a child process could not resolve the tool: %v (%s)", err, out)
	}
	if !strings.Contains(string(out), localBin) {
		t.Errorf("child resolved something unexpected: %s", out)
	}
}

// Nothing to do must cost nothing. On Linux and macOS this is the normal case —
// these tools live in /usr/bin — and it is what makes the change safe to ship:
// the only platform whose behaviour moves is the one that is broken.
func TestAugmentProcessPathIsANoOpWhenPathAlreadyReachesEverything(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake tools are shell scripts")
	}
	dir := t.TempDir()
	writeFakeTools(t, dir)

	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", dir)

	if added := AugmentProcessPath(); len(added) != 0 {
		t.Errorf("added %v though PATH already resolved every tool", added)
	}
	if os.Getenv("PATH") != dir {
		t.Errorf("PATH was modified with nothing to add: %q", os.Getenv("PATH"))
	}
}
