package browser

import (
	"fmt"

	"github.com/mxschmitt/playwright-go"
)

// Every engine is routed through the guarded local proxy, and every engine
// needs its OWN setting to stop it bypassing that proxy for localhost.
//
// This is a security property, not a nicety: the loopback interface hosts this
// install's connector, KB and MCP bridges. A page that reached them directly
// would be talking to the same origin the agent's own tools use. The bridges
// require a bearer token, so this is defence in depth rather than the only
// control — but it is the control this file exists to preserve, and the setting
// differs per engine, which is exactly the kind of thing that silently
// disappears when a second engine is added.
//
// The one test that asserts the BEHAVIOUR (loopback refused) rather than the
// flag sits behind the `browser` build tag and does not run in CI, so an engine
// whose setting cannot be applied is refused rather than launched.

func launchResolved(pw *playwright.Playwright, c Choice, proxyAddr string) (playwright.Browser, error) {
	proxy := &playwright.Proxy{Server: "http://" + proxyAddr}

	switch c.Engine {
	case EngineChromium:
		opts := playwright.BrowserTypeLaunchOptions{
			Headless: playwright.Bool(true),
			Proxy:    proxy,
			Args: []string{
				// Chromium bypasses the proxy for localhost by DEFAULT.
				// Playwright already compensates today — measured — so this is
				// currently redundant. It is set anyway, because relying on an
				// undocumented default for a security property is how that
				// property disappears in a dependency bump.
				"--proxy-bypass-list=<-loopback>",
				// Chromium's /dev/shm usage exceeds the default size in many
				// containers; without this it crashes on content-heavy pages.
				"--disable-dev-shm-usage",
			},
		}
		// A system Chrome or Edge is driven in place through its channel.
		// Channel is used rather than ExecutablePath because playwright-go's own
		// documentation supports the first and warns "use at your own risk"
		// about the second.
		if c.Source == SourceChannel && c.Channel != "" {
			opts.Channel = playwright.String(c.Channel)
		}
		return pw.Chromium.Launch(opts)

	case EngineFirefox:
		return pw.Firefox.Launch(playwright.BrowserTypeLaunchOptions{
			Headless: playwright.Bool(true),
			Proxy:    proxy,
			// Firefox's equivalent of --proxy-bypass-list=<-loopback>. Without
			// it Firefox resolves localhost directly and the proxy never sees
			// the request, which is the whole guard defeated.
			FirefoxUserPrefs: map[string]any{
				"network.proxy.allow_hijacking_localhost": true,
			},
		})
	}

	return nil, fmt.Errorf("unsupported engine %q", c.Engine)
}
