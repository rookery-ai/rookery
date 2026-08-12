package brandcheck

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// personalOwner is the account the project was developed under before moving to
// the rookery-ai organization.
//
// This guard exists because the rename touched 188 Go files, a cosign
// certificate-identity regexp, two install scripts, a container image path and
// the website — and NONE of those fail visibly when stale. A wrong cosign
// identity produces a fully green pipeline and an unverifiable release; a stale
// image path produces documentation that pulls a package nobody publishes.
const personalOwner = "ilijad1"

// ownerAllowedPrefixes are exempt: dated design records describing what was true
// when written. Rewriting them would falsify the archive.
var ownerAllowedPrefixes = []string{
	"docs/superpowers/",
	"internal/brandcheck/",
}

func TestNoPersonalAccountReferences(t *testing.T) {
	// TODO(task-18): remove this skip in the module-rename commit, which is what
	// makes this test pass. It is committed ahead of that change deliberately, so
	// the rename has a test that fails first.
	t.Skip("unskipped by the rookery-ai module rename — see Task 18")

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
		for _, p := range ownerAllowedPrefixes {
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
		if bytes.IndexByte(data, 0) >= 0 {
			return nil // binary
		}
		if bytes.Contains(data, []byte(personalOwner)) {
			offenders = append(offenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	if len(offenders) > 0 {
		t.Errorf("%d file(s) still reference the personal account %q:\n  %s\n\n"+
			"The project lives at github.com/rookery-ai/rookery. Stale references here "+
			"fail silently: a wrong cosign certificate-identity regexp still produces a "+
			"green pipeline and an unverifiable release.",
			len(offenders), personalOwner, strings.Join(offenders, "\n  "))
	}
}
