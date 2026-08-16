// Package packaging holds tests over the delivery surfaces — the installers,
// the container image and the native packages. Nothing here is compiled into
// the binary; these are assertions about files that no compiler ever reads,
// which is exactly why they need a test.
package packaging

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The four host tools Rookery degrades without. /healthz reports each one, and
// every delivery surface is expected to offer all four — the container installs
// them, the native packages recommend them, and the two installers offer them.
var hostTools = []string{"python3", "ripgrep", "poppler", "tesseract"}

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// The website advertises `curl -fsSL https://rookery.cloud/install.sh | sh` and
// `irm https://rookery.cloud/install.ps1 | iex` on the landing page and on three
// documentation pages. For the whole life of the repository neither file
// existed. This is the test that would have noticed.
func TestInstallScriptsExist(t *testing.T) {
	for _, name := range []string{"install.sh", "install.ps1"} {
		body := repoFile(t, name)
		if len(strings.TrimSpace(body)) == 0 {
			t.Errorf("%s is empty", name)
		}
	}
}

// The archive name is a goreleaser default, not something either installer can
// see. `rookery_<version>_<os>_<arch>.tar.gz` is what it produces today; if that
// convention ever changes, both installers 404 on a release that published
// perfectly well, and the failure looks like a network problem.
func TestInstallersUseTheGoreleaserArchiveNaming(t *testing.T) {
	sh := repoFile(t, "install.sh")
	if !strings.Contains(sh, `rookery_${NUM}_${OS}_${ARCH}.tar.gz`) {
		t.Error("install.sh does not build the goreleaser tar.gz archive name")
	}
	ps := repoFile(t, "install.ps1")
	if !strings.Contains(ps, `rookery_${num}_windows_${Arch}.zip`) {
		t.Error("install.ps1 does not build the goreleaser zip archive name")
	}
	// goreleaser strips the leading v from the version inside archive names but
	// keeps it on the tag, so both installers must do that trim explicitly.
	if !strings.Contains(sh, `NUM="${VERSION#v}"`) {
		t.Error("install.sh does not strip the leading v from the release tag")
	}
	if !strings.Contains(ps, `-replace '^v', ''`) {
		t.Error("install.ps1 does not strip the leading v from the release tag")
	}
}

// Every surface offers the same four tools. A surface that quietly drops one is
// the bug this batch was reported for: the rpm named a package Fedora does not
// have, so it installed no OCR and said nothing.
func TestEverySurfaceCoversTheHostTools(t *testing.T) {
	surfaces := map[string]string{
		"Dockerfile":       repoFile(t, "Dockerfile"),
		".goreleaser.yaml": repoFile(t, ".goreleaser.yaml"),
		"install.sh":       repoFile(t, "install.sh"),
		"install.ps1":      repoFile(t, "install.ps1"),
	}
	for name, body := range surfaces {
		for _, tool := range hostTools {
			if !strings.Contains(strings.ToLower(body), tool) {
				t.Errorf("%s never mentions %q", name, tool)
			}
		}
	}
}

// Fedora has no package called tesseract-ocr; its package is tesseract. dnf
// drops a weak dependency it cannot resolve without a word, which is why the rpm
// installed no OCR for its entire life and nothing reported an error. The names
// must therefore be declared per format, and the rpm override must exist.
func TestRPMRecommendsUseFedoraPackageNames(t *testing.T) {
	body := repoFile(t, ".goreleaser.yaml")

	// Anchored on the indented key, not the bare word: `archives` carries a
	// `format_overrides:` that a substring search finds first.
	idx := strings.Index(body, "\n    overrides:")
	if idx < 0 {
		t.Fatal(".goreleaser.yaml has no nfpms overrides — deb and rpm would share one dependency list again")
	}
	override := body[idx:]
	if !strings.Contains(override, "rpm:") {
		t.Fatal("nfpms overrides has no rpm section")
	}
	if !strings.Contains(override, "\n          - tesseract\n") {
		t.Error("the rpm override does not recommend `tesseract` (Fedora's actual package name)")
	}
	if strings.Contains(override, "tesseract-ocr") {
		t.Error("the rpm override still names tesseract-ocr, which does not exist on Fedora")
	}

	// The deb list is the un-overridden one, and Debian's package IS tesseract-ocr.
	base := body[:idx]
	if !strings.Contains(base, "tesseract-ocr") {
		t.Error("the base (deb) recommends should keep Debian's tesseract-ocr")
	}
}

