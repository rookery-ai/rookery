package gateway

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rookery-ai/rookery/internal/db"
)

const traceChat = "1001"

// newTraceManager runs messages through the real dispatcher and router, against a
// fake platform.
func newTraceManager(t *testing.T, onText TextHandler, onRun AgentRunHandler) (*GatewayManager, *fakeTypingGateway) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.CreateWorkspace(&db.Workspace{ID: "ws1", Name: "Test"}); err != nil {
		t.Fatal(err)
	}
	if err := database.LinkPlatformIdentity(&db.PlatformIdentity{
		ID: "link", WorkspaceID: "ws1", Platform: "telegram", PlatformUserID: traceChat,
	}); err != nil {
		t.Fatal(err)
	}
	m := New(database, []byte("k"), NewRouter(database, onText, onRun, nil, nil))
	gw := &fakeTypingGateway{}
	m.gateways[key("telegram", "ws1")] = gw
	return m, gw
}

func waitForEdit(t *testing.T, gw *fakeTypingGateway, substr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, edits := gw.snapshot()
		for _, e := range edits {
			if strings.Contains(e, substr) {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	_, edits := gw.snapshot()
	t.Fatalf("no edit containing %q in %q", substr, edits)
}

func TestChatTurnShowsStepsThenTheAnswer(t *testing.T) {
	var gw *fakeTypingGateway
	onText := func(_ context.Context, _ string, _ []db.ChatMessage, _ string, send, onStep func(string)) error {
		onStep("🔧 search_files(report)")
		waitForEdit(t, gw, "Searching knowledge base")
		onStep("🔧 read_file(notes/a.md)") // throttled, still pending at the answer
		send("the answer")
		return nil
	}
	m, g := newTraceManager(t, onText, nil)
	gw = g

	m.dispatch(context.Background(), Message{
		Platform: "telegram", PlatformUserID: traceChat, WorkspaceID: "ws1", Text: "find it",
	})

	sent, edits := gw.snapshot()
	if len(sent) != 1 {
		t.Errorf("new messages = %q, want only the placeholder", sent)
	}
	if last := edits[len(edits)-1]; last != "the answer" {
		t.Errorf("final edit = %q", last)
	}

	// The pending flush must not land after the answer.
	time.Sleep(stepTraceInterval + 200*time.Millisecond)
	sentAfter, editsAfter := gw.snapshot()
	if len(sentAfter) != len(sent) || len(editsAfter) != len(edits) {
		t.Errorf("trace kept writing after the answer: sent %q, edits %q", sentAfter, editsAfter)
	}
}

func TestPlaceholderOpensWithAWaitingPhrase(t *testing.T) {
	onText := func(_ context.Context, _ string, _ []db.ChatMessage, _ string, send, _ func(string)) error {
		send("the answer")
		return nil
	}
	m, gw := newTraceManager(t, onText, nil)
	m.dispatch(context.Background(), Message{
		Platform: "telegram", PlatformUserID: traceChat, WorkspaceID: "ws1", Text: "hello",
	})
	sent, edits := gw.snapshot()
	if len(sent) != 1 {
		t.Fatalf("sent = %q", sent)
	}
	matched := false
	for i := range waitingPhrases {
		matched = matched || sent[0] == waitingLine(i)
	}
	if !matched {
		t.Errorf("placeholder = %q", sent[0])
	}
	if len(edits) != 1 || edits[0] != "the answer" {
		t.Errorf("edits = %q", edits)
	}
}

func TestRunTraceKeepsHeaderAndHidesCommands(t *testing.T) {
	var gw *fakeTypingGateway
	onRun := func(_ context.Context, _, _ string, send, onStep func(string)) error {
		onStep("🔧 bash(curl https://example.com/?token=abc)")
		waitForEdit(t, gw, "Running a command")
		send("the report")
		return nil
	}
	m, g := newTraceManager(t, nil, onRun)
	gw = g
	m.dispatch(context.Background(), Message{
		Platform: "telegram", PlatformUserID: traceChat, WorkspaceID: "ws1", Text: "/run weather",
	})

	sent, edits := gw.snapshot()
	if len(sent) != 1 || !strings.Contains(sent[0], "Running agent **weather**") {
		t.Errorf("sent = %q", sent)
	}
	if !strings.Contains(edits[0], "Running agent **weather**") {
		t.Errorf("list lost its header: %q", edits[0])
	}
	if strings.Contains(strings.Join(edits, "\n"), "abc") {
		t.Errorf("a command reached the chat: %q", edits)
	}
	if last := edits[len(edits)-1]; last != "the report" {
		t.Errorf("final edit = %q", last)
	}
}

func TestSilentRunReplacesTheListInPlace(t *testing.T) {
	var gw *fakeTypingGateway
	onRun := func(_ context.Context, _, _ string, _, onStep func(string)) error {
		onStep("🔧 web_search(topic)")
		waitForEdit(t, gw, "Searching the web")
		return nil
	}
	m, g := newTraceManager(t, nil, onRun)
	gw = g
	m.dispatch(context.Background(), Message{
		Platform: "telegram", PlatformUserID: traceChat, WorkspaceID: "ws1", Text: "/run weather",
	})
	_, edits := gw.snapshot()
	if last := edits[len(edits)-1]; !strings.Contains(last, "no notification") || strings.Contains(last, "Search") {
		t.Errorf("final edit = %q", last)
	}
}
