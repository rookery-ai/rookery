package main

import (
	"os"
	"runtime"

	"github.com/rookery-ai/rookery/internal/browser"
	"github.com/rookery-ai/rookery/internal/onboard"
)

// stepBrowser offers the headless browser, and says nothing at all when it is
// already there.
//
// The silence is deliberate and differs from stepHostTools, which reports "all
// present". This is a several-hundred-megabyte optional download that most
// installs will never have; an owner who already has it does not need a line
// about it every time they run setup, and setup output is only read while it is
// short enough to read.
//
// It is offered rather than assumed for the same reason it is not an
// onboard.HostTool: that type probes a binary on PATH, this is a cache
// directory, and the "four host tools" count is asserted across four delivery
// surfaces.
func (o *onboarder) stepBrowser() {
	if browser.Probe().OK {
		return // already installed — nothing worth saying
	}

	o.step("Browser (optional)")
	o.info("Lets agents and chat read pages that only appear once JavaScript has run,")
	o.info("and lets an agent sign in, fill forms and click through a flow for you.")
	o.info("Without it those pages come back empty; everything else works normally.")
	o.info("About %s to download, once.", browserDownloadSize)

	// The install itself is a Go call into the Playwright driver, which fetches
	// the right Node build and Chromium for whatever platform this is — so this
	// offer works identically on Linux, macOS and Windows, unlike the host-tool
	// step, which needs a package manager it may not find.
	if !o.ask("Install it now?") {
		o.later("install the browser with `rookery browser install`")
		// Printed even when declined, because --non-interactive never asks and
		// the whole point of that mode is to report what to do.
		o.info("Later: rookery browser install")
		if hint := browser.SystemDepsHint(string(onboard.DetectManager(nil))); hint != "" {
			o.info("Then, for the libraries Chromium needs: %s", hint)
		}
		return
	}

	if err := browser.Install(os.Stdout, false); err != nil {
		o.info("failed: %v", err)
		o.later("install the browser with `rookery browser install`")
		return
	}
	if av := browser.Probe(); !av.OK {
		// The download reported success but the probe disagrees. Say so rather
		// than claim an install that later reads as missing.
		o.info("finished, but the runtime still looks incomplete: %s", av.Reason)
		o.later("finish installing the browser with `rookery browser install`")
		return
	}
	o.ok("installed")

	// Chromium needs shared libraries that only Linux distributions leave out;
	// macOS and Windows ship them, which is why the hint is empty there rather
	// than a platform branch here.
	if hint := browser.SystemDepsHint(string(onboard.DetectManager(nil))); hint != "" {
		o.info("Chromium also needs some system libraries. If pages fail to render, run:")
		o.info("%s", hint)
	} else if runtime.GOOS == "linux" {
		// A Linux host whose package manager was not recognised still needs the
		// libraries; naming them beats silence.
		o.info("Chromium needs nss, atk, cups, libdrm, libXcomposite, libXrandr, gbm,")
		o.info("alsa and pango. Install them with your distribution's own tools.")
	}
}

// browserDownloadSize is stated as a range because it is two downloads whose
// sizes move with upstream releases: the Node driver plus a Chromium build,
// measured at ~70 MB and ~115 MiB respectively when this was written. A precise
// number here would be wrong within a release or two and is not worth pinning —
// the point is to tell someone on a metered connection roughly what they are
// agreeing to.
const browserDownloadSize = "200 MB"
