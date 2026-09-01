package onboard

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeFS is a described filesystem. Every bug this file is about belongs to a
// platform there is no host for here, so the host is described rather than
// probed — the same technique coder/detect_platform_test.go uses, and for the
// same reason.
type fakeFS struct {
	files map[string]fs.FileMode // path → mode
	path  map[string]string      // bare name → resolved path, i.e. what PATH finds
	env   map[string]string
	// ran records the paths Verify was asked about, so a test can prove the
	// execution check happened rather than merely that the answer came out right.
	ran []string
	// python names the one path that behaves like a real interpreter.
	python string
}

type fakeInfo struct {
	mode fs.FileMode
}

func (f fakeInfo) Name() string       { return "" }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fakeInfo) Sys() any           { return nil }

func (f *fakeFS) host(goos string) Host {
	return Host{
		GOOS: goos,
		Home: "/home/o",
		LookPath: func(name string) (string, error) {
			if p, ok := f.path[name]; ok {
				return p, nil
			}
			return "", os.ErrNotExist
		},
		Stat: func(p string) (fs.FileInfo, error) {
			m, ok := f.files[p]
			if !ok {
				return nil, os.ErrNotExist
			}
			return fakeInfo{mode: m}, nil
		},
		Glob: func(pattern string) ([]string, error) {
			var out []string
			for p, m := range f.files {
				if !m.IsDir() {
					continue
				}
				if ok, _ := filepath.Match(pattern, p); ok {
					out = append(out, p)
				}
			}
			return out, nil
		},
		Getenv: func(k string) string { return f.env[k] },
		Verify: func(p string) bool {
			f.ran = append(f.ran, p)
			return p == f.python
		},
	}
}

func windowsFS() *fakeFS {
	return &fakeFS{
		files: map[string]fs.FileMode{},
		path:  map[string]string{},
		env: map[string]string{
			"LOCALAPPDATA": "C:/Users/o/AppData/Local",
			"ProgramFiles": "C:/Program Files",
		},
	}
}

func toolNamed(t *testing.T, bin string) HostTool {
	t.Helper()
	for _, ht := range HostTools {
		if ht.Bin == bin {
			return ht
		}
	}
	t.Fatalf("no host tool named %q", bin)
	return HostTool{}
}

// The reported bug, reduced: the installer put Tesseract on the machine and
// setup could not see it.
//
// UB-Mannheim.TesseractOCR installs into Program Files and does not touch PATH,
// so a probe that is only ever exec.LookPath reports it missing — on that run
// and on every run afterwards, because installing it again changes nothing.
func TestResolveFindsAWindowsToolThatIsNotOnPath(t *testing.T) {
	f := windowsFS()
	f.files["C:/Program Files/Tesseract-OCR/tesseract.exe"] = 0

	got, ok := Resolve(f.host("windows"), toolNamed(t, "tesseract"))
	if !ok {
		t.Fatal("tesseract is installed in Program Files and was reported missing")
	}
	if got != "C:/Program Files/Tesseract-OCR/tesseract.exe" {
		t.Errorf("resolved to %q", got)
	}
}

// winget writes a portable package's shim into its Links directory, which a
// process that resolved its environment before the install cannot see on PATH —
// which is exactly the case when the installer and setup run in one terminal.
func TestResolveFindsTheWingetShimDirectory(t *testing.T) {
	f := windowsFS()
	f.files["C:/Users/o/AppData/Local/Microsoft/WinGet/Links/pdftotext.exe"] = 0

	if _, ok := Resolve(f.host("windows"), toolNamed(t, "pdftotext")); !ok {
		t.Fatal("a tool reachable only through winget's Links directory was reported missing")
	}
}

// The mismatch that made this permanent: install.ps1 probes and installs
// `python`, this package probed `python3`, and python.org's distribution ships
// no python3.exe. So the installer succeeded and setup asked for it again,
// forever.
func TestResolveAcceptsWindowsPythonSpellings(t *testing.T) {
	f := windowsFS()
	f.path["python"] = "C:/Python313/python.exe"
	f.python = "C:/Python313/python.exe"

	got, ok := Resolve(f.host("windows"), toolNamed(t, "python3"))
	if !ok {
		t.Fatal("Python is installed as python.exe and was reported missing")
	}
	if got != "C:/Python313/python.exe" {
		t.Errorf("resolved to %q", got)
	}
}

// On Linux the extra spellings must NOT apply: `python` there is frequently
// Python 2 or absent, and the canonical name is the right and only probe.
func TestResolveDoesNotAcceptTheWindowsSpellingsElsewhere(t *testing.T) {
	f := &fakeFS{
		files:  map[string]fs.FileMode{},
		path:   map[string]string{"python": "/usr/bin/python"},
		env:    map[string]string{},
		python: "/usr/bin/python",
	}
	if _, ok := Resolve(f.host("linux"), toolNamed(t, "python3")); ok {
		t.Error("a bare `python` must not satisfy python3 off Windows")
	}
}

