package memory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppendListDeleteMarkdown(t *testing.T) {
	s := New(t.TempDir())
	const user = "u1"

	a, err := s.Append(user, "first fact")
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := s.Append(user, "second fact"); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Notes are real markdown files with frontmatter under memory/.
	notePath := filepath.Join(s.memDir(user), a.ID+".md")
	data, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatalf("note not written as .md: %v", err)
	}
	if !contains(string(data), "first fact") || !contains(string(data), "id: "+a.ID) {
		t.Fatalf("note missing content/frontmatter: %q", data)
	}

	entries, err := s.List(user)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List len = %d, want 2", len(entries))
	}
	if entries[0].Content != "first fact" || entries[1].Content != "second fact" {
		t.Errorf("List order wrong: %q, %q", entries[0].Content, entries[1].Content)
	}

	if err := s.Delete(user, a.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	entries, _ = s.List(user)
	if len(entries) != 1 || entries[0].Content != "second fact" {
		t.Fatalf("after delete: %v", entries)
	}

	ctx, _ := s.ContextString(user)
	if ctx != "- second fact\n" {
		t.Errorf("ContextString = %q", ctx)
	}
}

func TestImportJSONL(t *testing.T) {
	base := t.TempDir()
	s := New(base)
	const user = "u1"

	// Simulate a legacy memory.jsonl somewhere and import it.
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
	entries, _ := s.List(user)
	if len(entries) != 2 || entries[0].Content != "old one" {
		t.Fatalf("entries = %v", entries)
	}

	// Re-running must not duplicate (idempotent).
	n2, _ := s.ImportJSONL(user, legacy)
	if n2 != 0 {
		t.Fatalf("re-import = %d, want 0 (idempotent)", n2)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
