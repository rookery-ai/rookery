package browser

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// An upgrade can take the browser away without anything failing.
//
// The driver directory is named after the Playwright version compiled into the
// binary (driverDir), and the Chromium build after the revision that driver
// launches. A release that bumps playwright-go therefore looks in a directory
// the owner never populated: Resolve reports the runtime missing, the browser
// tools stop being offered, and the agents that relied on them fall back to
// worse routes or fail — with no error anywhere, because "no browser" is a
// legitimate state for most installs.
//
// Staleness tells the two apart. It is only reported when there is evidence the
// owner installed a browser before (an older driver directory, or a Playwright
// build in the cache) AND this build cannot launch one now. An install that
// never opted in is not outdated; saying it was would put a warning on every
// default install.

// Staleness is the answer to "was the browser installed for a different build?".
type Staleness struct {
	Outdated bool
	// Reason is empty unless Outdated, and names the fix.
	Reason string
	// Needed is the driver version this build needs, so a notice can be sent
	// once per stranding rather than on every restart.
	Needed string
}

// CheckRuntime reports whether this host's browser runtime was installed for a
// different Rookery build. Side-effect free, like Probe.
func CheckRuntime() Staleness {
	// An operator pointing at their own driver manages its version themselves.
	if os.Getenv("PLAYWRIGHT_DRIVER_PATH") != "" {
		return Staleness{}
	}
	cache := cacheDir()
	if cache == "" {
		return Staleness{}
	}
	return staleWith(filepath.Join(cache, "ms-playwright-go"), probeVersion(), browsersDir(), currentResolveHost())
}

// staleWith is the testable half: driverRoot holds one directory per driver
// version, and version is the one this build needs.
func staleWith(driverRoot, version, browsers string, h resolveHost) Staleness {
	if version == "" || browsers == "" {
		// The probe failed; "outdated" on no evidence would send the owner off
		// to reinstall for nothing.
		return Staleness{}
	}
	// Evidence of a previous install is Rookery's OWN driver directory, at any
	// version including this one. ~/.cache/ms-playwright is shared by every
	// Playwright binding, so a developer's Python or Node test project fills it
	// too — builds there say nothing about whether this owner opted in.
	prior := priorDrivers(driverRoot, version)
	_, curErr := os.Stat(filepath.Join(driverRoot, version, "package", "cli.js"))
	if len(prior) == 0 && curErr != nil {
		return Staleness{}
	}
	if resolveWith(filepath.Join(driverRoot, version), browsers, h).OK {
		return Staleness{}
	}
	reason := "the browser on this machine was installed for an earlier Rookery build and does not match this one"
	if len(prior) > 0 {
		reason = fmt.Sprintf("the browser on this machine was installed for Playwright %s, but this Rookery build needs %s",
			strings.Join(prior, ", "), version)
	}
	return Staleness{Outdated: true, Reason: reason + " — run `rookery browser install`", Needed: version}
}

// priorDrivers lists the driver versions installed beside the current one.
func priorDrivers(driverRoot, version string) []string {
	entries, err := os.ReadDir(driverRoot)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || e.Name() == version {
			continue
		}
		if _, err := os.Stat(filepath.Join(driverRoot, e.Name(), "package", "cli.js")); err == nil {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// expectedChromiumBuild reads, from the driver's own browsers.json, the build
// directory it launches — "chromium_headless_shell-<rev>", or "chromium-<rev>"
// for a driver that predates the headless shell — or "" when it cannot be read.
//
// The headless shell is preferred: Playwright launches it for headless
// Chromium, which is the only way Rookery launches it.
func expectedChromiumBuild(driver string) string {
	data, err := os.ReadFile(filepath.Join(driver, "package", "browsers.json"))
	if err != nil {
		return ""
	}
	var doc struct {
		Browsers []struct {
			Name     string `json:"name"`
			Revision string `json:"revision"`
		} `json:"browsers"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return ""
	}
	var full string
	for _, b := range doc.Browsers {
		switch b.Name {
		case "chromium-headless-shell":
			if b.Revision != "" {
				return "chromium_headless_shell-" + b.Revision
			}
		case "chromium":
			if b.Revision != "" {
				full = "chromium-" + b.Revision
			}
		}
	}
	return full
}

// managedChromiumReady reports whether the cache holds the Chromium build the
// driver will actually launch.
//
// Any chromium-* directory used to count, and Playwright leaves old builds in
// place after an upgrade — so a stale build read as a working browser, was
// preferred over the operator's own Chrome, and every launch failed looking for
// a revision that had never been downloaded. When the driver's browsers.json
// cannot be read the old rule applies, since there is nothing better to go on.
func managedChromiumReady(driver, browsers string) bool {
	build := expectedChromiumBuild(driver)
	if build == "" {
		return chromiumExecutableIn(browsers) != ""
	}
	return findChromiumBinary(filepath.Join(browsers, build)) != ""
}
