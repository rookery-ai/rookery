package coder

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeInfo is an os.FileInfo for a host that does not exist. Mode is the field
// that matters: Windows never reports an executable bit, and requiring one there
// is what made the fallback search find nothing at all.
type fakeInfo struct {
	name string
	mode fs.FileMode
}

func (f fakeInfo) Name() string       { return f.name }
func (f fakeInfo) Size() int64        { return 1 }
func (f fakeInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fakeInfo) Sys() any           { return nil }

// hostWith builds a detectHost whose PATH resolves nothing and whose filesystem
// contains exactly the given paths. Nothing on PATH is the interesting case:
// PATH already worked, the fallback search is what was broken.
func hostWith(goos, home string, env map[string]string, files map[string]fs.FileMode) detectHost {
	return detectHost{
		GOOS: goos,
		Home: home,
		LookPath: func(string) (string, error) {
			return "", exec.ErrNotFound
		},
		Stat: func(p string) (os.FileInfo, error) {
			p = filepath.ToSlash(p)
			if mode, ok := files[p]; ok {
				return fakeInfo{name: filepath.Base(p), mode: mode}, nil
			}
			return nil, os.ErrNotExist
		},
		Getenv: func(k string) string { return env[k] },
	}
}

func binsOf(found []Installed) string {
	var out []string
	for _, f := range found {
		out = append(out, filepath.ToSlash(f.Bin))
	}
	return strings.Join(out, ",")
}

// Homebrew is where a macOS user's coder actually lives, and a launchd-started
// process inherits a PATH that contains neither Homebrew prefix. Detection would
// fail for someone whose terminal finds the binary without any trouble.
func TestDetectFindsHomebrewOnMacOS(t *testing.T) {
	for _, prefix := range []string{"/opt/homebrew/bin", "/usr/local/bin"} {
		h := hostWith("darwin", "/Users/someone", nil, map[string]fs.FileMode{
			prefix + "/claude": 0o755,
		})
		found := detectInstalled(h)
		if len(found) != 1 || filepath.ToSlash(found[0].Bin) != prefix+"/claude" {
			t.Errorf("prefix %s: got %v", prefix, binsOf(found))
		}
	}
}

// npm's global shims are the normal way these coders are installed on Windows,
// and they are .cmd files, not extensionless binaries. exec.LookPath applies
// PATHEXT when searching PATH but a direct Stat does not, so the fallback has to
// expand the name itself or it finds nothing.
func TestDetectFindsNpmShimsOnWindows(t *testing.T) {
	h := hostWith("windows", `C:/Users/someone`, map[string]string{
		"APPDATA":      `C:/Users/someone/AppData/Roaming`,
		"LOCALAPPDATA": `C:/Users/someone/AppData/Local`,
	}, map[string]fs.FileMode{
		// 0666: Go synthesizes mode bits on Windows from file attributes and
		// never sets an executable bit. This is the exact condition the old
		// `fi.Mode()&0o111 != 0` test could not pass.
		`C:/Users/someone/AppData/Roaming/npm/claude.cmd`: 0o666,
	})

	found := detectInstalled(h)
	if len(found) != 1 {
		t.Fatalf("want the npm shim found, got %v", binsOf(found))
	}
	if !strings.HasSuffix(filepath.ToSlash(found[0].Bin), "npm/claude.cmd") {
		t.Errorf("resolved to %s", found[0].Bin)
	}
	if found[0].BackendType != "claude" {
		t.Errorf("backend = %q, want claude", found[0].BackendType)
	}
}

// The regression this whole change is about: a Windows file carries no
// executable bit, so demanding one rejected every candidate and the fallback
// search could never find anything.
func TestWindowsDetectionDoesNotRequireAnExecutableBit(t *testing.T) {
	h := hostWith("windows", `C:/Users/someone`, map[string]string{
		"LOCALAPPDATA": `C:/Users/someone/AppData/Local`,
	}, map[string]fs.FileMode{
		`C:/Users/someone/AppData/Local/Programs/opencode.exe`: 0o666,
	})
	if got := detectInstalled(h); len(got) != 1 {
		t.Fatalf("want opencode.exe found without an exec bit, got %v", binsOf(got))
	}
}

