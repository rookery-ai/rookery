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
	// Silent when anything usable is present — including a Chrome or Edge the
	// owner already had, which is now resolved rather than ignored. Being
	// offered a several-hundred-megabyte download for a capability the machine
	// already provides is exactly the "asking for reinstall" this setup is
	// being cleaned up to stop.
	if browser.Probe().OK {
		return
	}

	o.step("Browser (optional)")
	o.info("Lets agents and chat read pages that only appear once JavaScript has run,")
	o.info("and lets an agent sign in, fill forms and click through a flow for you.")
	o.info("Without it those pages come back empty; everything else works normally.")
	// The size is asked for rather than stated, because it depends on what is
	// already here: a host with Chrome installed needs the Playwright driver
	// alone, not a second browser. Quoting the larger figure to someone who
	// will not pay it is the kind of small inaccuracy that makes people decline.
	o.info("%s to download, once.", browser.InstallSize())

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

// The download size now comes from browser.InstallSize, because it is not one
// number: it is the Node driver (~70 MB) plus, only when no browser is already
// present, a Chromium build (~115 MiB). The old constant stated the total
// unconditionally, which was wrong for every host that already had Chrome.