// All four tools are installable on Windows via winget, which is what makes full
// feature coverage possible there without Chocolatey or Scoop. Poppler is the
// one whose id is not guessable — oschwartz10612.Poppler is the maintained
// Windows build and the one that ships pdftotext.exe.
func TestWindowsInstallerNamesRealWingetPackages(t *testing.T) {
	body := repoFile(t, "install.ps1")
	for _, id := range []string{
		"Python.Python.3",
		"BurntSushi.ripgrep.MSVC",
		"oschwartz10612.Poppler",
		"UB-Mannheim.TesseractOCR",
	} {
		if !strings.Contains(body, id) {
			t.Errorf("install.ps1 does not offer the winget package %q", id)
		}
	}
}

// The division of labour is deliberate: the scripts install, `rookery onboard`
// configures. Both must hand off, or a new user is left at a shell prompt with a
// binary and no idea that the next step exists.
func TestInstallersHandOffToOnboard(t *testing.T) {
	for _, name := range []string{"install.sh", "install.ps1"} {
		if !strings.Contains(repoFile(t, name), "rookery onboard") {
			t.Errorf("%s never tells the user to run `rookery onboard`", name)
		}
	}
}

// A checksum check that can be skipped by deleting a file is not a checksum
// check. Both installers must refuse to install on a mismatch rather than warn.
func TestInstallersRefuseAChecksumMismatch(t *testing.T) {
	if !strings.Contains(repoFile(t, "install.sh"), "checksum mismatch") {
		t.Error("install.sh does not fail on a checksum mismatch")
	}
	if !strings.Contains(repoFile(t, "install.ps1"), "checksum mismatch") {
		t.Error("install.ps1 does not fail on a checksum mismatch")
	}
}

// install.ps1 is advertised as `irm ... | iex`, which runs it in the CALLER's
// session rather than a child scope. `exit` there terminates the whole
// PowerShell session — closing the window and taking the error message with
// it, at exactly the moment the user most needs to read it. Nothing here can
// execute PowerShell, so the shape of the failure path is pinned by reading it.
func TestWindowsInstallerDoesNotExitTheCallersSession(t *testing.T) {
	body := repoFile(t, "install.ps1")
	if regexp.MustCompile(`(?m)^\s*exit\s+\d`).MatchString(body) {
		t.Error("install.ps1 calls `exit`, which kills the session when run via `irm | iex` — use `throw`")
	}
	if !strings.Contains(body, "throw ") {
		t.Error("install.ps1 never throws, so Stop-WithError cannot end the script at all")
	}
}

// Both scripts advertise their own URL in their header, and the domain is
// rookery.cloud. They carried rookery.sh — a domain this project does not use —
// for long enough that it reached the published file people are told to read
// before piping it to a shell.
func TestInstallersNameTheRealDomain(t *testing.T) {
	for _, name := range []string{"install.sh", "install.ps1"} {
		body := repoFile(t, name)
		if strings.Contains(body, "rookery.sh") {
			t.Errorf("%s still advertises rookery.sh; the domain is rookery.cloud", name)
		}
		if !strings.Contains(body, "rookery.cloud") {
			t.Errorf("%s never names rookery.cloud, so it does not say where it came from", name)
		}
	}
}

// The 404 message led with "the repository is still private" for the whole time
// that was true. It is public now, so that is the one cause a 404 CANNOT have,
// and leading with it sends users to fix something that is not broken. This
// pins the correction, because the wording is otherwise unreachable by any
// check and drifted once already.
func TestInstallersDoNotBlameAPrivateRepositoryForA404(t *testing.T) {
	for _, name := range []string{"install.sh", "install.ps1"} {
		body := repoFile(t, name)
		if strings.Contains(body, "still private") {
			t.Errorf("%s still blames a private repository for a 404; the repository is public", name)
		}
	}
}

// `iex` cannot pass arguments to what it runs, so -Version and -BinDir are
// unreachable through the advertised one-liner unless the script block idiom is
// documented in the file itself. A parameter nobody can reach is worse than no
// parameter: it reads as supported.
func TestWindowsInstallerDocumentsHowToPassParameters(t *testing.T) {
	if !strings.Contains(repoFile(t, "install.ps1"), "[scriptblock]::Create(") {
		t.Error("install.ps1 declares parameters but never shows how to pass them through `irm | iex`")
	}
}
