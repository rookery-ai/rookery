package backup

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveRoundTrip(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "db.sqlite"), "DATABASE")
	writeFile(t, filepath.Join(src, "notes", "a.md"), "hello")

	files := []archiveFile{
		{Name: "db/rookery.db", Path: filepath.Join(src, "db.sqlite")},
		{Name: "vaults/ws1/notes/a.md", Path: filepath.Join(src, "notes", "a.md")},
	}

	var buf bytes.Buffer
	m := Manifest{FormatVersion: FormatVersion, SchemaVersion: "001_initial_schema.up.sql", SystemKey: "abcd"}
	if err := writeArchive(&buf, files, m); err != nil {
		t.Fatalf("writeArchive: %v", err)
	}

	out := t.TempDir()
	got, err := readArchive(bytes.NewReader(buf.Bytes()), out)
	if err != nil {
		t.Fatalf("readArchive: %v", err)
	}
	if got.SystemKey != "abcd" {
		t.Fatalf("system key = %q, want %q", got.SystemKey, "abcd")
	}
	if len(got.Files) != 2 {
		t.Fatalf("manifest lists %d files, want 2", len(got.Files))
	}
	if got.TotalBytes != int64(len("DATABASE")+len("hello")) {
		t.Fatalf("total bytes = %d, want %d", got.TotalBytes, len("DATABASE")+len("hello"))
	}

	body, err := os.ReadFile(filepath.Join(out, "vaults", "ws1", "notes", "a.md"))
	if err != nil {
		t.Fatalf("read extracted: %v", err)
	}
	if string(body) != "hello" {
		t.Fatalf("got %q, want %q", body, "hello")
	}

	// ApplyPendingRestore reads the system key back out of the staged manifest.
	if _, err := os.Stat(filepath.Join(out, ManifestName)); err != nil {
		t.Fatalf("manifest must be written to the destination: %v", err)
	}
}

func TestArchiveDetectsChecksumMismatch(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.md"), "hello")
	var buf bytes.Buffer
	if err := writeArchive(&buf, []archiveFile{{Name: "vaults/ws1/a.md", Path: filepath.Join(src, "a.md")}}, Manifest{FormatVersion: FormatVersion}); err != nil {
		t.Fatal(err)
	}

	// Corrupt the gzip payload; extraction must not silently succeed.
	raw := buf.Bytes()
	raw[len(raw)-6] ^= 0xff
	if _, err := readArchive(bytes.NewReader(raw), t.TempDir()); err == nil {
		t.Fatal("expected an error for a corrupted archive")
	}
}

func TestArchiveRejectsPathTraversal(t *testing.T) {
	var buf bytes.Buffer
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "evil"), "x")
	if err := writeArchive(&buf, []archiveFile{{Name: "../../escape", Path: filepath.Join(src, "evil")}}, Manifest{FormatVersion: FormatVersion}); err != nil {
		t.Fatal(err)
	}
	if _, err := readArchive(bytes.NewReader(buf.Bytes()), t.TempDir()); err == nil {
		t.Fatal("expected extraction to refuse a path escaping the destination")
	}
}

// The regression this guards: vault.List and its siblings hide dotfiles, so
// reusing them would silently drop .kb/ (db-export sidecars, links.json) from
// every snapshot — invisible until a restore came back with no link index.
func TestCollectVaultFilesIncludesDotKB(t *testing.T) {
	vaults := t.TempDir()
	writeFile(t, filepath.Join(vaults, "ws1", "notes", "a.md"), "note")
	writeFile(t, filepath.Join(vaults, "ws1", ".kb", "links.json"), "{}")
	writeFile(t, filepath.Join(vaults, "ws1", ".kb", "db-export", "chats", "c1.json"), "{}")

	files, err := collectVaultFiles(vaults)
	if err != nil {
		t.Fatalf("collectVaultFiles: %v", err)
	}
	var names []string
	for _, f := range files {
		names = append(names, f.Name)
	}
	sort.Strings(names)

	want := []string{
		"vaults/ws1/.kb/db-export/chats/c1.json",
		"vaults/ws1/.kb/links.json",
		"vaults/ws1/notes/a.md",
	}
	if len(names) != len(want) {
		t.Fatalf("got %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("got %v, want %v", names, want)
		}
	}
}

func TestCollectVaultFilesSkipsStagingDirs(t *testing.T) {
	vaults := t.TempDir()
	writeFile(t, filepath.Join(vaults, "ws1", "notes", "a.md"), "note")
	writeFile(t, filepath.Join(vaults, ".restore-staging", "junk"), "x")

	files, err := collectVaultFiles(vaults)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Name == "vaults/.restore-staging/junk" {
			t.Fatal("staging dirs must never be archived")
		}
	}
}

func TestCollectVaultFilesMissingDirIsEmpty(t *testing.T) {
	files, err := collectVaultFiles(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("a missing vaults dir is an empty install, not an error: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("got %d files, want 0", len(files))
	}
}
