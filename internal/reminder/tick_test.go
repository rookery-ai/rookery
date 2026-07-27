package reminder

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/db"
)

// stubSender records what was sent and can be made to fail.
type stubSender struct {
	sent []string
	err  error
}

func (s *stubSender) SendToUser(workspaceID, text string) error {
	if s.err != nil {
		return s.err
	}
	s.sent = append(s.sent, text)
	return nil
}

// tickTestDB opens a migrated DB with one workspace and one already-due reminder.
// No platform identity is created — that is the condition under test.
func tickTestDB(t *testing.T) (*db.DB, string, string) {
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
	reminderID := uuid.New().String()
	if err := database.CreateReminder(&db.Reminder{
		ID:          reminderID,
		WorkspaceID: workspaceID,
		Message:     "call the dentist",
		RemindAt:    time.Now().Add(-time.Minute),
		CreatedAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create reminder: %v", err)
	}
	return database, workspaceID, reminderID
}

func inboxBodies(t *testing.T, database *db.DB, workspaceID string) []string {
	t.Helper()
	msgs, err := database.ListInboxMessages(workspaceID, 50, 0)
	if err != nil {
		t.Fatalf("list inbox: %v", err)
	}
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.Body)
	}
	return out
}

// TestTickDeliversToInboxWithoutChatPlatform is the reported bug: with no chat app
// connected, reminders never appeared anywhere. tick() skipped the whole iteration on a
// missing platform identity, which bypassed BOTH recordInbox and MarkReminderSent — so the
// reminder was invisible AND stayed due forever, re-firing on every tick.
//
// The inbox is a real delivery channel, so it must receive the reminder regardless.
func TestTickDeliversToInboxWithoutChatPlatform(t *testing.T) {
	database, workspaceID, reminderID := tickTestDB(t)
	sender := &stubSender{}
	s := New(database, sender)

	s.tick()

	bodies := inboxBodies(t, database, workspaceID)
	if len(bodies) != 1 {
		t.Fatalf("expected exactly 1 inbox message with no chat platform connected, got %d: %v", len(bodies), bodies)
	}
	if want := "call the dentist"; !strings.Contains(bodies[0], want) {
		t.Errorf("inbox body must carry the reminder text %q; got %q", want, bodies[0])
	}
	if len(sender.sent) != 0 {
		t.Errorf("must not attempt a chat send with no platform identity; sent %v", sender.sent)
	}

	// Marked sent, so the next tick does not re-fire it. This is the quiet half of the
	// bug: without it the reminder is re-delivered on every 60s poll forever.
	assertNotDue(t, database, reminderID)

	s.tick()
	if got := inboxBodies(t, database, workspaceID); len(got) != 1 {
		t.Errorf("a delivered reminder must not re-fire on the next tick; inbox now has %d messages", len(got))
	}
}

// TestTickDeliversToInboxWhenChatSendFails covers the second early exit: a `continue` on
// SendToUser error, which bypassed the same two calls even for a user WITH a chat app.
// A transient send failure must not cost the user the reminder or wedge it re-firing.
func TestTickDeliversToInboxWhenChatSendFails(t *testing.T) {
	database, workspaceID, reminderID := tickTestDB(t)
	// A platform identity exists, so the send is attempted — and fails.
	linkIdentity(t, database, workspaceID)
	s := New(database, &stubSender{err: errors.New("telegram unreachable")})

	s.tick()

	if got := inboxBodies(t, database, workspaceID); len(got) != 1 {
		t.Fatalf("a failed chat send must still deliver to the inbox; got %d messages", len(got))
	}
	assertNotDue(t, database, reminderID)
}

// TestTickSendsToChatWhenConnected guards the unchanged happy path: a connected platform
// still receives the message, and the inbox copy is additional, not a replacement.
func TestTickSendsToChatWhenConnected(t *testing.T) {
	database, workspaceID, _ := tickTestDB(t)
	linkIdentity(t, database, workspaceID)
	sender := &stubSender{}
	s := New(database, sender)

	s.tick()

	if len(sender.sent) != 1 {
		t.Fatalf("connected user must receive the chat message; sent %d", len(sender.sent))
	}
	if got := inboxBodies(t, database, workspaceID); len(got) != 1 {
		t.Errorf("connected user must ALSO get the inbox copy; got %d", len(got))
	}
}

// linkIdentity gives the workspace a connected chat platform.
func linkIdentity(t *testing.T, database *db.DB, workspaceID string) {
	t.Helper()
	if err := database.UpsertPlatformIdentity(&db.PlatformIdentity{
		ID: uuid.New().String(), WorkspaceID: workspaceID,
		Platform: "telegram", PlatformUserID: "12345", LinkedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("link identity: %v", err)
	}
}

func assertNotDue(t *testing.T, database *db.DB, reminderID string) {
	t.Helper()
	due, err := database.ListDueReminders(time.Now())
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	for _, r := range due {
		if r.ID == reminderID {
			t.Fatal("reminder must be marked sent after delivery — it is still due and will re-fire every tick")
		}
	}
}
