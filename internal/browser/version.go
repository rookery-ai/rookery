package browser

import "github.com/mxschmitt/playwright-go"

// probeVersion returns the Playwright CLI version this build pins.
//
// It is read from the library rather than copied into a constant here, because
// the driver directory is named after it: a hardcoded copy would keep compiling
// after a dependency bump and quietly report a perfectly good install as
// missing, since Probe would be looking in a directory the new version never
// created.
//
// NewDriver has no side effects (it fills defaults and returns) — unlike the
// isUpToDateDriver path, which is why Probe does not use that. A failure here
// yields "" and Probe then reports the runtime as absent, which is the correct
// answer for a host whose home directory cannot be resolved.
func probeVersion() string {
	d, err := playwright.NewDriver(&playwright.RunOptions{})
	if err != nil || d == nil {
		return ""
	}
	return d.Version
}