// On POSIX the executable bit is real and a non-executable file is not a coder.
func TestPosixDetectionStillRequiresAnExecutableBit(t *testing.T) {
	h := hostWith("linux", "/home/someone", nil, map[string]fs.FileMode{
		"/home/someone/.local/bin/claude": 0o644,
	})
	if got := detectInstalled(h); len(got) != 0 {
		t.Fatalf("a non-executable file is not an installed coder, got %v", binsOf(got))
	}
}

func TestDetectSkipsDirectories(t *testing.T) {
	h := hostWith("linux", "/home/someone", nil, map[string]fs.FileMode{
		"/home/someone/.local/bin/claude": fs.ModeDir | 0o755,
	})
	if got := detectInstalled(h); len(got) != 0 {
		t.Fatalf("a directory named like a coder is not a coder, got %v", binsOf(got))
	}
}

// Every platform must keep looking somewhere after PATH. An empty fallback list
// means detection is PATH-only again, which is the state this replaced.
func TestEveryPlatformSearchesBeyondPath(t *testing.T) {
	cases := []struct {
		goos string
		env  map[string]string
		want string // one directory that must be searched
	}{
		{"linux", nil, "/home/someone/.local/bin"},
		{"darwin", nil, "/opt/homebrew/bin"},
		{"windows", map[string]string{"APPDATA": `C:/Users/someone/AppData/Roaming`}, `C:/Users/someone/AppData/Roaming/npm`},
	}
	for _, c := range cases {
		home := "/home/someone"
		if c.goos == "windows" {
			home = `C:/Users/someone`
		}
		dirs := coderSearchDirs(detectHost{GOOS: c.goos, Home: home, Getenv: func(k string) string { return c.env[k] }})
		if len(dirs) == 0 {
			t.Fatalf("%s searches nothing beyond PATH", c.goos)
		}
		var hit bool
		for _, d := range dirs {
			if filepath.ToSlash(d) == c.want {
				hit = true
			}
		}
		if !hit {
			t.Errorf("%s does not search %s (searches %v)", c.goos, c.want, dirs)
		}
	}
}

// A host with no home directory and no environment must not panic or produce
// paths rooted at "". This is the launchd/service case, where neither is set.
func TestSearchDirsSurviveAnEmptyEnvironment(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		dirs := coderSearchDirs(detectHost{GOOS: goos})
		for _, d := range dirs {
			if d == "" || strings.HasPrefix(filepath.ToSlash(d), "/npm") {
				t.Errorf("%s produced a bogus search dir %q", goos, d)
			}
		}
	}
}

// PATH still wins, and its answer is used as-is rather than re-derived from the
// fallback list.
func TestPathResolutionTakesPrecedence(t *testing.T) {
	h := detectHost{
		GOOS: "linux",
		Home: "/home/someone",
		LookPath: func(bin string) (string, error) {
			if bin == "claude" {
				return "/usr/bin/claude", nil
			}
			return "", exec.ErrNotFound
		},
		Stat: func(p string) (os.FileInfo, error) {
			if filepath.ToSlash(p) == "/home/someone/.local/bin/claude" {
				return fakeInfo{name: "claude", mode: 0o755}, nil
			}
			return nil, os.ErrNotExist
		},
		Getenv: func(string) string { return "" },
	}
	found := detectInstalled(h)
	if len(found) != 1 || found[0].Bin != "/usr/bin/claude" {
		t.Fatalf("PATH should win, got %v", binsOf(found))
	}
}

// Windows expands a bare name the way PATHEXT would; other platforms must not,
// or a Linux host starts statting claude.exe.
func TestBinCandidatesAreWindowsOnly(t *testing.T) {
	if got := binCandidates("linux", "claude"); len(got) != 1 || got[0] != "claude" {
		t.Fatalf("linux candidates = %v, want [claude]", got)
	}
	win := binCandidates("windows", "claude")
	for _, want := range []string{"claude.exe", "claude.cmd", "claude.bat"} {
		var hit bool
		for _, g := range win {
			if g == want {
				hit = true
			}
		}
		if !hit {
			t.Errorf("windows candidates %v missing %s", win, want)
		}
	}
}

// Two coders resolving to the same file must be reported once.
func TestDetectDeduplicatesByResolvedPath(t *testing.T) {
	h := hostWith("linux", "/home/someone", nil, map[string]fs.FileMode{
		"/home/someone/.local/bin/claude":      0o755,
		"/home/someone/.local/bin/claude-code": 0o755,
	})
	found := detectInstalled(h)
	if len(found) != 1 {
		t.Fatalf("Claude Code should be reported once, got %v", binsOf(found))
	}
}
