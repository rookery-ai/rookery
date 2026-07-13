package db_test

import (
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/db"
)

// inboxTestDB opens a fresh migrated DB with one workspace + one agent (FK
// targets) and returns the agentID and workspaceID.
func inboxTestDB(t *testing.T) (*db.DB, string, string) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"), "../../migrations")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	workspaceID := uuid.New().String()
	if err := database.CreateWorkspace(&db.Workspace{ID: workspaceID, Name: "tester"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	agentID := "agent-1"
	if err := database.CreateAgent(&db.Agent{ID: agentID, WorkspaceID: workspaceID, Name: "A", Active: true}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return database, agentID, workspaceID
}

// TestInboxCreateListRead verifies an agent-run notification is stored, listed
// newest-first, counted as unread, and markable read; a reminder notification
// (empty agent_id) stores a NULL FK without tripping foreign-key enforcement.
func TestInboxCreateListRead(t *testing.T) {
	database, agentID, workspaceID := inboxTestDB(t)

	// Two agent-run notifications + one reminder (no agent_id).
	id1 := uuid.New().String()
	id2 := uuid.New().String()
	idRem := uuid.New().String()
	for _, m := range []*db.InboxMessage{
		{ID: id1, WorkspaceID: workspaceID, Source: "agent_run", AgentID: agentID, AgentName: "A", RefID: "r1", Trigger: "manual", Body: "first", Status: "ok"},
		{ID: id2, WorkspaceID: workspaceID, Source: "agent_run", AgentID: agentID, AgentName: "A", RefID: "r2", Trigger: "cron", Body: "second", Status: "error"},
		{ID: idRem, WorkspaceID: workspaceID, Source: "reminder", RefID: "rem1", Body: "⏰ Reminder: call", Status: "ok"},
	} {
		if err := database.CreateInboxMessage(m); err != nil {
			t.Fatalf("create inbox message: %v", err)
		}
	}

	if n, err := database.UnreadInboxCount(workspaceID); err != nil || n != 3 {
		t.Fatalf("unread = %d (%v), want 3", n, err)
	}

	msgs, err := database.ListInboxMessages(workspaceID, 100, 0)
	if err != nil || len(msgs) != 3 {
		t.Fatalf("list inbox: %v (n=%d)", err, len(msgs))
	}
	// Newest first.
	if msgs[0].ID != idRem || msgs[1].ID != id2 || msgs[2].ID != id1 {
		t.Fatalf("order = %s,%s,%s; want %s,%s,%s", msgs[0].ID, msgs[1].ID, msgs[2].ID, idRem, id2, id1)
	}
	// Reminder row carries a NULL/empty agent_id (FK survived the insert).
	if msgs[0].Source != "reminder" || msgs[0].AgentID != "" {
		t.Fatalf("reminder row = %+v, want empty AgentID", msgs[0])
	}

	// Mark one read -> unread drops to 2.
	if err := database.MarkInboxRead(id2, workspaceID); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	if n, _ := database.UnreadInboxCount(workspaceID); n != 2 {
		t.Fatalf("unread after one read = %d, want 2", n)
	}
	// Re-marking is a no-op (ErrNotFound), not an error that breaks callers.
	if err := database.MarkInboxRead(id2, workspaceID); err != db.ErrNotFound {
		t.Fatalf("re-mark read err = %v, want ErrNotFound", err)
	}

	// Mark all read -> unread 0.
	if err := database.MarkAllInboxRead(workspaceID); err != nil {
		t.Fatalf("mark all read: %v", err)
	}
	if n, _ := database.UnreadInboxCount(workspaceID); n != 0 {
		t.Fatalf("unread after mark-all = %d, want 0", n)
	}
}

// TestInboxDeleteScoped proves delete + mark-read are workspace-scoped: a row in
// another workspace is invisible and cannot be mutated by ID guess.
func TestInboxDeleteScoped(t *testing.T) {
	database, agentID, wsA := inboxTestDB(t)

	// A second workspace with its own message.
	wsB := uuid.New().String()
	if err := database.CreateWorkspace(&db.Workspace{ID: wsB, Name: "b"}); err != nil {
		t.Fatalf("create workspace b: %v", err)
	}
	idB := uuid.New().String()
	if err := database.CreateInboxMessage(&db.InboxMessage{ID: idB, WorkspaceID: wsB, Source: "agent_run", AgentID: agentID, AgentName: "A", Body: "b-only", Status: "ok"}); err != nil {
		t.Fatalf("create b message: %v", err)
	}

	// Cross-workspace mark-read / delete fail (ErrNotFound), leaving the row intact.
	if err := database.MarkInboxRead(idB, wsA); err != db.ErrNotFound {
		t.Fatalf("cross-ws mark read = %v, want ErrNotFound", err)
	}
	if err := database.DeleteInboxMessage(idB, wsA); err != db.ErrNotFound {
		t.Fatalf("cross-ws delete = %v, want ErrNotFound", err)
	}
	if n, _ := database.UnreadInboxCount(wsB); n != 1 {
		t.Fatalf("wsB unread after cross-ws attempts = %d, want 1 (row untouched)", n)
	}

	// In-workspace delete works.
	idA := uuid.New().String()
	if err := database.CreateInboxMessage(&db.InboxMessage{ID: idA, WorkspaceID: wsA, Source: "agent_run", AgentID: agentID, AgentName: "A", Body: "a", Status: "ok"}); err != nil {
		t.Fatalf("create a message: %v", err)
	}
	if err := database.DeleteInboxMessage(idA, wsA); err != nil {
		t.Fatalf("delete a message: %v", err)
	}
	msgs, _ := database.ListInboxMessages(wsA, 100, 0)
	if len(msgs) != 0 {
		t.Fatalf("wsA after delete = %d msgs, want 0", len(msgs))
	}
}

// TestInboxListPagination checks limit/offset paging.
func TestInboxListPagination(t *testing.T) {
	database, agentID, workspaceID := inboxTestDB(t)
	for i := 0; i < 5; i++ {
		if err := database.CreateInboxMessage(&db.InboxMessage{
			ID: uuid.New().String(), WorkspaceID: workspaceID, Source: "agent_run",
			AgentID: agentID, AgentName: "A", Body: "m", Status: "ok",
		}); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	page1, _ := database.ListInboxMessages(workspaceID, 2, 0)
	page2, _ := database.ListInboxMessages(workspaceID, 2, 2)
	if len(page1) != 2 || len(page2) != 2 {
		t.Fatalf("page sizes = %d/%d, want 2/2", len(page1), len(page2))
	}
	// No overlap between pages.
	seen := map[string]bool{}
	for _, m := range append(page1, page2...) {
		if seen[m.ID] {
			t.Fatalf("duplicate id across pages: %s", m.ID)
		}
		seen[m.ID] = true
	}
}