// Package browser renders web pages in a real, confined browser so the platform
// can read content that only exists after JavaScript runs.
//
// It is a peer of internal/connectors and internal/mcp and deliberately mirrors
// their shape: one typed choke point (Render/Act), one availability probe, and
// one loopback Bridge so a CLI coder reaches exactly the same code an API-engine
// tool call reaches. Divergence between those two paths is the failure
// coder.ChatAllowedTools' doc comment was written about.
//
// Two properties are load-bearing and are enforced here rather than by prompt:
//
//   - Chromium runs in a SANDBOXED HELPER PROCESS (the hidden __browser-host
//     subcommand under sandbox.Wrap), never in the host process. The host process
//     holds the database, the system key and every decrypted secret; a browser
//     rendering untrusted third-party content must not share that address space
//     or that filesystem view. CLAUDE.md defers MCP stdio transport for precisely
//     this reason, and the playwright-browser core skill this replaces already ran
//     Chromium inside the coder's own sandbox — so an in-process browser would
//     have been a regression, not a new risk.
//
//   - Every byte the browser fetches dials through nethttp's guard, via a local
//     CONNECT proxy the helper owns. Chromium resolves DNS itself, so a
//     net.Dialer Control hook cannot reach it and URL inspection would miss
//     redirects and subresources. The proxy is the only interception point that
//     sees all three.
package browser

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ErrUnavailable means the browser runtime is not installed on this host. It is
// a named error rather than a failed spawn for the same reason
// coder.ErrLocalCoderDisabled is: the fix is a command the operator can run, and
// an exec failure deep inside a tool call does not tell them that.
var ErrUnavailable = errors.New("browser runtime not installed")

// ErrActingDisabled means an acting call was made in a context that may only
// read — a build or dry run, a chat turn, or an agent with no acting grant.
var ErrActingDisabled = errors.New("browser acting not permitted here")

// Request is one page render.
type Request struct {
	URL string
	// WaitFor is "", "load", "domcontentloaded", "networkidle", "selector:<css>"
	// or "text:<substring>". Empty means domcontentloaded plus a short settle,
	// which is what a content read almost always wants.
	WaitFor   string
	TimeoutMS int
	// Offset/Limit page through the extracted text, mirroring read_file. A
	// rendered article routinely exceeds the per-tool-result cap, and paging is
	// how read_file already solved that.
	Offset int
	Limit  int
	// Session, when set, keeps the browser context alive under this key so a
	// later call continues in the same page. Empty means an ephemeral context
	// that is torn down when the call returns.
	Session string
	// WantHTML asks for the rendered DOM alongside the text. Only the search
	// cascade sets it; a model is never given markup.
	WantHTML bool
}

// Result is what a render produced.
type Result struct {
	Text       string
	Title      string
	FinalURL   string
	Status     int
	Truncated  bool
	NextOffset int
	// Blocked names a bot wall when one was detected: "cloudflare", "captcha" or
	// "login". Empty means the page was read normally. It is REPORTED, never
	// worked around — bypassing bot protection is a stated non-goal, and a
	// retry loop against an interstitial burns a run's whole turn budget for
	// nothing.
	Blocked     string
	BlockedNote string
	// Elements is the interactive-element list used by the acting tools. It is
	// empty for a plain read.
	Elements []Element
}

// Element is one interactive control, addressed by the ref Playwright's
// aria-ref selector engine resolves. The model never writes a CSS selector —
// that is the single property that decides whether a weak model can drive a
// page at all.
type Element struct {
	Ref  string `json:"ref"`
	Role string `json:"role"`
	Name string `json:"name"`
	// Note carries a short state hint ("empty", "checked", "disabled") when the
	// role has one worth showing.
	Note string `json:"note,omitempty"`
}

// Availability reports whether the runtime is present, and if not, what to do
// about it in the operator's own terms.
type Availability struct {
	OK bool
	// Reason is empty when OK. Otherwise it names the missing half and the
	// command that installs it.
	Reason string
}

// driverVersion is the Playwright CLI version playwright-go pins. It is read
// from the library at run time (PlaywrightDriver.Version) rather than copied,
// so a dependency bump cannot leave this probe looking in a stale directory.
// See probeVersion.

// Probe reports whether the browser runtime is installed, WITHOUT side effects.
//
// It deliberately does not call playwright-go's own isUpToDateDriver: that
// helper is unexported, it runs the driver to read its version, and — the
// disqualifying part — it MkdirAlls the driver directory. A probe that creates
// the thing it is probing for cannot be called from /healthz.
func Probe() Availability { return probeAt(driverDir(), browsersDir()) }

func probeAt(driver, browsers string) Availability {
	if driver == "" || browsers == "" {
		return Availability{Reason: "could not determine the browser cache directory for this platform"}
	}
	if _, err := os.Stat(filepath.Join(driver, "package", "cli.js")); err != nil {
		return Availability{Reason: "the Playwright driver is not installed — run `rookery browser install`"}
	}
	if !hasChromium(browsers) {
		return Availability{Reason: "no Chromium build is installed — run `rookery browser install`"}
	}
	return Availability{OK: true}
}

// hasChromium reports whether any chromium build is present. It matches on the
// directory PREFIX rather than an exact revision because Playwright names these
// chromium-<rev> / chromium_headless_shell-<rev> and the revision changes with
// every upgrade; pinning one would report a working install as broken the first
// time the dependency moved.
func hasChromium(browsers string) bool {
	entries, err := os.ReadDir(browsers)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), "chromium-") || strings.HasPrefix(e.Name(), "chromium_headless_shell-") {
			return true
		}
	}
	return false
}

// driverDir resolves where playwright-go keeps node + cli.js, honouring the two
// environment overrides the library itself honours.
func driverDir() string {
	if p := os.Getenv("PLAYWRIGHT_DRIVER_PATH"); p != "" {
		return p
	}
	cache := cacheDir()
	if cache == "" {
		return ""
	}
	return filepath.Join(cache, "ms-playwright-go", probeVersion())
}

// browsersDir resolves where Playwright keeps browser builds.
func browsersDir() string {
	if p := os.Getenv("PLAYWRIGHT_BROWSERS_PATH"); p != "" {
		return p
	}
	cache := cacheDir()
	if cache == "" {
		return ""
	}
	return filepath.Join(cache, "ms-playwright")
}

// cacheDir mirrors playwright-go's getDefaultCacheDirectory, which is
// unexported. Duplicated deliberately and kept to four lines: the alternative is
// a probe with side effects (see Probe).
func cacheDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(home, "AppData", "Local")
	case "darwin":
		return filepath.Join(home, "Library", "Caches")
	case "linux":
		return filepath.Join(home, ".cache")
	}
	return ""
}

// Renderer is the narrow interface consumers depend on, so internal/coder and
// the designers can be tested without spawning a browser. *Manager implements
// it. Mirrors mcp.Caller for the same reason.
type Renderer interface {
	Available() Availability
	Render(ctx context.Context, req Request) (Result, error)
	Act(ctx context.Context, req ActRequest) (Result, error)
	CloseSession(ctx context.Context, session string)
}
