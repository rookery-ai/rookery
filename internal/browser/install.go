package browser

import (
	"fmt"
	"io"
	"runtime"

	"github.com/mxschmitt/playwright-go"
)

// Install fetches the browser runtime: the Node driver, the playwright-core
// package, and a Chromium build.
//
// Only Chromium is installed. Playwright's default is all three engines, which
// would pull roughly a gigabyte for two browsers nothing here ever launches.
func Install(out io.Writer, withDeps bool) error {
	opts := &playwright.RunOptions{
		Browsers: []string{"chromium"},
		WithDeps: withDeps,
		Stdout:   out,
		Stderr:   out,
		Verbose:  true,
	}
	if err := playwright.Install(opts); err != nil {
		return fmt.Errorf("install browser runtime: %w", err)
	}
	return nil
}

// SystemDepsHint describes the shared libraries Chromium needs, in the form of
// a command the operator can run.
//
// This is a HINT rather than something Rookery runs, because installing system
// packages needs root and the server does not have it (and should not). The
// alternative — Playwright's own `--with-deps` — also needs root and fails with
// a message about sudo that does not name the packages, so an operator who
// cannot use it is left with nothing to act on.
func SystemDepsHint(manager string) string {
	if runtime.GOOS != "linux" {
		return ""
	}
	switch manager {
	case "dnf":
		return "sudo dnf install -y nss atk at-spi2-atk cups-libs libdrm libXcomposite libXdamage libXfixes libXrandr mesa-libgbm alsa-lib pango"
	case "apt":
		return "sudo apt-get install -y libnss3 libatk1.0-0 libatk-bridge2.0-0 libcups2 libdrm2 libxcomposite1 libxdamage1 libxfixes3 libxrandr2 libgbm1 libasound2 libpango-1.0-0"
	case "zypper":
		return "sudo zypper install -y mozilla-nss libatk-1_0-0 libatk-bridge-2_0-0 libcups2 libdrm2 libXcomposite1 libXdamage1 libXfixes3 libXrandr2 libgbm1 libasound2 pango"
	case "pacman":
		return "sudo pacman -S --needed nss atk at-spi2-atk libcups libdrm libxcomposite libxdamage libxfixes libxrandr mesa alsa-lib pango"
	}
	return ""
}
