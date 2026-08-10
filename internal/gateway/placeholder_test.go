package gateway

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ilijad1/rookery/internal/db"
)

// fakeTypingGateway records what a platform actually received, so the
// placeholder lifecycle can be asserted end to end rather than inferred.
type fakeTypingGateway struct {
	mu     sync.Mutex
	sent   []string // messages posted as NEW messages
	edits  []string // edits applied to the placeholder
	nextID int
}

func (f *fakeTypingGateway) Platform() string                       { return "discord" }
func (f *fakeTypingGateway) OwnerUserID() string                    { return "ws1" }
func (f *fakeTypingGateway) Start(ctx context.Context) error        { return nil }
func (f *fakeTypingGateway) Stop() error                            { return nil }
func (f *fakeTypingGateway) SendTyping(platformUserID string) error { return nil }

func (f *fakeTypingGateway) Send(platformUserID, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, text)
	return nil
}

func (f *fakeTypingGateway) SendMessageGetID(platformUserID, text string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, text)
	f.nextID++
	return "msg-1", nil
}

func (f *fakeTypingGateway) EditMessage(platformUserID, msgID, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.edits = append(f.edits, text)
	return nil
}

func (f *fakeTypingGateway) snapshot() ([]string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.sent...), append([]string{}, f.edits...)
}

// recordingRouter drives the dispatch closures the way the real router does for a
// turn that starts a detached build: one progress line, then milestones arriving
// later from another goroutine.
type recordingRouter struct {
	run func(send func(string), sendProgress func(string))
}

func (r *recordingRouter) Handle(ctx context.Context, msg Message, send func(string),
	deleteIncoming func(), sendAutoDelete func(string), sendProgress func(string)) error {
	r.run(send, sendProgress)
	return nil
}

func newDispatchHarness(t *testing.T, run func(send, sendProgress func(string))) (*GatewayManager, *fakeTypingGateway) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.CreateWorkspace(&db.Workspace{ID: "ws1", Name: "w"}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertPlatformIdentity(&db.PlatformIdentity{
		ID: "id-1", WorkspaceID: "ws1", Platform: "discord", PlatformUserID: "u1",
	}); err != nil {
		t.Fatal(err)
	}

	fake := &fakeTypingGateway{}
	m := New(database, []byte("k"), nil)
	m.router = &recordingRouter{run: run}
	m.gateways[key("discord", "ws1")] = fake
	return m, fake
}

// TestProgressStillReachesTheUserAfterTheBuildingNotice is the regression this
// nearly shipped.
//
// send() consumes the placeholder on a successful edit, and every build milestone
// goes through updatePlaceholder. While generation was synchronous, send() ran
// only AFTER the build, so the placeholder survived the whole build and
// milestones landed. Detached, the turn returns a "building…" line immediately —
// pushing that through send() would consume the placeholder before the first
// milestone and the user would see the notice followed by silence, which is the
// exact complaint ("we are missing outputs and actions") this work addresses.
func TestProgressStillReachesTheUserAfterTheBuildingNotice(t *testing.T) {
	m, fake := newDispatchHarness(t, func(send, sendProgress func(string)) {
		// The router's design branch for a turn that started a build.
		sendProgress("🤖 Building your agent…")
		// Milestones from the detached build goroutine, after the turn returned.
		done := make(chan struct{})
		go func() {
			defer close(done)
			sendProgress("🔧 web_search(...)")
			sendProgress("🔧 write_file(...)")
		}()
		<-done
	})

	m.dispatch(context.Background(), Message{
		Platform: "discord", PlatformUserID: "u1", WorkspaceID: "ws1", Text: "approve",
	})

	sent, edits := fake.snapshot()
	all := append(append([]string{}, sent...), edits...)
	for _, want := range []string{"Building your agent", "web_search", "write_file"} {
		found := false
		for _, got := range all {
			if strings.Contains(got, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%q never reached the user; sent=%v edits=%v", want, sent, edits)
		}
	}
}

// send() must still consume the placeholder for an ordinary reply — that is what
// turns "⏳ Thinking..." into the answer instead of leaving a stray message.
func TestSendConsumesThePlaceholderForAnOrdinaryReply(t *testing.T) {
	m, fake := newDispatchHarness(t, func(send, sendProgress func(string)) {
		send("here is your answer")
		send("and a second message")
	})

	m.dispatch(context.Background(), Message{
		Platform: "discord", PlatformUserID: "u1", WorkspaceID: "ws1", Text: "hello",
	})

	sent, edits := fake.snapshot()
	if len(edits) == 0 || !strings.Contains(edits[0], "here is your answer") {
		t.Errorf("first reply should EDIT the placeholder; edits=%v", edits)
	}
	// The placeholder is consumed, so the second reply is a new message.
	found := false
	for _, s := range sent {
		if strings.Contains(s, "and a second message") {
			found = true
		}
	}
	if !found {
		t.Errorf("second reply should be a NEW message; sent=%v edits=%v", sent, edits)
	}
}

// A build begun by a command creates no placeholder ("/" messages skip it), so
// updatePlaceholder must CREATE one rather than dropping the text.
func TestProgressCreatesAPlaceholderWhenNoneExists(t *testing.T) {
	m, fake := newDispatchHarness(t, func(send, sendProgress func(string)) {
		sendProgress("🤖 Building your agent…")
		sendProgress("🔧 run_script(...)")
	})

	m.dispatch(context.Background(), Message{
		// A "/" message: dispatch creates no placeholder for it.
		Platform: "discord", PlatformUserID: "u1", WorkspaceID: "ws1", Text: "/agent create x",
	})

	sent, edits := fake.snapshot()
	if len(sent) == 0 {
		t.Fatalf("progress with no placeholder must post a message; sent=%v edits=%v", sent, edits)
	}
	if !strings.Contains(sent[0], "Building your agent") {
		t.Errorf("first progress line should be posted; sent=%v", sent)
	}
	// And the next milestone edits it rather than posting again.
	if len(edits) == 0 || !strings.Contains(edits[0], "run_script") {
		t.Errorf("later milestones should edit in place; sent=%v edits=%v", sent, edits)
	}
}
