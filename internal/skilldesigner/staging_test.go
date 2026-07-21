package skilldesigner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func TestLocateSkillRootAtRoot(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "SKILL.md"), "---\nname: x\n---\n")

	root, err := LocateSkillRoot(dir)
	require.NoError(t, err)
	require.Equal(t, dir, root)
}

// The observed failure: a weak model nests SKILL.md under <name>/ because that is the
// PUBLISHED layout skill-creator documents. A valid build must not be thrown away over
// a directory level (SP10 spec §1.1a).
func TestLocateSkillRootOneLevelDown(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "pretty-printer")
	writeFile(t, filepath.Join(nested, "SKILL.md"), "---\nname: pretty-printer\n---\n")
	writeFile(t, filepath.Join(nested, "scripts", "fmt.py"), "print('x')\n")

	root, err := LocateSkillRoot(dir)
	require.NoError(t, err)
	require.Equal(t, nested, root)
}

func TestLocateSkillRootAbsent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "notes.txt"), "nothing here\n")

	_, err := LocateSkillRoot(dir)
	require.ErrorIs(t, err, ErrNoSkillMD)
}

// Two candidates are ambiguous — guessing which one is the skill would be worse than
// soft-failing and asking the user.
func TestLocateSkillRootAmbiguous(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a", "SKILL.md"), "---\nname: a\n---\n")
	writeFile(t, filepath.Join(dir, "b", "SKILL.md"), "---\nname: b\n---\n")

	_, err := LocateSkillRoot(dir)
	require.ErrorIs(t, err, ErrNoSkillMD)
}

// A root SKILL.md wins even when a nested one also exists.
func TestLocateSkillRootPrefersRoot(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "SKILL.md"), "---\nname: root\n---\n")
	writeFile(t, filepath.Join(dir, "nested", "SKILL.md"), "---\nname: nested\n---\n")

	root, err := LocateSkillRoot(dir)
	require.NoError(t, err)
	require.Equal(t, dir, root)
}
