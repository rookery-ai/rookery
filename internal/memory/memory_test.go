package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppendAndListAndDelete(t *testing.T) {
	s := New(t.TempDir())
	const user = "u1"

	e1, err := s.Append(user, "first fact")
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := s.Append(user, "second fact"); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Entries go to GENERAL.md as bullet lines.
	data, err := os.ReadFile(filepath.Join(s.memDir(user), "GENERAL.md"))
	if err != nil {
		t.Fatalf("GENERAL.md not written: %v", err)
	}
	if !strings.Contains(string(data), "first fact") || !strings.Contains(string(data), "second fact") {
		t.Fatalf("GENERAL.md missing content: %q", data)
	}

	entries, err := s.List(user)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List len = %d, want 2", len(entries))
	}
	if entries[0].Content != "first fact" || entries[1].Content != "second fact" {
		t.Errorf("List content wrong: %q, %q", entries[0].Content, entries[1].Content)
	}
	if entries[0].CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt for first entry")
	}

	// Delete first entry by ID.
	if err := s.Delete(user, e1.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	entries, _ = s.List(user)
	if len(entries) != 1 || entries[0].Content != "second fact" {
		t.Fatalf("after delete: %v", entries)
	}
}

func TestContextStringSectionedOutput(t *testing.T) {
	s := New(t.TempDir())
	const user = "u1"

	dir := s.memDir(user)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "USER.md"), []byte("# About Me\n\nName: Ilija\nLocation: Skopje\n"), 0o640)
	_ = os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("# Style\n\nDirect and concise.\n"), 0o640)
	_ = os.WriteFile(filepath.Join(dir, "WORK.md"), []byte("# Work\n\nBuilding home server tools.\n"), 0o640)

	ctx, err := s.ContextString(user)
	if err != nil {
		t.Fatalf("ContextString: %v", err)
	}
	for _, want := range []string{"## USER.md", "Name: Ilija", "## SOUL.md", "Direct and concise", "## WORK.md", "Building home server"} {
		if !strings.Contains(ctx, want) {
			t.Errorf("ContextString missing %q in:\n%s", want, ctx)
		}
	}
}

func TestContextStringSkipsEmptyTemplates(t *testing.T) {
	s := New(t.TempDir())
	const user = "u1"

	dir := s.memDir(user)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	// Scaffold-style placeholder — should be skipped.
	_ = os.WriteFile(filepath.Join(dir, "USER.md"),
		[]byte("# About Me\n\n<!-- Add your name, location, role, and background here -->\n"), 0o640)
	// File with real content — should be included.
	_ = os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("# Style\n\nBe direct.\n"), 0o640)

	ctx, err := s.ContextString(user)
	if err != nil {
		t.Fatalf("ContextString: %v", err)
	}
	if strings.Contains(ctx, "USER.md") {
		t.Errorf("ContextString should skip placeholder USER.md, got:\n%s", ctx)
	}
	if !strings.Contains(ctx, "## SOUL.md") {
		t.Errorf("ContextString should include SOUL.md, got:\n%s", ctx)
	}
}

func TestMigrateToStructuredFiles(t *testing.T) {
	s := New(t.TempDir())
	const user = "u1"

	dir := s.memDir(user)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	// Write two legacy UUID-named files with YAML frontmatter.
	ts1, _ := time.Parse(time.RFC3339, "2021-01-01T00:00:00Z")
	ts2, _ := time.Parse(time.RFC3339, "2021-01-02T00:00:00Z")
	e1 := &Entry{ID: "1000000001", Content: "old fact one", CreatedAt: ts1}
	e2 := &Entry{ID: "1000000002", Content: "old fact two", CreatedAt: ts2}
	_ = os.WriteFile(filepath.Join(dir, e1.ID+".md"), []byte(renderNote(e1)), 0o640)
	_ = os.WriteFile(filepath.Join(dir, e2.ID+".md"), []byte(renderNote(e2)), 0o640)

	// Named file that must NOT be touched.
	_ = os.WriteFile(filepath.Join(dir, "USER.md"), []byte("# About Me\n\nKeep me.\n"), 0o640)

	if err := s.MigrateToStructuredFiles(user); err != nil {
		t.Fatalf("MigrateToStructuredFiles: %v", err)
	}

	// UUID files must be gone.
	for _, id := range []string{e1.ID, e2.ID} {
		if _, err := os.Stat(filepath.Join(dir, id+".md")); !os.IsNotExist(err) {
			t.Errorf("legacy file %s.md still exists after migration", id)
		}
	}

	// GENERAL.md must contain both facts as bullets.
	data, err := os.ReadFile(filepath.Join(dir, "GENERAL.md"))
	if err != nil {
		t.Fatalf("GENERAL.md missing: %v", err)
	}
	for _, want := range []string{"old fact one", "old fact two"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("GENERAL.md missing %q", want)
		}
	}

	// USER.md must be untouched.
	userData, _ := os.ReadFile(filepath.Join(dir, "USER.md"))
	if !strings.Contains(string(userData), "Keep me.") {
		t.Errorf("USER.md was modified by migration")
	}

	// Second run must be idempotent — GENERAL.md must not change.
	if err := s.MigrateToStructuredFiles(user); err != nil {
		t.Fatalf("second MigrateToStructuredFiles: %v", err)
	}
	data2, _ := os.ReadFile(filepath.Join(dir, "GENERAL.md"))
	if string(data) != string(data2) {
		t.Errorf("GENERAL.md changed on second run (not idempotent):\nbefore: %s\nafter: %s", data, data2)
	}
}

func TestImportJSONL(t *testing.T) {
	base := t.TempDir()
	s := New(base)
	const user = "u1"

	legacy := filepath.Join(t.TempDir(), "memory.jsonl")
	lines := `{"id":"100","content":"old one","created_at":"2021-01-01T00:00:00Z"}
{"id":"200","content":"old two","created_at":"2021-01-02T00:00:00Z"}

{"bad json}
`
	if err := os.WriteFile(legacy, []byte(lines), 0o640); err != nil {
		t.Fatal(err)
	}
	n, err := s.ImportJSONL(user, legacy)
	if err != nil {
		t.Fatalf("ImportJSONL: %v", err)
	}
	if n != 2 {
		t.Fatalf("imported = %d, want 2 (malformed/blank skipped)", n)
	}

	// Migrate UUID files into GENERAL.md before using List.
	if err := s.MigrateToStructuredFiles(user); err != nil {
		t.Fatalf("MigrateToStructuredFiles: %v", err)
	}
	entries, _ := s.List(user)
	if len(entries) != 2 || entries[0].Content != "old one" {
		t.Fatalf("entries after migrate = %v", entries)
	}
}
