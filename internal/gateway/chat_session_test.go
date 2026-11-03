package gateway_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/gateway"
)

// fakeCoder records the history each turn was handed and replies "reply N".
type fakeCoder struct {
	histories [][]db.ChatMessage
}

func (f *fakeCoder) handler() gateway.TextHandler {
	return func(_ context.Context, _ string, history []db.ChatMessage, text string, send func(string)) error {
		f.histories = append(f.histories, history)
		send("reply to " + text)
		return nil
	}
}

func newChatRouter(t *testing.T) (*gateway.Router, *db.DB, *fakeCoder) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.CreateWorkspace(&db.Workspace{ID: testWorkspaceID, Name: "chat-tester"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	fc := &fakeCoder{}
	return gateway.NewRouter(database, fc.handler(), nil, nil, nil), database, fc
}

func say(t *testing.T, r *gateway.Router, text string) []string {
	t.Helper()
	send, got := collect()
	if err := r.Handle(context.Background(), testMsg(text), send, func() {}, send, send); err != nil {
		t.Fatalf("handle %q: %v", text, err)
	}
	return *got
}

func platformChats(t *testing.T, database *db.DB) []*db.Chat {
	t.Helper()
	all, err := database.ListChats(testWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	return all
}

// ageChat pushes a chat's last_seen past the idle timeout.
func ageChat(t *testing.T, database *db.DB, id string, ago time.Duration) {
	t.Helper()
	ts := time.Now().Add(-ago).UTC().Format("2006-01-02 15:04:05")
	if _, err := database.Exec(`UPDATE chats SET last_seen=? WHERE id=?`, ts, id); err != nil {
		t.Fatal(err)
	}
}

// The reported bug: a plain message with no chat was a one-off — no history,
// nothing saved. Now it opens a chat and the next message sees the first.
func TestPlainMessageOpensAChatAndKeepsHistory(t *testing.T) {
	r, database, fc := newChatRouter(t)

	say(t, r, "hello")
	say(t, r, "what did I just say?")

	chats := platformChats(t, database)
	if len(chats) != 1 {
		t.Fatalf("want 1 chat, got %d", len(chats))
	}
	c := chats[0]
	if !c.Active || c.Platform != "telegram" {
		t.Fatalf("chat should be active on telegram: %+v", c)
	}
	if !strings.HasPrefix(c.Name, "Chat ") {
		t.Errorf("an auto-opened chat must carry the default name the auto-titler recognises, got %q", c.Name)
	}
	if len(fc.histories[0]) != 0 {
		t.Errorf("first turn must see no history, got %d messages", len(fc.histories[0]))
	}
	if len(fc.histories[1]) != 2 || fc.histories[1][0].Content != "hello" {
		t.Errorf("second turn must see the first exchange, got %+v", fc.histories[1])
	}
	msgs, _ := database.ListChatMessages(c.ID)
	if len(msgs) != 4 {
		t.Errorf("want 4 stored messages, got %d", len(msgs))
	}
}

// After 30 minutes of silence the next message starts a new chat, and its reply
// opens by saying so — once.
func TestIdleChatEndsAndTheOwnerIsToldOnce(t *testing.T) {
	r, database, fc := newChatRouter(t)

	say(t, r, "first conversation")
	old := platformChats(t, database)[0]
	ageChat(t, database, old.ID, 31*time.Minute)

	got := say(t, r, "a new topic")
	if len(got) != 1 || !strings.Contains(got[0], "ended after 30 minutes") || !strings.Contains(got[0], old.ID[:8]) {
		t.Fatalf("reply should open with the ended notice naming how to resume, got %q", got)
	}
	if !strings.HasSuffix(got[0], "reply to a new topic") {
		t.Errorf("the coder's reply must still be delivered after the notice, got %q", got[0])
	}
	if len(fc.histories[1]) != 0 {
		t.Errorf("the new chat must not inherit the old chat's history")
	}

	prev, _ := database.GetChat(old.ID)
	if prev.Active || prev.EndedReason != db.ChatEndedIdle {
		t.Errorf("old chat should be stopped as idle, got active=%v reason=%q", prev.Active, prev.EndedReason)
	}

	// The notice is never stored as part of the new conversation.
	cur, _ := database.GetActiveChatForPlatform(testWorkspaceID, "telegram")
	msgs, _ := database.ListChatMessages(cur.ID)
	for _, m := range msgs {
		if strings.Contains(m.Content, "ended after") {
			t.Errorf("notice leaked into the transcript: %q", m.Content)
		}
	}

	if again := say(t, r, "still here"); strings.Contains(again[0], "ended after") {
		t.Errorf("notice must fire once, repeated on: %q", again[0])
	}
}

// A chat the sweeper already stopped is announced just the same.
func TestChatStoppedBySweeperIsAnnounced(t *testing.T) {
	r, database, _ := newChatRouter(t)
	say(t, r, "hello")
	old := platformChats(t, database)[0]
	if err := database.StopIdleChat(old.ID); err != nil {
		t.Fatal(err)
	}
	if got := say(t, r, "back again"); !strings.Contains(got[0], "ended after") {
		t.Errorf("want the ended notice, got %q", got[0])
	}
}

// /chat stop already confirmed the ending, so no inactivity notice follows.
func TestUserStoppedChatIsNotAnnouncedAsIdle(t *testing.T) {
	r, database, _ := newChatRouter(t)
	say(t, r, "hello")
	say(t, r, "/chat stop")
	ageChat(t, database, platformChats(t, database)[0].ID, 2*time.Hour)

	got := say(t, r, "new thing")
	if strings.Contains(got[0], "ended after") {
		t.Errorf("a chat the owner stopped must not be announced as idle-ended: %q", got[0])
	}
	if len(platformChats(t, database)) != 2 {
		t.Errorf("the message after /chat stop should open a new chat")
	}
}

// /chat starts a fresh chat; the next message continues THAT chat.
func TestChatCommandStartsFresh(t *testing.T) {
	r, database, fc := newChatRouter(t)
	say(t, r, "hello")
	say(t, r, "/chat")
	say(t, r, "after reset")

	if len(fc.histories[1]) != 0 {
		t.Errorf("a message after /chat must start with no history, got %d", len(fc.histories[1]))
	}
	cur, _ := database.GetActiveChatForPlatform(testWorkspaceID, "telegram")
	if !strings.HasPrefix(cur.Name, "Chat ") {
		t.Errorf("an unnamed /chat start should get the default name, got %q", cur.Name)
	}
	if len(platformChats(t, database)) != 2 {
		t.Errorf("want 2 chats")
	}
}

// /start links the account and must never open a chat or reach the coder.
func TestStartCommandOpensNoChat(t *testing.T) {
	r, database, fc := newChatRouter(t)
	got := say(t, r, "/start")
	if len(got) != 1 || !strings.Contains(got[0], "linked") {
		t.Fatalf("/start should link, got %q", got)
	}
	if n := len(platformChats(t, database)); n != 0 {
		t.Errorf("/start must not open a chat, got %d", n)
	}
	if len(fc.histories) != 0 {
		t.Errorf("/start must not reach the coder")
	}
}

// A chat with no stored messages is not worth announcing.
func TestEmptyIdleChatIsNotAnnounced(t *testing.T) {
	r, database, _ := newChatRouter(t)
	say(t, r, "/chat start")
	c := platformChats(t, database)[0]
	ageChat(t, database, c.ID, time.Hour)
	if got := say(t, r, "hi"); strings.Contains(got[0], "ended after") {
		t.Errorf("an empty chat must not be announced: %q", got[0])
	}
}

// /chat resume of a chat started elsewhere (the web) must make it THIS app's
// live chat — otherwise the next message opens a new chat and the resume
// silently does nothing.
func TestResumeOfAWebChatContinuesItHere(t *testing.T) {
	r, database, fc := newChatRouter(t)
	web := &db.Chat{ID: "webchat0-1111-2222-3333-444444444444", WorkspaceID: testWorkspaceID, Name: "From the web", Platform: "web"}
	if err := database.CreateChat(web); err != nil {
		t.Fatal(err)
	}
	_ = database.AddChatMessage(web.ID, "user", "web question")
	_ = database.AddChatMessage(web.ID, "assistant", "web answer")
	_ = database.StopChat(web.ID)

	say(t, r, "/chat resume webchat0")
	say(t, r, "follow-up")

	if len(fc.histories) != 1 || len(fc.histories[0]) != 2 {
		t.Fatalf("the follow-up must continue the resumed chat's history, got %+v", fc.histories)
	}
	if n := len(platformChats(t, database)); n != 1 {
		t.Errorf("resume must not be followed by a new chat, got %d chats", n)
	}
}
