package browser

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Rookery used to launch exactly one thing: Playwright's own managed Chromium.
// So an owner who already had Chrome or Edge — or a Playwright Firefox — was
// offered a several-hundred-megabyte download for a capability their machine
// could already provide, and `rookery onboard` offered it again on every run.
//
// This file answers "what will actually be launched", once, for every caller.

// Engine is the browser family being driven. Playwright's Firefox and WebKit
// are patched builds, so only Chromium has system installs worth using.
type Engine string

const (
	EngineChromium Engine = "chromium"
	EngineFirefox  Engine = "firefox"
)

// Source says where the browser came from, which is what decides whether an
// install still has to download one.
type Source string

const (
	// SourceManaged is a build Playwright installed into its own cache.
	SourceManaged Source = "managed"
	// SourceChannel is a browser the operator already had, driven in place.
	SourceChannel Source = "channel"
)

// Choice is the resolved browser.
type Choice struct {
	OK      bool
	Engine  Engine
	Source  Source
	Channel string // "chrome" or "msedge" when Source is SourceChannel
	// Reason is empty when OK, and otherwise says what is missing in the
	// operator's own terms, naming the command that fixes it.
	Reason string
}

// Describe names the choice for a human.
func (c Choice) Describe() string {
	if !c.OK {
		return "none"
	}
	if c.Source == SourceChannel {
		return "your installed " + channelLabel(c.Channel)
	}
	return "the bundled " + string(c.Engine)
}

func channelLabel(channel string) string {
	switch channel {
	case "chrome":
		return "Google Chrome"
	case "msedge":
		return "Microsoft Edge"
	}
	return channel
}

// resolveHost is the machine being described, injected so the per-platform
// search can be tested on a host that is not the one being described — the same
// reason onboard.Host and coder.detectHost exist.
type resolveHost struct {
	GOOS     string
	LookPath func(string) (string, error)
	Stat     func(string) (os.FileInfo, error)
	Getenv   func(string) string
}

func currentResolveHost() resolveHost {
	return resolveHost{
		GOOS:     runtime.GOOS,
		LookPath: exec.LookPath,
		Stat:     os.Stat,
		Getenv:   os.Getenv,
	}
}

// Resolve reports the browser this host will drive.
func Resolve() Choice {
	return resolveWith(driverDir(), browsersDir(), currentResolveHost())
}

// resolveWith is the testable half.
//
// The driver is checked first and separately because it is a FLOOR, not an
// alternative: Playwright drives even a system Chrome through its own Node
// driver, so `playwright.Run` fails without it whatever browsers are installed.
// Reporting "no browser" when the real problem is a missing driver would send
// the operator looking in the wrong place.
func resolveWith(driver, browsers string, h resolveHost) Choice {
	if driver == "" || browsers == "" {
		return Choice{Reason: "could not determine the browser cache directory for this platform"}
	}
	if _, err := os.Stat(filepath.Join(driver, "package", "cli.js")); err != nil {
		return Choice{Reason: "the Playwright driver is not installed — run `rookery browser install`"}
	}

	// A managed Chromium first: it is what every existing install has, it is
	// what the loopback-proxy hardening was verified against, and it is the one
	// engine internal/export can also use for PDF.
	if chromiumExecutableIn(browsers) != "" {
		return Choice{OK: true, Engine: EngineChromium, Source: SourceManaged}
	}
	// Then a browser the operator already has. This is the case that removes
	// the download entirely.
	if channel, _ := systemChannel(h); channel != "" {
		return Choice{OK: true, Engine: EngineChromium, Source: SourceChannel, Channel: channel}
	}
	if hasBuild(browsers, "firefox-") {
		return Choice{OK: true, Engine: EngineFirefox, Source: SourceManaged}
	}

	// WebKit is deliberately NOT resolved, and saying so beats silence.
	//
	// The browser is routed through a local proxy that blocks this install's own
	// loopback bridges, and each engine needs its own setting to stop it
	// bypassing that proxy for localhost: an argument for Chromium, a preference
	// for Firefox. WebKit has no equivalent that could be verified here, and the
	// one test that asserts the behaviour rather than the flag is behind a build
	// tag that CI does not run. Launching it anyway would be asserting a
	// security property nothing has checked.
	if hasBuild(browsers, "webkit-") {
		return Choice{Reason: "only a Playwright WebKit build is installed, which Rookery does not drive — " +
			"run `rookery browser install` to add Chromium"}
	}
	return Choice{Reason: "no browser is installed — run `rookery browser install`"}
}

