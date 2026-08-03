package vault

import (
	"context"
	"strings"
	"testing"
)

func TestParseAndResolveWikilinks(t *testing.T) {
	v := New(t.TempDir())
	const user = "u1"
	mustWrite(t, v, user, "notes/Groceries.md", "# Groceries\nmilk")
	mustWrite(t, v, user, "memory/fact-1.md", "remember [[Groceries]] and [[notes/Groceries]] and [[Missing|see this]]")

	idx, err := v.BuildLinkIndex(user)
	if err != nil {
		t.Fatalf("BuildLinkIndex: %v", err)
	}
	if got := idx.Resolve("Groceries"); got != "notes/Groceries.md" {
		t.Errorf("Resolve(Groceries) = %q, want notes/Groceries.md", got)
	}
	if got := idx.Resolve("notes/Groceries"); got != "notes/Groceries.md" {
		t.Errorf("Resolve(notes/Groceries) = %q, want notes/Groceries.md", got)
	}
	if got := idx.Resolve("Missing"); got != "" {
		t.Errorf("Resolve(Missing) = %q, want empty", got)
	}

	content, _ := v.ReadNote(user, "memory/fact-1.md")
	rendered := idx.RenderHTMLLinks(string(content), func(rel string) string {
		return "/dashboard/kb/view?path=" + rel
	})
	if !strings.Contains(rendered, "[Groceries](/dashboard/kb/view?path=notes/Groceries.md)") {
		t.Errorf("rendered missing resolved link: %q", rendered)
	}
	if !strings.Contains(rendered, "see this <sup>(no note)</sup>") {
		t.Errorf("rendered missing dangling-link marker: %q", rendered)
	}
}

func TestBacklinks(t *testing.T) {
	v := New(t.TempDir())
	const user = "u1"
	mustWrite(t, v, user, "notes/Target.md", "# Target")
	mustWrite(t, v, user, "notes/A.md", "links to [[Target]]")
	mustWrite(t, v, user, "notes/B.md", "no link here")

	back, err := v.Backlinks(user, "notes/Target.md")
	if err != nil {
		t.Fatalf("Backlinks: %v", err)
	}
	if len(back) != 1 || back[0] != "notes/A.md" {
		t.Errorf("Backlinks = %v, want [notes/A.md]", back)
	}
}

func TestSearchGoFallback(t *testing.T) {
	v := New(t.TempDir())
	const user = "u1"
	mustWrite(t, v, user, "notes/x.md", "alpha beta\ngamma")
	mustWrite(t, v, user, ".kb/secret.md", "alpha should be skipped")

	s := &ripgrepSearcher{v: v}
	hits, err := s.searchGo(user, "alpha")
	if err != nil {
		t.Fatalf("searchGo: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("searchGo hits = %d (%v), want 1 (.kb must be excluded)", len(hits), hits)
	}
	if hits[0].Path != "notes/x.md" || hits[0].Line != 1 {
		t.Errorf("hit = %+v, want notes/x.md line 1", hits[0])
	}
}

func TestSearchEndToEnd(t *testing.T) {
	// Exercises whichever backend is present (rg or fallback).
	v := New(t.TempDir())
	const user = "u1"
	mustWrite(t, v, user, "notes/find-me.md", "the needle is here")
	hits, err := v.NewSearcher().Search(context.Background(), user, "needle")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 || hits[0].Path != "notes/find-me.md" {
		t.Errorf("Search hits = %v, want a hit in notes/find-me.md", hits)
	}
}

func mustWrite(t *testing.T, v *Vault, user, rel, content string) {
	t.Helper()
	if err := v.WriteNote(user, rel, []byte(content)); err != nil {
		t.Fatalf("WriteNote(%s): %v", rel, err)
	}
}

// A bare [[Foo]] must resolve to the USER's note when an agent also wrote a
// Foo.md — the agent dir sorts before notes/, so first-seen-wins used to lose
// the user's note. namePriority fixes this.
func TestResolvePrefersUserContentOnCollision(t *testing.T) {
	v := New(t.TempDir())
	const user = "u1"
	mustWrite(t, v, user, "agents/abc/Report.md", "agent copy")
	mustWrite(t, v, user, "notes/Report.md", "the real user note")

	idx, err := v.BuildLinkIndex(user)
	if err != nil {
		t.Fatalf("BuildLinkIndex: %v", err)
	}
	if got := idx.Resolve("Report"); got != "notes/Report.md" {
		t.Errorf("Resolve(Report) = %q, want notes/Report.md", got)
	}
	// An exact-path link still reaches the agent copy.
	if got := idx.Resolve("agents/abc/Report"); got != "agents/abc/Report.md" {
		t.Errorf("Resolve(agents/abc/Report) = %q, want the agent copy", got)
	}
}

// Backlinks on a user note must appear (the reported bug), and must NOT include
// machine-generated sources: reflected chats and agent run logs.
func TestBacklinksExcludesSystemSourcesAndCoversUserNotes(t *testing.T) {
	v := New(t.TempDir())
	const user = "u1"
	mustWrite(t, v, user, "notes/Target.md", "# Target")
	mustWrite(t, v, user, "notes/Author.md", "see [[Target]]")            // real user backlink
	mustWrite(t, v, user, "agents/abc/logs/run_1.md", "wrote [[Target]]") // excluded source
	mustWrite(t, v, user, "chats/c1.md", "chat about [[Target]]")         // excluded source

	back, err := v.Backlinks(user, "notes/Target.md")
	if err != nil {
		t.Fatalf("Backlinks: %v", err)
	}
	if len(back) != 1 || back[0] != "notes/Author.md" {
		t.Errorf("Backlinks = %v, want exactly [notes/Author.md]", back)
	}
}

func TestListImageFiles(t *testing.T) {
	v := New(t.TempDir())
	const user = "u1"
	mustWrite(t, v, user, "assets/a.png", "x")
	mustWrite(t, v, user, "assets/b.JPG", "x")
	mustWrite(t, v, user, "notes/doc.md", "not an image")
	mustWrite(t, v, user, "assets/readme.txt", "not an image")

	imgs, err := v.ListImageFiles(user)
	if err != nil {
		t.Fatalf("ListImageFiles: %v", err)
	}
	if len(imgs) != 2 {
		t.Fatalf("ListImageFiles = %v, want 2 images", imgs)
	}
}
