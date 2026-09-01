package packaging

import (
	"strings"
	"testing"
)

// Both installers must offer the browser.
//
// The reported complaint was being offered things during `rookery onboard` that
// the installer should already have dealt with — the browser among them. It was
// never offered at install time at all.
func TestBothInstallersOfferTheBrowser(t *testing.T) {
	for _, name := range []string{"install.sh", "install.ps1"} {
		body := repoFile(t, name)
		if !strings.Contains(body, "rookery browser install") {
			t.Errorf("%s never offers the browser", name)
		}
	}
}

// Neither installer may go looking for a browser itself.
//
// This one reads like an arbitrary restriction and is not. Playwright's runtime
// is version-MATCHED to the binary: the cache directory is named after the
// Playwright version compiled into it, so any version a shell script hardcoded
// would silently stop matching at the next dependency bump — and the failure
// mode is "the browser is installed and Rookery cannot see it", which is
// precisely the class of bug this whole change set exists to remove.
//
// Installing a system browser instead does not help either. Rookery drives even
// a system Chrome through Playwright's own Node driver, so the driver is needed
// regardless; a `winget install Google.Chrome` in this script would download a
// browser and leave the capability still missing. The binary decides, because
// it is the only thing that knows both the pinned version and what is already
// present.
func TestTheInstallersDoNotResolveTheBrowserThemselves(t *testing.T) {
	for _, name := range []string{"install.sh", "install.ps1"} {
		// Comments are stripped first: both scripts EXPLAIN this rule, naming
		// the very things it forbids, and a check that cannot tell code from
		// the prose describing it would forbid documenting the decision.
		body := withoutComments(repoFile(t, name))
		for _, marker := range []string{"ms-playwright", "playwright-go", "Google.Chrome", "Microsoft.Edge"} {
			if strings.Contains(body, marker) {
				t.Errorf("%s resolves the browser itself (%q); only the binary knows the pinned version", name, marker)
			}
		}
	}
}

// withoutComments drops whole-line comments. Both shells use "#", which is the
// only form these scripts use for the commentary this matters for.
func withoutComments(body string) string {
	var kept []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// Asking before a several-hundred-megabyte download is the minimum, and a
// script piped from the network into a shell must never start one on its own
// initiative — nor hang when there is no terminal to ask on.
func TestTheBrowserIsOfferedAndSkippable(t *testing.T) {
	for _, name := range []string{"install.sh", "install.ps1"} {
		body := repoFile(t, name)
		if !strings.Contains(body, "ROOKERY_NO_BROWSER") {
			t.Errorf("%s offers no way to skip the browser step", name)
		}
		if !strings.Contains(body, "Download it now?") {
			t.Errorf("%s does not ask before downloading the browser", name)
		}
	}
}

// The step is skipped entirely when the machine can already do this, which is
// what stops an owner who has Chrome being offered a download they do not need.
func TestTheInstallersCheckBeforeOffering(t *testing.T) {
	for _, name := range []string{"install.sh", "install.ps1"} {
		if !strings.Contains(repoFile(t, name), "browser status") {
			t.Errorf("%s offers the browser without first asking whether one is usable", name)
		}
	}
}
