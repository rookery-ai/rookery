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

func TestReadSkillTreeKeepsEveryShippingFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "SKILL.md"), "---\nname: x\n---\n")
	writeFile(t, filepath.Join(dir, "scripts", "extract.py"), "print('hi')\n")
	writeFile(t, filepath.Join(dir, "scripts", "install.sh"), "#!/bin/bash\necho hi\n")
	writeFile(t, filepath.Join(dir, "scripts", "lib", "parse.py"), "X = 1\n")
	writeFile(t, filepath.Join(dir, "references", "api.md"), "# API\n")

	tree, err := ReadSkillTree(dir)
	require.NoError(t, err)

	require.Equal(t, map[string]string{
		"scripts/extract.py":   "print('hi')\n",
		"scripts/install.sh":   "#!/bin/bash\necho hi\n",
		"scripts/lib/parse.py": "X = 1\n",
		"references/api.md":    "# API\n",
	}, tree)
}

func TestReadSkillTreeExcludesSkillMDAndTestArtifacts(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "SKILL.md"), "---\nname: x\n---\n")
	writeFile(t, filepath.Join(dir, "scripts", "run.py"), "print(1)\n")
	writeFile(t, filepath.Join(dir, "sample.pdf"), "%PDF-1.4 binary\n")
	writeFile(t, filepath.Join(dir, "run.out"), "stdout capture\n")

	tree, err := ReadSkillTree(dir)
	require.NoError(t, err)

	require.Contains(t, tree, "scripts/run.py")
	require.NotContains(t, tree, "SKILL.md")
	require.NotContains(t, tree, "sample.pdf")
	require.NotContains(t, tree, "run.out")
}

func TestReadSkillTreeEmpty(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "SKILL.md"), "---\nname: x\n---\n")

	tree, err := ReadSkillTree(dir)
	require.NoError(t, err)
	require.Empty(t, tree)
}

func TestSlugifySkillName(t *testing.T) {
	cases := map[string]string{
		"pretty printer":     "pretty-printer",
		"Pretty Printer":     "pretty-printer",
		"PDF → Email":        "pdf-email",
		"my_skill":           "my-skill",
		"  spaced  out  ":    "spaced-out",
		"already-fine":       "already-fine",
		"a--b":               "a-b",
		"CSV/JSON converter": "csv-json-converter",
	}
	for in, want := range cases {
		require.Equal(t, want, SlugifySkillName(in), "input %q", in)
	}
}
