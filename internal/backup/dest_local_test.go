package backup

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSnapshotNameSortsLexically(t *testing.T) {
	earlier := SnapshotName(time.Date(2026, 7, 29, 3, 0, 0, 0, time.UTC))
	later := SnapshotName(time.Date(2026, 7, 30, 3, 0, 0, 0, time.UTC))
	if earlier != "rookery-20260729-030000.rkb" {
		t.Fatalf("got %q", earlier)
	}
	if !(earlier < later) {
		t.Fatal("snapshot names must sort lexically by time — retention depends on it")
	}
}

func TestIsSnapshotName(t *testing.T) {
	if !IsSnapshotName("rookery-20260729-030000.rkb") {
		t.Fatal("must accept a well-formed name")
	}
	for _, bad := range []string{
		"notes.txt", "rookery-2026-07-29.rkb", "rookery-20260729-030000.rkb.tmp",
		"other-20260729-030000.rkb",
	} {
		if IsSnapshotName(bad) {
			t.Fatalf("must reject %q — retention deletes only what it matches", bad)
		}
	}
}

func TestLocalPutGetListDelete(t *testing.T) {
	dir := t.TempDir()
	d := NewLocalDestination(dir)
	ctx := context.Background()
	body := []byte("snapshot bytes")
	name := SnapshotName(time.Date(2026, 7, 29, 3, 0, 0, 0, time.UTC))

	if err := d.Put(ctx, name, bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatalf("Put: %v", err)
	}

	rc, err := d.Get(ctx, name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(got, body) {
		t.Fatalf("got %q, want %q", got, body)
	}

	entries, err := d.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != name || entries[0].Size != int64(len(body)) {
		t.Fatalf("List = %+v", entries)
	}

	if err := d.Delete(ctx, name); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if entries, _ := d.List(ctx); len(entries) != 0 {
		t.Fatalf("expected empty after delete, got %+v", entries)
	}
}

func TestLocalListIgnoresForeignFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "important-tax-return.pdf"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "rookery-20260729-030000.rkb.tmp"), []byte("x"), 0o644)

	entries, err := NewLocalDestination(dir).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a shared directory's other files must never be listed: %+v", entries)
	}
}

// A listing must never expose a half-written upload.
func TestLocalPutIsAtomic(t *testing.T) {
	dir := t.TempDir()
	d := NewLocalDestination(dir)
	name := SnapshotName(time.Now().UTC())
	if err := d.Put(context.Background(), name, strings.NewReader("body"), 4); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if len(matches) != 0 {
		t.Fatalf("temp files must not survive a successful Put: %v", matches)
	}
}

func TestLocalGetMissingIsNotFound(t *testing.T) {
	_, err := NewLocalDestination(t.TempDir()).Get(context.Background(), "rookery-20260729-030000.rkb")
	if err == nil {
		t.Fatal("expected an error for a missing snapshot")
	}
}

func TestLocalDeleteRefusesForeignName(t *testing.T) {
	dir := t.TempDir()
	foreign := filepath.Join(dir, "important-tax-return.pdf")
	os.WriteFile(foreign, []byte("x"), 0o644)

	if err := NewLocalDestination(dir).Delete(context.Background(), "important-tax-return.pdf"); err == nil {
		t.Fatal("delete must refuse a name that is not a snapshot")
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("the foreign file must survive: %v", err)
	}
}
