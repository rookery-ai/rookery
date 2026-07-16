package gateway

import "testing"

// fakeTyping asserts the TypingGateway/DeletableGateway interfaces are string-based.
type fakeTyping struct{ lastID string }

func (f *fakeTyping) SendTyping(string) error                      { return nil }
func (f *fakeTyping) SendMessageGetID(_, _ string) (string, error) { return "msg-123", nil }
func (f *fakeTyping) EditMessage(_, msgID, _ string) error         { f.lastID = msgID; return nil }
func (f *fakeTyping) DeleteMessage(_, msgID string) error          { f.lastID = msgID; return nil }

func TestMessageIDsAreStrings(t *testing.T) {
	var tg TypingGateway = &fakeTyping{}
	id, _ := tg.SendMessageGetID("u", "hi")
	if id != "msg-123" {
		t.Fatalf("want string id, got %q", id)
	}
	var dg DeletableGateway = &fakeTyping{}
	if err := dg.DeleteMessage("u", "snowflake-999"); err != nil {
		t.Fatal(err)
	}
	var m Message
	m.MessageID = "abc" // must compile as string
	_ = m
}
