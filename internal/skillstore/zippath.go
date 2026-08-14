package skillstore

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// errEmptyEntryName marks a member that is only the archive's root folder, and
// so carries no file of its own. It is the one refusal the caller skips past
// rather than aborting on.
var errEmptyEntryName = errors.New("zip entry has an empty name")

// skillEntryPath resolves one zip member to an absolute path inside skillDir,
// refusing anything that would land outside it.
//
// A skill ZIP is an upload, so every member name is attacker-controlled. The
// check this replaces was `strings.HasPrefix(filepath.Clean(rel), "..")`, which
// asks a spelling question that merely correlates with the real one. It
// happened to hold, but it was wrong in both directions: it refused ordinary
// files whose names begin with ".." (a skill shipping "..config" installed
// without it and without a word of explanation), and it would stop holding the
// moment anything upstream of it changed. Ask whether the result is inside the
// directory instead — the same shape internal/backup's safeJoin uses for the
// restore path.
//
// An escape is an error rather than a skip: a silently dropped member installs
// a half-formed skill whose scripts reference files that never arrived.
func skillEntryPath(skillDir, rootPrefix, name string) (string, error) {
	// The ZIP spec (APPNOTE 4.4.17) requires forward slashes in member names,
	// so a backslash is either a non-conformant archive or a deliberate one.
	// Refusing it uniformly is what keeps this check from meaning two different
	// things on two platforms: `..\evil.py` is a literal filename on POSIX and
	// a traversal on Windows, and the binary ships for both.
	if strings.Contains(name, "\\") {
		return "", fmt.Errorf("zip entry %q contains a backslash; entry names must use forward slashes", name)
	}
	slash := filepath.ToSlash(name)
	if path.IsAbs(slash) || strings.HasPrefix(slash, "/") {
		return "", fmt.Errorf("zip entry %q is an absolute path", name)
	}
	rel := strings.TrimPrefix(slash, rootPrefix)
	if strings.TrimSpace(rel) == "" {
		return "", fmt.Errorf("%w: %q", errEmptyEntryName, name)
	}

	clean := filepath.Clean(filepath.FromSlash(rel))
	target := filepath.Join(skillDir, clean)
	inside, err := filepath.Rel(skillDir, target)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("zip entry %q escapes the skill directory", name)
	}
	return target, nil
}
