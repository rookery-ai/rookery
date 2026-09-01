package browser

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// cache builds a Playwright cache layout on disk: a driver, and whichever
// browser builds the test wants.
func cache(t *testing.T, builds ...string) (driver, browsers string) {
	t.Helper()
	root := t.TempDir()
	driver = filepath.Join(root, "driver")
	browsers = filepath.Join(root, "browsers")

	if err := os.MkdirAll(filepath.Join(driver, "package"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(driver, "package", "cli.js"), []byte("//"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(browsers, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, b := range builds {
		dir := filepath.Join(browsers, b)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		// A chromium build only counts when it holds a real executable —
		// hasChromium used to match the DIRECTORY name, which is how a
		// half-extracted cache could report a browser that could not launch.
		if strings.HasPrefix(b, "chromium") {
			name := "chrome"
			if runtime.GOOS == "windows" {
				name = "chrome.exe"
			}
			if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	return driver, browsers
}

// bareHost describes a machine with no browser of its own, so a test about the
// managed cache is not decided by whatever is installed on the machine running
// it.
func bareHost() resolveHost {
	return resolveHost{
		GOOS:     runtime.GOOS,
		LookPath: func(string) (string, error) { return "", os.ErrNotExist },
		Stat:     func(string) (os.FileInfo, error) { return nil, os.ErrNotExist },
		Getenv:   func(string) string { return "" },
	}
}

// hostWith describes a machine where exactly the named paths exist.
func hostWith(goos string, env map[string]string, present ...string) resolveHost {
	set := map[string]bool{}
	for _, p := range present {
		set[p] = true
	}
	return resolveHost{
		GOOS:     goos,
		LookPath: func(string) (string, error) { return "", os.ErrNotExist },
		Stat: func(p string) (os.FileInfo, error) {
			if !set[p] {
				return nil, os.ErrNotExist
			}
			return os.Stat(os.DevNull)
		},
		Getenv: func(k string) string { return env[k] },
	}
}

// The driver is a FLOOR, not an alternative: Playwright drives even a system
// Chrome through its own Node driver. Reporting "no browser" when the driver is
// what is missing would send the operator looking in the wrong place.
func TestADriverlessInstallSaysSoEvenWithABrowserPresent(t *testing.T) {
	root := t.TempDir()
	browsers := filepath.Join(root, "browsers")
	if err := os.MkdirAll(browsers, 0o755); err != nil {
		t.Fatal(err)
	}

	got := resolveWith(filepath.Join(root, "driver"), browsers,
		hostWith("linux", nil, "/opt/google/chrome/chrome"))

	if got.OK {
		t.Fatal("resolved a browser with no driver installed")
	}
	if !strings.Contains(got.Reason, "driver") {
		t.Errorf("reason should name the driver, got %q", got.Reason)
	}
}

func TestAManagedChromiumIsUsed(t *testing.T) {
	driver, browsers := cache(t, "chromium-1234")

	got := resolveWith(driver, browsers, bareHost())

	if !got.OK || got.Engine != EngineChromium || got.Source != SourceManaged {
		t.Fatalf("got %+v", got)
	}
}

// The case the whole change exists for: an owner who already has Chrome is not
// asked to download one.
func TestAnInstalledChromeIsUsedInsteadOfDownloading(t *testing.T) {
	driver, browsers := cache(t) // driver only, no browser build

	got := resolveWith(driver, browsers, hostWith("linux", nil, "/opt/google/chrome/chrome"))

	if !got.OK {
		t.Fatalf("Chrome is installed and was not used: %s", got.Reason)
	}
	if got.Source != SourceChannel || got.Channel != "chrome" {
		t.Fatalf("got %+v, want a chrome channel", got)
	}
}

func TestEdgeIsUsedWhenChromeIsAbsent(t *testing.T) {
	driver, browsers := cache(t)

	got := resolveWith(driver, browsers, hostWith("linux", nil, "/opt/microsoft/msedge/msedge"))

	if got.Channel != "msedge" {
		t.Fatalf("got %+v, want the msedge channel", got)
	}
}

// Windows installs land in one of three directories, and Chrome's per-user
// install under LOCALAPPDATA needs no administrator, so it is common on managed
// machines — missing it would be missing the likeliest case.
func TestWindowsBrowsersAreFoundInEachProgramDirectory(t *testing.T) {
	driver, browsers := cache(t)

	for _, tc := range []struct{ key, base string }{
		{"ProgramFiles", `C:\Program Files`},
		{"ProgramFiles(x86)", `C:\Program Files (x86)`},
		{"LOCALAPPDATA", `C:\Users\o\AppData\Local`},
	} {
		env := map[string]string{tc.key: tc.base}
		path := filepath.Join(tc.base, "Google", "Chrome", "Application", "chrome.exe")

		got := resolveWith(driver, browsers, hostWith("windows", env, path))

		if got.Channel != "chrome" {
			t.Errorf("Chrome under %s was not found: %+v", tc.key, got)
		}
	}
}

// "Detect other Playwright browsers too": an owner with a managed Firefox is
// not asked to download Chromium.
func TestAManagedFirefoxIsUsedWhenThereIsNoChromium(t *testing.T) {
	driver, browsers := cache(t, "firefox-1234")

	got := resolveWith(driver, browsers, bareHost())

	if !got.OK || got.Engine != EngineFirefox {
		t.Fatalf("got %+v, want a managed firefox", got)
	}
}

// A managed Chromium wins, because it is what the loopback hardening was
// verified against and the one engine internal/export can also use for PDF.
func TestAManagedChromiumIsPreferredOverEverythingElse(t *testing.T) {
	driver, browsers := cache(t, "chromium-1234", "firefox-1234")

	got := resolveWith(driver, browsers, hostWith("linux", nil, "/opt/google/chrome/chrome"))

	if got.Source != SourceManaged || got.Engine != EngineChromium {
		t.Fatalf("got %+v", got)
	}
}

// WebKit is detected and REFUSED with a reason, rather than either launched or
// silently ignored.
//
// Every engine needs its own setting to stop it bypassing the guarded proxy for
// localhost, where this install's own bridges listen. Chromium has an argument
// and Firefox a preference; WebKit has no equivalent that could be verified
// here, and the test that asserts the behaviour rather than the flag is behind
// a build tag CI does not run. Launching it would be asserting a security
// property nothing has checked; ignoring it would leave the operator reading
// "no browser is installed" with a browser plainly in the cache.
func TestAWebKitOnlyCacheIsRefusedByName(t *testing.T) {
	driver, browsers := cache(t, "webkit-1234")

	got := resolveWith(driver, browsers, bareHost())

	if got.OK {
		t.Fatal("WebKit was launched despite its loopback guard being unverified")
	}
	if !strings.Contains(got.Reason, "WebKit") {
		t.Errorf("the reason must name what IS installed, got %q", got.Reason)
	}
	if !strings.Contains(got.Reason, "browser install") {
		t.Errorf("the reason must name the command that fixes it, got %q", got.Reason)
	}
}

func TestAnEmptyCacheAsksForAnInstall(t *testing.T) {
	driver, browsers := cache(t)

	got := resolveWith(driver, browsers, bareHost())

	if got.OK {
		t.Fatal("resolved a browser from an empty cache")
	}
	if !strings.Contains(got.Reason, "rookery browser install") {
		t.Errorf("reason must name the command, got %q", got.Reason)
	}
}

// Probe answers from Resolve, so OK has to mean "something can be launched".
// It used to mean "a chromium- DIRECTORY exists", which a half-extracted cache
// satisfies while every render fails.
func TestProbeRequiresAnExecutableNotJustADirectory(t *testing.T) {
	root := t.TempDir()
	driver := filepath.Join(root, "driver")
	browsers := filepath.Join(root, "browsers")
	if err := os.MkdirAll(filepath.Join(driver, "package"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(driver, "package", "cli.js"), []byte("//"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A build directory with no binary in it.
	if err := os.MkdirAll(filepath.Join(browsers, "chromium-1234"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := resolveWith(driver, browsers, bareHost()); got.OK {
		t.Error("an empty chromium- directory was reported as a usable browser")
	}
}

// The offer has to say what it will actually download. Quoting ~200 MB to
// someone who already has Chrome — and will only fetch the driver — is the kind
// of small dishonesty that makes people decline.
func TestDescribeNamesWhatWillBeUsed(t *testing.T) {
	if got := (Choice{}).Describe(); got != "none" {
		t.Errorf("got %q", got)
	}
	got := Choice{OK: true, Engine: EngineChromium, Source: SourceChannel, Channel: "msedge"}.Describe()
	if !strings.Contains(got, "Microsoft Edge") {
		t.Errorf("a channel should be named for a human, got %q", got)
	}
	managed := Choice{OK: true, Engine: EngineFirefox, Source: SourceManaged}.Describe()
	if !strings.Contains(managed, "firefox") {
		t.Errorf("got %q", managed)
	}
}
