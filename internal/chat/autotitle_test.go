package chat

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/rookery/internal/db"
)

func TestIsDefaultChatName(t *testing.T) {
	cases := map[string]bool{
		"Chat 2026-07-23 15:04": true,
		"Chat 2026-01-02 09:00": true,
		"Invoice questions":     false,
		"Chat about chat":       false,
		"":                      false,
	}
	for name, want := range cases {
		if got := isDefaultChatName(name); got != want {
			t.Errorf("isDefaultChatName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestSanitizeTitle(t *testing.T) {
	cases := map[string]string{
		"  Invoice Questions  ": "Invoice Questions",
		"\"Trip Planning\"":     "Trip Planning",
		"Budget review.":        "Budget review",
		"Title: Meeting Notes":  "Meeting Notes",
		"line one\nline two":    "line one line two",
		"":                      "",
	}
	for raw, want := range cases {
		if got := sanitizeTitle(raw); got != want {
			t.Errorf("sanitizeTitle(%q) = %q, want %q", raw, got, want)
		}
	}
	// Over-long titles are capped.
	long := ""
	for i := 0; i < 200; i++ {
		long += "x"
	}
	if got := sanitizeTitle(long); len(got) > 60 {
		t.Errorf("sanitizeTitle did not cap length: got %d chars", len(got))
	}
}

// newTestDB opens a fresh migrated DB, mirroring the pattern used by
// internal/db's own tests (e.g. inboxTestDB in internal/db/inbox_test.go).
func newTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

// seedWorkspace creates a workspace row and returns its id.
func seedWorkspace(t *testing.T, database *db.DB) string {
	t.Helper()
	workspaceID := uuid.New().String()
	if err := database.CreateWorkspace(&db.Workspace{ID: workspaceID, Name: "tester"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	return workspaceID
}

// waitFor polls pred for up to 2s (MaybeAutoTitle's rename runs in a
// background goroutine).
func waitFor(t *testing.T, pred func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if pred() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !pred() {
		t.Fatal("condition not met within timeout")
	}
}

func TestMaybeAutoTitle(t *testing.T) {
	database := newTestDB(t)
	ws := seedWorkspace(t, database)
	ch := &db.Chat{ID: "c1", WorkspaceID: ws, Name: "Chat 2026-07-23 15:04", Active: true}
	if err := database.CreateChat(ch); err != nil {
		t.Fatal(err)
	}

	gen := func(_ context.Context, _, _, _ string) (string, error) { return "Invoice Questions", nil }

	// Real user exchange → chat gets titled.
	MaybeAutoTitle(database, gen, ch, "how do I read this invoice?", "Here is how…")
	waitFor(t, func() bool {
		got, _ := database.GetChat("c1")
		return got != nil && got.Name == "Invoice Questions"
	})

	// Already titled → never re-titled.
	ch2, _ := database.GetChat("c1")
	MaybeAutoTitle(database, func(_ context.Context, _, _, _ string) (string, error) {
		t.Fatal("generator must not run for an already-titled chat")
		return "", nil
	}, ch2, "another question", "another answer")

	// Attachment-confirmation first turn → skipped (stays default).
	ch3 := &db.Chat{ID: "c2", WorkspaceID: ws, Name: "Chat 2026-07-23 16:00", Active: true}
	if err := database.CreateChat(ch3); err != nil {
		t.Fatal(err)
	}
	MaybeAutoTitle(database, func(_ context.Context, _, _, _ string) (string, error) {
		t.Fatal("generator must not run for an attachment-confirmation turn")
		return "", nil
	}, ch3, "📎 Attached **invoice.pdf** to my knowledge base as `notes/invoice.md`.", "Got it.")
}

func TestMaybeAutoTitle_RecoversPanic(t *testing.T) {
	database := newTestDB(t)
	ws := seedWorkspace(t, database)
	ch := &db.Chat{ID: "cpanic", WorkspaceID: ws, Name: "Chat 2026-07-23 15:04", Active: true}
	if err := database.CreateChat(ch); err != nil {
		t.Fatal(err)
	}
	// A generator that panics must NOT crash the process; the chat keeps its
	// default name and MaybeAutoTitle returns normally.
	MaybeAutoTitle(database, func(_ context.Context, _, _, _ string) (string, error) {
		panic("boom")
	}, ch, "real question", "real answer")
	// Give the goroutine time to run and recover.
	time.Sleep(300 * time.Millisecond)
	got, err := database.GetChat("cpanic")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Chat 2026-07-23 15:04" {
		t.Errorf("name changed after panic: %q", got.Name)
	}
}