// hasBuild reports whether the cache holds a build with the given prefix.
// Playwright names these <product>-<revision>, and the revision moves with
// every upgrade, so matching the prefix is the only stable test.
func hasBuild(browsers, prefix string) bool {
	entries, err := os.ReadDir(browsers)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
			return true
		}
	}
	return false
}

// systemChannel finds a Chromium-family browser the operator already installed,
// returning Playwright's channel name and the executable path.
//
// Chrome is preferred over Edge only because it is the more common deliberate
// install; either drives identically.
func systemChannel(h resolveHost) (channel, path string) {
	for _, c := range []struct {
		name  string
		bins  []string
		paths []string
	}{
		{name: "chrome", bins: chromeBins(h.GOOS), paths: chromePaths(h)},
		{name: "msedge", bins: edgeBins(h.GOOS), paths: edgePaths(h)},
	} {
		if p := firstExisting(h, c.bins, c.paths); p != "" {
			return c.name, p
		}
	}
	return "", ""
}

func firstExisting(h resolveHost, bins, paths []string) string {
	if h.LookPath != nil {
		for _, b := range bins {
			if p, err := h.LookPath(b); err == nil {
				return p
			}
		}
	}
	if h.Stat != nil {
		for _, p := range paths {
			if fi, err := h.Stat(p); err == nil && !fi.IsDir() {
				return p
			}
		}
	}
	return ""
}

func chromeBins(goos string) []string {
	if goos == "windows" {
		return nil // resolved by path; chrome.exe is not normally on PATH
	}
	return []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"}
}

func edgeBins(goos string) []string {
	if goos == "windows" {
		return nil
	}
	return []string{"microsoft-edge", "microsoft-edge-stable"}
}

func chromePaths(h resolveHost) []string {
	switch h.GOOS {
	case "windows":
		return windowsAppPaths(h, filepath.Join("Google", "Chrome", "Application", "chrome.exe"))
	case "darwin":
		return []string{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}
	}
	return []string{"/opt/google/chrome/chrome"}
}

func edgePaths(h resolveHost) []string {
	switch h.GOOS {
	case "windows":
		return windowsAppPaths(h, filepath.Join("Microsoft", "Edge", "Application", "msedge.exe"))
	case "darwin":
		return []string{"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge"}
	}
	return []string{"/opt/microsoft/msedge/msedge"}
}

// windowsAppPaths expands a relative install path against the three program
// directories a browser can land in.
//
// Both 32- and 64-bit locations are searched because Chrome and Edge have
// shipped in each at different times, and LOCALAPPDATA covers Chrome's
// per-user install, which needs no administrator and is therefore common on
// managed machines.
func windowsAppPaths(h resolveHost, rel string) []string {
	if h.Getenv == nil {
		return nil
	}
	var out []string
	for _, key := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"} {
		if base := h.Getenv(key); base != "" {
			out = append(out, filepath.Join(base, rel))
		}
	}
	return out
}

// SystemChromiumExecutable returns a Chromium-family browser the operator
// already has, or "" when there is none.
//
// internal/export needs a real Chromium BINARY — it shells out directly rather
// than going through Playwright — so without this, an install whose only
// browser is a system Chrome would render pages perfectly well and still report
// PDF export as unavailable. ChromiumExecutable falls back to this for exactly
// that reason.
func SystemChromiumExecutable() string {
	_, path := systemChannel(currentResolveHost())
	return path
}

// A managedBuilds helper was written here and removed before merge: its comment
// claimed `rookery browser status` used it, and nothing did. Go does not flag an
// unused package-level function, so the build stayed green while the comment was
// simply false.
