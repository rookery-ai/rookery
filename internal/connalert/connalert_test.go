package connalert

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/rookery-ai/rookery/internal/db"
)

type fakeSender struct {
	sent []string
	err  error
}

func (f *fakeSender) SendToUser(workspaceID, message string) error {
	f.sent = append(f.sent, message)
	return f.err
}

func testDB(t *testing.T) (*db.DB, string) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	ws := uuid.New().String()
	if err := d.CreateWorkspace(&db.Workspace{ID: ws, Name: "tester"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	return d, ws
}

func TestNeedsReauthWritesInboxAndSendsChat(t *testing.T) {
	d, ws := testDB(t)
	sender := &fakeSender{}
	New(d, sender).ConnectionNeedsReauth(ws, "conn-1", "Gmail", "work")

	msgs, err := d.ListInboxMessages(ws, 10, 0)
	if err != nil {
		t.Fatalf("list inbox: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d inbox messages, want 1", len(msgs))
	}
	m := msgs[0]
	if m.Source != "connection" {
		t.Fatalf("source = %q, want connection", m.Source)
	}
	if m.Status != "error" {
		t.Fatalf("status = %q, want error", m.Status)
	}
	if m.AgentID != "" {
		t.Fatalf("agent_id = %q, want empty", m.AgentID)
	}
	if m.RefID != "conn-1" {
		t.Fatalf("ref_id = %q, want conn-1", m.RefID)
	}
	for _, want := range []string{"Gmail", "work", "Action required"} {
		if !strings.Contains(m.Body, want) {
			t.Fatalf("body %q missing %q", m.Body, want)
		}
	}
	if len(sender.sent) != 1 {
		t.Fatalf("got %d chat sends, want 1", len(sender.sent))
	}
	if !strings.Contains(sender.sent[0], "Gmail") {
		t.Fatalf("chat message %q does not name the provider", sender.sent[0])
	}
}

// The inbox row must survive a chat failure, because a workspace with no chat
// platform linked errors on EVERY send — and the inbox is precisely that
// user's only surface. Ordering the write second would hand the failure to the
// people who depend on it most.
func TestNeedsReauthWritesInboxEvenWhenChatFails(t *testing.T) {
	d, ws := testDB(t)
	sender := &fakeSender{err: errors.New("no platform connected")}
	New(d, sender).ConnectionNeedsReauth(ws, "conn-1", "Gmail", "work")

	msgs, _ := d.ListInboxMessages(ws, 10, 0)
	if len(msgs) != 1 {
		t.Fatalf("got %d inbox messages, want 1 despite the chat failure", len(msgs))
	}
}

func TestNilSenderStillWritesInbox(t *testing.T) {
	d, ws := testDB(t)
	New(d, nil).ConnectionNeedsReauth(ws, "conn-1", "Gmail", "work")

	msgs, _ := d.ListInboxMessages(ws, 10, 0)
	if len(msgs) != 1 {
		t.Fatalf("got %d inbox messages, want 1", len(msgs))
	}
}
