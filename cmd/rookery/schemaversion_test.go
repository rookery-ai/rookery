package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A .rkb snapshot records the schema version as this exact string and compares
// it on restore, so switching the source from disk to the embedded FS must not
// change what it returns. Comparing against the directory rather than a
// hardcoded name keeps the test correct when the next migration lands.
func TestBinarySchemaVersionMatchesNewestOnDisk(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	want := ""
	for _, e := range entries {
		if name := e.Name(); strings.HasSuffix(name, ".up.sql") && name > want {
			want = name
		}
	}
	if want == "" {
		t.Fatal("no .up.sql files on disk")
	}

	got, err := binarySchemaVersion()
	if err != nil {
		t.Fatalf("binarySchemaVersion: %v", err)
	}
	if got != want {
		t.Errorf("binarySchemaVersion() = %q, newest on disk is %q", got, want)
	}
}

// It must not depend on the working directory — that dependency is the whole bug.
func TestBinarySchemaVersionWorksFromAnyDirectory(t *testing.T) {
	before, err := binarySchemaVersion()
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}

	t.Chdir(t.TempDir())

	after, err := binarySchemaVersion()
	if err != nil {
		t.Fatalf("after chdir: %v", err)
	}
	if after != before {
		t.Errorf("changed with CWD: %q then %q", before, after)
	}
}
