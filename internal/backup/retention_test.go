package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func seedSnapshots(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPruneKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	seedSnapshots(t, dir,
		"rookery-20260725-030000.rkb",
		"rookery-20260726-030000.rkb",
		"rookery-20260727-030000.rkb",
		"rookery-20260728-030000.rkb",
	)
	deleted, err := Prune(context.Background(), NewLocalDestination(dir), 2)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(deleted) != 2 {
		t.Fatalf("deleted %v, want the two oldest", deleted)
	}
	remaining, _ := NewLocalDestination(dir).List(context.Background())
	if len(remaining) != 2 {
		t.Fatalf("kept %d, want 2", len(remaining))
	}
	for _, e := range remaining {
		if e.Name < "rookery-20260727" {
			t.Fatalf("kept the wrong ones: %+v", remaining)
		}
	}
}

func TestPruneNoopUnderLimit(t *testing.T) {
	dir := t.TempDir()
	seedSnapshots(t, dir, "rookery-20260728-030000.rkb")
	deleted, err := Prune(context.Background(), NewLocalDestination(dir), 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 0 {
		t.Fatalf("deleted %v, want none", deleted)
	}
}

// The property that matters most: a bucket or folder shared with other data
// must never lose a foreign file to retention.
func TestPruneNeverTouchesForeignFiles(t *testing.T) {
	dir := t.TempDir()
	seedSnapshots(t, dir,
		"rookery-20260725-030000.rkb",
		"rookery-20260726-030000.rkb",
	)
	if err := os.WriteFile(filepath.Join(dir, "important-tax-return.pdf"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Prune(context.Background(), NewLocalDestination(dir), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "important-tax-return.pdf")); err != nil {
		t.Fatalf("a foreign file was deleted: %v", err)
	}
}

func TestPruneRejectsKeepBelowOne(t *testing.T) {
	if _, err := Prune(context.Background(), NewLocalDestination(t.TempDir()), 0); err == nil {
		t.Fatal("keep<1 would delete every snapshot; it must be refused")
	}
}
