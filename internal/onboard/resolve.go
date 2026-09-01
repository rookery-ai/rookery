package onboard

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Host is everything Resolve needs to know about the machine it is describing.
//
// It is injected for the same reason coder.detectHost is: every bug this code
// exists to fix is specific to a platform we cannot run here, and a host we
// cannot boot still has to be describable in a test.
type Host struct {
	GOOS string
	Home string

	// LookPath searches PATH. exec.LookPath already applies PATHEXT on Windows,
	// so a bare name finds `rg.exe`.
	LookPath func(string) (string, error)
	// Stat is the existence check for the directory search, which LookPath
	// cannot do because those directories are precisely the ones not on PATH.
	Stat func(string) (fs.FileInfo, error)
	// Glob expands the version-stamped directories these installers create
	// (Python3xx, the winget package directory).
	Glob func(string) ([]string, error)
	// Getenv reads the environment the search directories are derived from.
	Getenv func(string) string
	// Verify proves a candidate is the real tool by running it. Nil disables
	// the check, which is what a test describing a fictional filesystem wants:
	// there is nothing there to execute.
	Verify func(path string) bool
}

// CurrentHost describes the machine this process is running on.
func CurrentHost() Host {
	home, _ := os.UserHomeDir()
	return Host{
		GOOS:     runtime.GOOS,
		Home:     home,
		LookPath: exec.LookPath,
		Stat:     os.Stat,
		Glob:     filepath.Glob,
		Getenv:   os.Getenv,
		Verify:   verifyByRunning,
	}
}

func (h Host) lookPath(name string) (string, error) {
	if h.LookPath == nil {
		return "", exec.ErrNotFound
	}
	return h.LookPath(name)
}

func (h Host) getenv(k string) string {
	if h.Getenv == nil {
		return ""
	}
	return h.Getenv(k)
}

func (h Host) glob(pattern string) []string {
	if h.Glob == nil {
		return nil
	}
	out, err := h.Glob(pattern)
	if err != nil {
		return nil
	}
	return out
}

// verifyByRunning runs a candidate with --version and requires it to behave
// like Python.
//
// This exists for one specific decoy. Stock Windows ships an App Execution
// Alias at %LOCALAPPDATA%\Microsoft\WindowsApps\python3.exe which resolves
// through LookPath and opens the Microsoft Store rather than running anything.
// Accepting it would turn this package's failure mode from "offers a tool that
// is already installed" into "reports a missing tool as present" — noisy into
// silent, which is the wrong direction: the AST guardrail self-skips when
// python3 is absent, and the whole point of Critical is that nobody finds out.
func verifyByRunning(path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		return false
	}
	// The Store stub exits non-zero, but a future one might not; requiring the
	// name in the output is the check that actually describes what we want.
	return strings.Contains(string(out), "Python")
}

// binCandidates expands a bare binary name into the file names to look for in a
// directory.
//
// exec.LookPath applies PATHEXT when searching PATH, but a direct Stat does
// not, so the extension has to be supplied here. This mirrors
// coder.binCandidates deliberately: the two solve the identical problem and
// disagreeing about it would be a bug nobody would think to look for.
func binCandidates(goos, bin string) []string {
	if goos != "windows" {
		return []string{bin}
	}
	return []string{bin + ".exe", bin + ".cmd", bin + ".bat", bin, bin + ".ps1"}
}

