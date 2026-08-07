package vault

import (
	"os"
	"path/filepath"
	"testing"
)

// newMigrateVault builds a vault rooted in a temp dir with one workspace.
func newMigrateVault(t *testing.T) (*Vault, string) {
	t.Helper()
	base := t.TempDir()
	v := New(base)
	root := v.Root("ws1")
	if err := os.MkdirAll(filepath.Join(root, "notes"), 0o750); err != nil {
		t.Fatal(err)
	}
	return v, root
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestMigrateFilesToUploadsRenamesTheDirectory(t *testing.T) {
	v, root := newMigrateVault(t)
	writeFile(t, filepath.Join(root, "files", "report.pdf"), "%PDF-1.4")

	if err := v.MigrateFilesToUploads(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "files")); !os.IsNotExist(err) {
		t.Error("files/ should be gone after the migration")
	}
	if got := readFile(t, filepath.Join(root, "uploads", "report.pdf")); got != "%PDF-1.4" {
		t.Errorf("the original was not preserved, got %q", got)
	}
}

// renderImportedNote embeds the original's path twice. Both must be rewritten,
// or every imported note is left with two dead links.
func TestMigrateFilesToUploadsRewritesBothReferences(t *testing.T) {
	v, root := newMigrateVault(t)
	writeFile(t, filepath.Join(root, "files", "report.pdf"), "x")
	note := filepath.Join(root, "notes", "report.md")
	writeFile(t, note, "---\noriginal_file: \"files/report.pdf\"\n---\n\n"+
		"_Converted from [report.pdf](files/report.pdf)._\n\nBody.\n")

	if err := v.MigrateFilesToUploads(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	got := readFile(t, note)
	want := "---\noriginal_file: \"uploads/report.pdf\"\n---\n\n" +
		"_Converted from [report.pdf](uploads/report.pdf)._\n\nBody.\n"
	if got != want {
		t.Errorf("references not rewritten\n got: %q\nwant: %q", got, want)
	}
}

// The rewrite is scoped to the two emitted patterns. A blind replace would
// corrupt ordinary prose that happens to mention such a path — for instance an
// agent's own notes about a repository layout.
func TestMigrateFilesToUploadsLeavesProseAlone(t *testing.T) {
	v, root := newMigrateVault(t)
	prose := "The repo keeps its assets in files/ and the build reads files/config.json.\n" +
		"See the `files/` directory for details.\n"
	note := filepath.Join(root, "notes", "prose.md")
	writeFile(t, note, prose)

	if err := v.MigrateFilesToUploads(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if got := readFile(t, note); got != prose {
		t.Errorf("prose was rewritten\n got: %q\nwant: %q", got, prose)
	}
}

func TestMigrateFilesToUploadsIsIdempotent(t *testing.T) {
	v, root := newMigrateVault(t)
	writeFile(t, filepath.Join(root, "files", "a.pdf"), "x")
	note := filepath.Join(root, "notes", "a.md")
	writeFile(t, note, "original_file: \"files/a.pdf\"\n[a.pdf](files/a.pdf)\n")

	for i := 0; i < 3; i++ {
		if err := v.MigrateFilesToUploads(); err != nil {
			t.Fatalf("migrate run %d: %v", i, err)
		}
	}

	want := "original_file: \"uploads/a.pdf\"\n[a.pdf](uploads/a.pdf)\n"
	if got := readFile(t, note); got != want {
		t.Errorf("not idempotent\n got: %q\nwant: %q", got, want)
	}
	if got := readFile(t, filepath.Join(root, "uploads", "a.pdf")); got != "x" {
		t.Errorf("original lost, got %q", got)
	}
}

// files/ is created lazily, never by EnsureScaffold, so most vaults will not
// have one. That must be a clean no-op rather than an error.
func TestMigrateFilesToUploadsNoOpWhenNothingToDo(t *testing.T) {
	v, root := newMigrateVault(t)
	writeFile(t, filepath.Join(root, "notes", "plain.md"), "Just a note.\n")

	if err := v.MigrateFilesToUploads(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "uploads")); !os.IsNotExist(err) {
		t.Error("the migration must not create uploads/ out of nothing")
	}
}

// An install that already has uploads/ (a newer asset upload landed first) must
// have files/ drained into it without clobbering anything.
func TestMigrateFilesToUploadsDrainsWithoutClobbering(t *testing.T) {
	v, root := newMigrateVault(t)
	writeFile(t, filepath.Join(root, "files", "old.pdf"), "old")
	writeFile(t, filepath.Join(root, "files", "same.png"), "from-files")
	writeFile(t, filepath.Join(root, "uploads", "same.png"), "already-here")

	if err := v.MigrateFilesToUploads(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if got := readFile(t, filepath.Join(root, "uploads", "old.pdf")); got != "old" {
		t.Errorf("old.pdf not drained, got %q", got)
	}
	if got := readFile(t, filepath.Join(root, "uploads", "same.png")); got != "already-here" {
		t.Errorf("an existing upload was clobbered, got %q", got)
	}
}

// A missing vaults dir is normal on a fresh install.
func TestMigrateFilesToUploadsToleratesNoVaults(t *testing.T) {
	v := New(t.TempDir())
	if err := v.MigrateFilesToUploads(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
}
