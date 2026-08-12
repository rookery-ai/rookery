package ui

import (
	"os/exec"
	"strings"
	"testing"
)

// TestDistPlaceholderIsTracked asserts that web/ui/dist/.gitkeep is committed,
// not merely present on this machine.
//
// embed.go's `//go:embed all:dist` fails the BUILD when the pattern matches
// nothing, and go:embed reads the working tree rather than git. So a developer
// who has ever run `make ui` has a populated dist/ and builds fine forever,
// while a fresh clone has no dist/ directory at all and cannot compile the
// module — `go build ./...` fails before any test runs.
//
// That is exactly what happened: .gitignore carries the `!web/ui/dist/.gitkeep`
// negation and embed.go's own comment states the placeholder is what "keeps the
// embed pattern valid, so `go build` works without node" — but the file was
// never actually added, so the negation protected a file git had never heard of.
// The failure is invisible to everyone who has built the SPA once, which is
// everyone who works on the project, and hits only new contributors.
//
// A test that merely stats the file would pass in precisely the broken case, so
// this asks git what it tracks.
func TestDistPlaceholderIsTracked(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH; cannot verify what is tracked")
	}

	out, err := exec.Command("git", "ls-files", "--error-unmatch", "dist/.gitkeep").CombinedOutput()
	if err != nil {
		t.Fatalf("web/ui/dist/.gitkeep is NOT tracked by git (%v: %s)\n\n"+
			"embed.go's `//go:embed all:dist` fails the build when dist/ is absent, so a fresh\n"+
			"clone cannot compile the module. Fix with:\n\n"+
			"    git add -f web/ui/dist/.gitkeep\n\n"+
			"The -f is required: .gitignore ignores web/ui/dist/* and the negation for this\n"+
			"file only takes effect once git is tracking it.",
			err, strings.TrimSpace(string(out)))
	}
}
