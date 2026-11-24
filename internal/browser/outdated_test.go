package browser

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// playwrightCache lays out a real-shaped cache: ms-playwright-go/<version>/package
// for each driver version given, and ms-playwright/<build> for each build.
//
// A driver may carry a browsers.json naming the Chromium revision it launches,
// which is what lets an upgrade be told apart from an install: Playwright names
// builds <product>-<revision> and leaves the old ones in place.
type cacheSpec struct {
	drivers map[string]string // version -> chromium revision in browsers.json ("" = no file)
	builds  []string
}

func playwrightCache(t *testing.T, spec cacheSpec) (driverRoot, browsers string) {
	t.Helper()
	root := t.TempDir()
	driverRoot = filepath.Join(root, "ms-playwright-go")
	browsers = filepath.Join(root, "ms-playwright")
	for version, rev := range spec.drivers {
		pkg := filepath.Join(driverRoot, version, "package")
		if err := os.MkdirAll(pkg, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pkg, "cli.js"), []byte("//"), 0o644); err != nil {
			t.Fatal(err)
		}
		if rev != "" {
			doc := `{"browsers":[` +
				`{"name":"chromium","revision":"` + rev + `"},` +
				`{"name":"chromium-headless-shell","revision":"` + rev + `"},` +
				`{"name":"chromium-tip-of-tree-headless-shell","revision":"9999"}]}`
			if err := os.WriteFile(filepath.Join(pkg, "browsers.json"), []byte(doc), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.MkdirAll(browsers, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, b := range spec.builds {
		dir := filepath.Join(browsers, b)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(b, "chromium") {
			name := "chrome"
			if strings.HasPrefix(b, "chromium_headless_shell") {
				name = "chrome-headless-shell"
			}
			if runtime.GOOS == "windows" {
				name += ".exe"
			}
			if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	return driverRoot, browsers
}

// The case this file exists for: a dependency bump moved the driver to a new
// version directory, and the one the owner installed is still sitting beside it.
// Before, nothing noticed — the browser tools simply stopped being offered.
func TestAnOlderDriverIsReportedAsOutdated(t *testing.T) {
	root, browsers := playwrightCache(t, cacheSpec{
		drivers: map[string]string{"1.62.1": "1234"},
		builds:  []string{"chromium-1234", "chromium_headless_shell-1234"},
	})
	s := staleWith(root, "1.64.1", browsers, bareHost())
	if !s.Outdated {
		t.Fatal("a driver installed for 1.62.1 under a build needing 1.64.1 must be reported outdated")
	}
	for _, want := range []string{"1.62.1", "1.64.1", "rookery browser install"} {
		if !strings.Contains(s.Reason, want) {
			t.Errorf("reason %q should name %q", s.Reason, want)
		}
	}
}

// A new driver can launch a newer Chromium than the one cached. Any chromium-*
// directory used to count as "installed", so this read as a working browser
// and every launch then failed looking for a build that was never downloaded.
func TestAStaleBundledChromiumIsReportedAsOutdated(t *testing.T) {
	root, browsers := playwrightCache(t, cacheSpec{
		drivers: map[string]string{"1.64.1": "1250"},
		builds:  []string{"chromium-1234", "chromium_headless_shell-1234"},
	})
	if c := resolveWith(filepath.Join(root, "1.64.1"), browsers, bareHost()); c.OK {
		t.Fatalf("resolve must not pick a Chromium build the driver will not launch: %+v", c)
	}
	if s := staleWith(root, "1.64.1", browsers, bareHost()); !s.Outdated {
		t.Fatal("a cached Chromium older than the driver expects must be reported outdated")
	}
}

// The expected build being present is the healthy case, old builds left beside
// it included — Playwright does not always remove them.
func TestACurrentInstallIsNotOutdated(t *testing.T) {
	root, browsers := playwrightCache(t, cacheSpec{
		drivers: map[string]string{"1.62.1": "1234", "1.64.1": "1250"},
		builds:  []string{"chromium-1234", "chromium_headless_shell-1234", "chromium_headless_shell-1250"},
	})
	if c := resolveWith(filepath.Join(root, "1.64.1"), browsers, bareHost()); !c.OK || c.Source != SourceManaged {
		t.Fatalf("the expected build is present and should be used: %+v", c)
	}
	if s := staleWith(root, "1.64.1", browsers, bareHost()); s.Outdated {
		t.Fatalf("a current install reported outdated: %s", s.Reason)
	}
}

// Most installs never opt into the browser. Reporting them as outdated would
// put a warning on every default install, which is how a warnings list stops
// being read — the reason health.Warnings deliberately says nothing about an
// absent browser.
func TestANeverInstalledBrowserIsNotOutdated(t *testing.T) {
	root, browsers := playwrightCache(t, cacheSpec{})
	if s := staleWith(root, "1.64.1", browsers, bareHost()); s.Outdated {
		t.Fatalf("an install with no browser at all reported outdated: %s", s.Reason)
	}
}

// With a stale bundled Chromium AND the operator's own Chrome, the current
// driver can still drive Chrome — so the browser works and nothing is outdated.
// Before, resolve picked the stale managed build first and every launch failed.
func TestAStaleChromiumFallsBackToTheSystemChrome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("system Chrome paths in hostWith are POSIX here")
	}
	root, browsers := playwrightCache(t, cacheSpec{
		drivers: map[string]string{"1.64.1": "1250"},
		builds:  []string{"chromium_headless_shell-1234"},
	})
	h := hostWith("linux", nil, "/opt/google/chrome/chrome")
	c := resolveWith(filepath.Join(root, "1.64.1"), browsers, h)
	if !c.OK || c.Source != SourceChannel {
		t.Fatalf("expected the system Chrome, got %+v", c)
	}
	if s := staleWith(root, "1.64.1", browsers, h); s.Outdated {
		t.Fatalf("a working system Chrome reported outdated: %s", s.Reason)
	}
}

// ~/.cache/ms-playwright is shared by every Playwright binding — a developer's
// Python or Node test project fills it too. Builds there are not evidence the
// owner ever installed Rookery's browser; only Rookery's own driver directory
// (ms-playwright-go) is. Counting them flagged such machines as "outdated" and
// let `upgrade` download ~200 MB onto a host that never opted in.
func TestAnotherProjectsPlaywrightCacheIsNotOurs(t *testing.T) {
	root, browsers := playwrightCache(t, cacheSpec{
		builds: []string{"chromium-1100", "chromium_headless_shell-1100", "firefox-1400"},
	})
	if s := staleWith(root, "1.64.1", browsers, bareHost()); s.Outdated {
		t.Fatalf("another project's Playwright cache was read as Rookery's own install: %s", s.Reason)
	}
}

// A driver whose browsers.json names only the full Chromium (no headless-shell
// entry) must be checked against chromium-<rev>, not a shell directory that the
// driver would never have downloaded.
func TestAFullChromiumOnlyDriverChecksTheFullBuild(t *testing.T) {
	root, browsers := playwrightCache(t, cacheSpec{builds: []string{"chromium-1234"}})
	pkg := filepath.Join(root, "1.50.0", "package")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(pkg, "cli.js"), []byte("//"), 0o644)
	_ = os.WriteFile(filepath.Join(pkg, "browsers.json"),
		[]byte(`{"browsers":[{"name":"chromium","revision":"1234"}]}`), 0o644)
	if !managedChromiumReady(filepath.Join(root, "1.50.0"), browsers) {
		t.Fatal("chromium-1234 is exactly what this driver launches and should count")
	}
}

// An unknown driver version means the probe itself failed; claiming the runtime
// is outdated on no evidence would send the owner off to reinstall for nothing.
func TestAnUnknownVersionIsNeverOutdated(t *testing.T) {
	root, browsers := playwrightCache(t, cacheSpec{
		drivers: map[string]string{"1.62.1": "1234"},
		builds:  []string{"chromium_headless_shell-1234"},
	})
	if s := staleWith(root, "", browsers, bareHost()); s.Outdated {
		t.Fatal("an unknown driver version must not be reported outdated")
	}
}