// searchDirs returns the directories probed after PATH for one tool.
//
// On Linux and macOS this is nearly redundant — these tools land in /usr/bin,
// which is on every PATH — and that is exactly why the change is safe to ship:
// the platform it alters is the one that is broken.
//
// On Windows it is the whole fix. winget puts a portable package's shim in
// Links, and Tesseract's installer does not touch PATH at all, so the tools are
// installed, invisible, and re-offered on every run of setup.
func searchDirs(h Host, t HostTool) []string {
	var dirs []string
	add := func(parts ...string) {
		for _, p := range parts {
			if p != "" {
				dirs = append(dirs, p)
			}
		}
	}

	switch h.GOOS {
	case "windows":
		local := h.getenv("LOCALAPPDATA")
		programFiles := h.getenv("ProgramFiles")

		if local != "" {
			// winget's shim directory. A process that resolved its environment
			// before the install cannot see this on PATH, which is why the
			// installer has to warn about a new terminal and why onboard —
			// often run in that same terminal — could not find anything.
			add(filepath.Join(local, "Microsoft", "WinGet", "Links"))
		}
		// Deliberately NOT added: %LOCALAPPDATA%\Microsoft\WindowsApps. It holds
		// the App Execution Alias stubs, and searching it would find the Python
		// decoy that Verify exists to reject.

		switch t.Bin {
		case "tesseract":
			add(
				filepath.Join(programFiles, "Tesseract-OCR"),
				filepath.Join(local, "Programs", "Tesseract-OCR"),
			)
		case "pdftotext":
			if local != "" {
				add(h.glob(filepath.Join(local, "Microsoft", "WinGet", "Packages",
					"oschwartz10612.Poppler_*", "poppler-*", "Library", "bin"))...)
			}
		case "python3":
			if local != "" {
				add(h.glob(filepath.Join(local, "Programs", "Python", "Python3*"))...)
			}
			if programFiles != "" {
				add(h.glob(filepath.Join(programFiles, "Python3*"))...)
			}
		}
	case "darwin":
		if h.Home != "" {
			add(filepath.Join(h.Home, ".local", "bin"))
		}
		add("/opt/homebrew/bin", "/usr/local/bin")
	default:
		if h.Home != "" {
			add(filepath.Join(h.Home, ".local", "bin"))
		}
		add("/usr/local/bin")
	}
	return dirs
}

// isExecutable reports whether path is a file this host could run.
//
// The mode check is POSIX-only on purpose. Go synthesises file mode from file
// attributes on Windows and never sets 0o111, so testing for it there matches
// nothing at all — the same trap coder.binCandidates records, and the reason
// coder detection's fallback search could not find anything on Windows.
func isExecutable(h Host, path string) bool {
	if h.Stat == nil {
		return false
	}
	fi, err := h.Stat(path)
	if err != nil || fi.IsDir() {
		return false
	}
	if h.GOOS == "windows" {
		return true
	}
	return fi.Mode()&0o111 != 0
}

// accepts applies the per-tool execution check, when there is one.
func (h Host) accepts(t HostTool, path string) bool {
	if !t.VerifyByRunning || h.Verify == nil {
		return true
	}
	return h.Verify(path)
}

// Resolve returns the path to a usable copy of t on this host.
//
// PATH first — that is where a correctly installed tool lives, and it is what
// every consumer in the codebase uses. The directory search is the fallback for
// the installers that do not put themselves there.
func Resolve(h Host, t HostTool) (string, bool) {
	names := t.Bins(h.GOOS)

	for _, name := range names {
		if p, err := h.lookPath(name); err == nil && h.accepts(t, p) {
			return p, true
		}
	}
	for _, dir := range searchDirs(h, t) {
		for _, name := range names {
			for _, cand := range binCandidates(h.GOOS, name) {
				p := filepath.Join(dir, cand)
				if isExecutable(h, p) && h.accepts(t, p) {
					return p, true
				}
			}
		}
	}
	return "", false
}

// MissingOn returns the host tools this host cannot resolve, in canonical order.
func MissingOn(h Host) []HostTool {
	var out []HostTool
	for _, t := range HostTools {
		if _, ok := Resolve(h, t); !ok {
			out = append(out, t)
		}
	}
	return out
}

// ToolDirs returns the directories holding resolved tools that PATH does not
// already reach. It is what PATH augmentation prepends; see AugmentPath.
func ToolDirs(h Host) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range HostTools {
		p, ok := Resolve(h, t)
		if !ok {
			continue
		}
		// A tool PATH already finds needs no directory added. Asking LookPath
		// again is the honest test: it is the same question every consumer in
		// the codebase asks.
		if _, err := h.lookPath(t.Bin); err == nil {
			continue
		}
		dir := filepath.Dir(p)
		if dir == "" || dir == "." || seen[dir] {
			continue
		}
		seen[dir] = true
		out = append(out, dir)
	}
	return out
}
