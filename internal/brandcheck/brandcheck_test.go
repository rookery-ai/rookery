// Package brandcheck holds a single repository-wide guard test. It deliberately
// has no non-test source: the check is about the shape of the tree, not about
// any type this package exports.
package brandcheck

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// legacyTokens are the brand strings the Rookery rename removed.
//
// This guard is mechanical rather than trusted to review because several of the
// surfaces that carry these strings are checked by nothing else: prompt text and
// SKILL.md bodies are model-facing and never compiled, and the cosign
// certificate-identity regexp is verified at signature-verification time, so a
// stale value there produces a fully green pipeline and an unverifiable release.
var legacyTokens = []string{
	"simple-agents",
	"simple_agents",
	"SimpleAgents",
	"Simple Agents",
	"SA_",
	".sa_out",
	".sab",
}

// allowedPrefixes are repo-relative paths exempt from the scan. They are dated
// records and release-please-managed history: they describe what was true when
// they were written, and rewriting them would falsify the archive. brandcheck
// itself is exempt because it necessarily contains every token it bans.
//
// CHANGELOG.md, CHANGES.md and plans/ were removed in the rookery-ai migration:
// the changelog was regenerated from a reset manifest and the other two were
// stale pre-rename artifacts. release-please recreates CHANGELOG.md at the first
// release, so its exemption is restored then, not now — an exemption for a file
// that does not exist is a blind spot waiting for a future file to land in it.
var allowedPrefixes = []string{
	"docs/superpowers/",
	"internal/brandcheck/",
}

// skipNames are never walked or read: VCS internals, dependencies and build
// output. ".git" is matched as a NAME rather than a directory because in a git
// worktree it is a FILE containing a gitdir pointer.
var skipNames = map[string]bool{
	".git":         true,
	".claude":      true,
	".superpowers": true, // gitignored local agent tooling, same class as .claude
	"node_modules": true,
	"dist":         true,
	"bin":          true,
	"logs":         true,
}

// maxScanBytes bounds what is read. Anything larger is generated or vendored,
// not source anybody renames by hand.
const maxScanBytes = 2 << 20

// repoRoot walks up from the test's working directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate go.mod above the working directory")
		}
		dir = parent
	}
}

func TestNoLegacyBrandStrings(t *testing.T) {
	root := repoRoot(t)

	var offenders []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if skipNames[d.Name()] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		for _, p := range allowedPrefixes {
			if strings.HasPrefix(rel, p) {
				return nil
			}
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > maxScanBytes {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// A NUL byte means binary; scanning it yields noise, not signal.
		if bytes.IndexByte(data, 0) >= 0 {
			return nil
		}

		for _, tok := range legacyTokens {
			if bytes.Contains(data, []byte(tok)) {
				offenders = append(offenders, rel+": "+tok)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	if len(offenders) > 0 {
		t.Fatalf("legacy brand strings survive in %d place(s):\n  %s",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}

// TestAllowedPrefixesExist fails when the exemption list names a path that is no
// longer in the tree. A stale exemption is worse than a missing one: it silently
// widens the scan's blind spot if a future file lands at that path.
func TestAllowedPrefixesExist(t *testing.T) {
	root := repoRoot(t)
	for _, p := range allowedPrefixes {
		if p == "internal/brandcheck/" {
			continue // always present; it is this package
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err != nil {
			t.Errorf("allowedPrefixes names %q, which does not exist: %v", p, err)
		}
	}
}
