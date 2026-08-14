package skillstore

import (
	"path/filepath"
	"strings"
	"testing"
)

// Zip entry names are attacker-controlled: a skill ZIP is an upload. The
// containment check that guards extraction was an ad-hoc
// `strings.HasPrefix(clean, "..")`, which is not the same question as "does
// this land inside the skill directory" — it is a spelling test that happens
// to correlate with it. Ask the real question instead, the way
// internal/backup's safeJoin does for the restore path.
func TestSkillEntryPathRejectsEscapes(t *testing.T) {
	dir := filepath.FromSlash("/vault/skills/demo")
	for _, name := range []string{
		"../evil.py",
		"../../evil.py",
		"a/../../evil.py",
		"a/b/../../../evil.py",
		"..",
		"../",
		// A backslash is a legal character in a POSIX filename but a separator
		// on Windows, so this one entry means two different things on the two
		// platforms the binary ships for. The ZIP spec requires forward
		// slashes, so it is refused on both rather than left platform-defined.
		`..\evil.py`,
		`a\..\..\evil.py`,
	} {
		if _, err := skillEntryPath(dir, "", name); err == nil {
			t.Errorf("entry %q should have been refused", name)
		}
	}
}

// The old check refused these too, silently dropping them: any name starting
// with ".." is not necessarily a traversal. A skill shipping a "..config" file
// installed without it and without a word of explanation.
func TestSkillEntryPathAcceptsOrdinaryNames(t *testing.T) {
	dir := filepath.FromSlash("/vault/skills/demo")
	cases := map[string]string{
		"SKILL.md":       "SKILL.md",
		"scripts/run.py": filepath.FromSlash("scripts/run.py"),
		"a/b/c.txt":      filepath.FromSlash("a/b/c.txt"),
		"..config":       "..config",
		"...dots":        "...dots",
		"a/..b/c.txt":    filepath.FromSlash("a/..b/c.txt"),
		"./SKILL.md":     "SKILL.md",
		"a/./b.txt":      filepath.FromSlash("a/b.txt"),
	}
	for name, wantRel := range cases {
		got, err := skillEntryPath(dir, "", name)
		if err != nil {
			t.Errorf("entry %q should have been accepted: %v", name, err)
			continue
		}
		if want := filepath.Join(dir, wantRel); got != want {
			t.Errorf("entry %q resolved to %q, want %q", name, got, want)
		}
	}
}

// An absolute member name is contained by filepath.Join rather than escaping,
// but it is still a malformed archive and worth refusing outright: a member
// claiming to be /etc/passwd is not a skill file that happens to be oddly
// named.
func TestSkillEntryPathRejectsAbsoluteNames(t *testing.T) {
	dir := filepath.FromSlash("/vault/skills/demo")
	for _, name := range []string{"/etc/passwd", "/a.txt"} {
		if _, err := skillEntryPath(dir, "", name); err == nil {
			t.Errorf("absolute entry %q should have been refused", name)
		}
	}
}

// The root prefix strip happens before containment, so an archive wrapped in a
// top-level folder resolves to the same place as a flat one — and a traversal
// hidden behind that prefix is still caught.
func TestSkillEntryPathStripsTheRootPrefix(t *testing.T) {
	dir := filepath.FromSlash("/vault/skills/demo")
	got, err := skillEntryPath(dir, "my-skill/", "my-skill/scripts/run.py")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "scripts", "run.py"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if _, err := skillEntryPath(dir, "my-skill/", "my-skill/../../evil.py"); err == nil {
		t.Fatal("a traversal behind the root prefix should have been refused")
	}
}

// An entry that is only the root prefix carries no file of its own.
func TestSkillEntryPathSkipsAnEmptyRemainder(t *testing.T) {
	dir := filepath.FromSlash("/vault/skills/demo")
	_, err := skillEntryPath(dir, "my-skill/", "my-skill/")
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("want an empty-name error, got %v", err)
	}
}