// Stock Windows ships an App Execution Alias stub named python3.exe that
// resolves through LookPath and opens the Microsoft Store instead of running
// anything.
//
// Accepting it would change this package's failure mode from "offers a tool
// that is already installed" to "reports a missing tool as present" — and the
// second is far worse, because python3's absence silently disables the
// agent-tool AST guardrail rather than printing anything.
func TestResolveRejectsTheWindowsStorePythonStub(t *testing.T) {
	f := windowsFS()
	stub := "C:/Users/o/AppData/Local/Microsoft/WindowsApps/python3.exe"
	f.path["python3"] = stub
	f.path["python"] = "C:/Python313/python.exe"
	f.python = "C:/Python313/python.exe" // the stub is not it

	got, ok := Resolve(f.host("windows"), toolNamed(t, "python3"))
	if !ok {
		t.Fatal("the real interpreter was present under another name and was not found")
	}
	if got == stub {
		t.Fatal("the Microsoft Store stub was accepted as a working python3")
	}
	if len(f.ran) == 0 {
		t.Error("python3 must be verified by running it, not by existing")
	}
}

// A host with nothing but the stub has no Python, and must say so rather than
// reporting one it cannot run.
func TestResolveReportsNoPythonWhenOnlyTheStubExists(t *testing.T) {
	f := windowsFS()
	f.path["python3"] = "C:/Users/o/AppData/Local/Microsoft/WindowsApps/python3.exe"

	if _, ok := Resolve(f.host("windows"), toolNamed(t, "python3")); ok {
		t.Error("a host with only the Store stub must report python3 missing")
	}
}

// Go synthesises file mode from file attributes on Windows and never sets the
// executable bits, so a mode test there matches nothing at all. This is the
// trap that made coder detection's fallback search unable to find anything on
// Windows; it must not be reintroduced here.
func TestResolveDoesNotRequireAnExecutableBitOnWindows(t *testing.T) {
	f := windowsFS()
	f.files["C:/Program Files/Tesseract-OCR/tesseract.exe"] = 0 // no 0o111 anywhere

	if _, ok := Resolve(f.host("windows"), toolNamed(t, "tesseract")); !ok {
		t.Error("a Windows binary was rejected for lacking an executable bit Go never sets there")
	}
}

// Off Windows the bit is real and is the right test.
func TestResolveRequiresAnExecutableBitOnPosix(t *testing.T) {
	f := &fakeFS{files: map[string]fs.FileMode{}, path: map[string]string{}, env: map[string]string{}}
	f.files["/usr/local/bin/tesseract"] = 0o644

	if _, ok := Resolve(f.host("linux"), toolNamed(t, "tesseract")); ok {
		t.Error("a non-executable file was accepted as a tool")
	}
	f.files["/usr/local/bin/tesseract"] = 0o755
	if _, ok := Resolve(f.host("linux"), toolNamed(t, "tesseract")); !ok {
		t.Error("an executable file was not accepted")
	}
}

// PATH wins, because that is where a correctly installed tool lives and it is
// what every consumer in the codebase actually asks.
func TestResolvePrefersPath(t *testing.T) {
	f := windowsFS()
	f.path["rg"] = "C:/tools/rg.exe"
	f.files["C:/Users/o/AppData/Local/Microsoft/WinGet/Links/rg.exe"] = 0

	got, _ := Resolve(f.host("windows"), toolNamed(t, "rg"))
	if got != "C:/tools/rg.exe" {
		t.Errorf("the directory search overrode PATH: %q", got)
	}
}

// ToolDirs feeds PATH augmentation, so it must name only the directories PATH
// does not already reach — otherwise every start would grow the variable with
// entries that change nothing.
func TestToolDirsNamesOnlyWhatPathCannotReach(t *testing.T) {
	f := windowsFS()
	f.path["rg"] = "C:/tools/rg.exe" // already reachable
	f.files["C:/Program Files/Tesseract-OCR/tesseract.exe"] = 0

	dirs := ToolDirs(f.host("windows"))

	if len(dirs) != 1 || dirs[0] != "C:/Program Files/Tesseract-OCR" {
		t.Fatalf("expected only the off-PATH directory, got %v", dirs)
	}
}

// The whole point of the fallback search is that setup stops re-offering what
// is already installed.
func TestMissingOnIsEmptyWhenEverythingIsInstalledOffPath(t *testing.T) {
	f := windowsFS()
	local := "C:/Users/o/AppData/Local"
	f.files[local+"/Microsoft/WinGet/Links/rg.exe"] = 0
	f.files[local+"/Microsoft/WinGet/Links/pdftotext.exe"] = 0
	f.files["C:/Program Files/Tesseract-OCR/tesseract.exe"] = 0
	f.path["python"] = "C:/Python313/python.exe"
	f.python = "C:/Python313/python.exe"

	if got := MissingOn(f.host("windows")); len(got) != 0 {
		var names []string
		for _, t := range got {
			names = append(names, t.Bin)
		}
		t.Errorf("setup would re-offer tools that are installed: %s", strings.Join(names, ", "))
	}
}

// Canonical order is part of the contract — the summary setup prints reads in
// it, and Critical comes first.
func TestMissingOnKeepsCanonicalOrder(t *testing.T) {
	f := windowsFS()
	got := MissingOn(f.host("windows"))
	if len(got) != len(HostTools) {
		t.Fatalf("a bare host must report every tool missing, got %d", len(got))
	}
	for i, ht := range HostTools {
		if got[i].Bin != ht.Bin {
			t.Fatalf("order diverged at %d: %q vs %q", i, got[i].Bin, ht.Bin)
		}
	}
}
