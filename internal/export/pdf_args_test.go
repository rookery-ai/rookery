package export

import (
	"strings"
	"testing"
)

// Every Chromium-family engine must carry the container flags.
//
// This looks like belt and braces and is not. Each of these was added because
// its absence produced a failure that named something else entirely:
//
//   - --disable-dev-shm-usage: a CONTAINER's /dev/shm defaults to 64 MB, which a
//     full Chromium exceeds, and it then crashes on content-heavy pages. Rookery
//     ships a container image, so this is a real target. (It is not what fixes
//     the hang findPDFEngine addresses — a VM runner sizes /dev/shm from RAM, so
//     the flag is inert there.)
//   - --no-sandbox: Chromium refuses to start as root without a usable sandbox
//     namespace, which is the normal case inside a container.
//   - --no-pdf-header-footer: without it every exported page is stamped with the
//     print date and the source file:// temp path.
//
// A flag whose absence produces a hang rather than an error is exactly the kind
// a later reader deletes as redundant.
func TestChromiumArgsCarryTheContainerFlags(t *testing.T) {
	_, args := chromiumArgs("/usr/bin/chromium", "/tmp/in.html", "/tmp/out.pdf")
	got := strings.Join(args, " ")

	for _, want := range []struct{ flag, because string }{
		{"--disable-dev-shm-usage", "a 64 MB /dev/shm makes Chromium hang until the timeout kills it"},
		{"--no-sandbox", "Chromium will not start as root without a usable sandbox namespace"},
		{"--disable-gpu", "there is no GPU in a headless container"},
		{"--no-pdf-header-footer", "otherwise every page is stamped with the print date and a temp path"},
		{"--headless", "there is no display"},
	} {
		if !strings.Contains(got, want.flag) {
			t.Errorf("missing %s — %s", want.flag, want.because)
		}
	}
}

// The output path and the input must both reach the command, and the input has
// to come last: Chromium treats the final positional argument as the URL to
// print.
func TestChromiumArgsNameTheInputAndOutput(t *testing.T) {
	bin, args := chromiumArgs("/usr/bin/chromium", "/tmp/in.html", "/tmp/out.pdf")

	if bin != "/usr/bin/chromium" {
		t.Errorf("got binary %q", bin)
	}
	if args[len(args)-1] != "/tmp/in.html" {
		t.Errorf("the input must be the last argument, got %q", args[len(args)-1])
	}
	if !strings.Contains(strings.Join(args, " "), "--print-to-pdf=/tmp/out.pdf") {
		t.Error("the output path is not passed")
	}
}
