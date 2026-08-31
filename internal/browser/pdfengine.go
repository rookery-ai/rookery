package browser

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// ChromiumExecutable returns the path to the Chromium build this platform
// installed with `rookery browser install`, or "" when there is none.
//
// It exists because a working renderer was invisible to the one place that
// needed it. internal/export probes PATH for "chromium", "chromium-browser" and
// friends, but Playwright installs into a versioned cache directory under a
// different file name — so on a host where /healthz reported "browser": true,
// PDF export reported "unavailable" and told the operator to install a Chromium
// they already had. The argv export uses was verified against this exact binary.
//
// The headless shell is preferred over the full browser: it is the build meant
// for exactly this (no window, no GPU stack) and produced a markedly smaller PDF
// from identical input. Either works.
//
// Deliberately a plain path lookup with no side effects, so it can be called
// from an availability probe — the same constraint that shaped Probe.
// It falls back to a Chromium-family browser the operator already has. Without
// that, an install whose only browser is a system Chrome would render pages
// perfectly well through Playwright and still report PDF export as
// unavailable — the same "invisible working renderer" this function was written
// to fix, reintroduced by the change that stopped downloading Chromium when one
// was already present.
func ChromiumExecutable() string {
	if p := chromiumExecutableIn(browsersDir()); p != "" {
		return p
	}
	return SystemChromiumExecutable()
}

// chromiumExecutableIn is the testable half: the caller supplies the browsers
// directory, so a test can describe a cache layout without one being installed.
func chromiumExecutableIn(dir string) string {
	if dir == "" {
		return ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	// Newest revision first. Playwright names these <product>-<revision> and
	// leaves older builds in place after an upgrade, so without ordering the
	// choice between two installed revisions would be directory order — stable
	// enough to look correct, arbitrary enough to be wrong.
	var shells, fulls []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		switch {
		case strings.HasPrefix(e.Name(), "chromium_headless_shell-"):
			shells = append(shells, e.Name())
		case strings.HasPrefix(e.Name(), "chromium-"):
			fulls = append(fulls, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(shells)))
	sort.Sort(sort.Reverse(sort.StringSlice(fulls)))

	for _, name := range append(shells, fulls...) {
		if p := findChromiumBinary(filepath.Join(dir, name)); p != "" {
			return p
		}
	}
	return ""
}

// chromiumBinaryNames are the executable names Playwright's Chromium builds use,
// per platform. Listed rather than derived, because the enclosing directory name
// varies by both product and platform (chrome-linux64, chrome-mac,
// chrome-headless-shell-win64, …) and enumerating those pairs is more brittle
// than looking for the executable itself.
func chromiumBinaryNames() []string {
	if runtime.GOOS == "windows" {
		return []string{"chrome.exe", "chrome-headless-shell.exe"}
	}
	// "Chromium" is the macOS app-bundle executable.
	return []string{"chrome", "chrome-headless-shell", "Chromium"}
}

// findChromiumBinary looks for the executable inside ONE build directory. The
// walk is bounded to that directory rather than the whole cache, and stops at
// the first match.
func findChromiumBinary(buildDir string) string {
	want := map[string]bool{}
	for _, n := range chromiumBinaryNames() {
		want[n] = true
	}
	var found string
	_ = filepath.WalkDir(buildDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || found != "" {
			return nil
		}
		if d.IsDir() || !want[d.Name()] {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		// The executable bit is meaningful on POSIX and is never set by Go on
		// Windows, where the file name alone is the signal — the same split
		// coder detection documents.
		if runtime.GOOS == "windows" || fi.Mode()&0o111 != 0 {
			found = path
		}
		return nil
	})
	return found
}
