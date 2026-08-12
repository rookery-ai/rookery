package gateway

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/db"
)

// panicRouter is a messageHandler stub that panics when handling a message
// whose text matches panicOn (or always, if panicOn is empty). It stands in
// for a real *Router with a bug — a nil deref, a bad type assertion, a
// render bug — without needing to coax the real Router (which needs a live
// DB, coder, agent designer, ...) into panicking for real.
type panicRouter struct {
	panicOn string
	calls   int
}

func (p *panicRouter) Handle(ctx context.Context, msg Message, send func(string), deleteIncoming func(), sendAutoDelete func(string), sendProgress func(string)) error {
	p.calls++
	if p.panicOn == "" || msg.Text == p.panicOn {
		panic("boom: simulated handler panic for " + msg.Text)
	}
	send("ok: " + msg.Text)
	return nil
}

// fakeGateway is a minimal Gateway that records every outbound Send, standing
// in for a real adapter (Telegram/Discord/Slack) so tests can assert on what
// would have reached the user without a live bot connection.
type fakeGateway struct {
	platform string
	sent     []string
}

func (f *fakeGateway) Platform() string                { return f.platform }
func (f *fakeGateway) OwnerUserID() string             { return "" }
func (f *fakeGateway) Start(ctx context.Context) error { return nil }
func (f *fakeGateway) Stop() error                     { return nil }
func (f *fakeGateway) Send(platformUserID, text string) error {
	f.sent = append(f.sent, text)
	return nil
}

// newDispatchTestManager builds a GatewayManager backed by a real temp DB
// (so identity resolution in dispatch() behaves exactly as it does in
// production) with the given messageHandler stub and a fakeGateway wired up
// as the active gateway for platform+workspaceID, plus a linked identity so
// the sender is treated as the workspace owner.
func newDispatchTestManager(t *testing.T, router messageHandler, platform, workspaceID, platformUserID string) (*GatewayManager, *fakeGateway) {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	if err := database.CreateWorkspace(&db.Workspace{ID: workspaceID, Name: "dispatch-recover-test"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := database.UpsertPlatformIdentity(&db.PlatformIdentity{
		ID:             "ident-1",
		WorkspaceID:    workspaceID,
		Platform:       platform,
		PlatformUserID: platformUserID,
	}); err != nil {
		t.Fatalf("link identity: %v", err)
	}

	gw := &fakeGateway{platform: platform}
	m := &GatewayManager{
		db:       database,
		router:   router,
		gateways: map[string]Gateway{key(platform, workspaceID): gw},
		cancels:  map[string]context.CancelFunc{},
	}
	return m, gw
}

// TestDispatchRecoversFromPanic is the core regression guard for this fix:
// GatewayManager.dispatch is the single funnel every adapter (Telegram,
// Discord, Slack) calls for every inbound message. Before the recover() was
// added, a panic anywhere in the router path (or dispatch itself) had
// nothing standing between it and crashing the whole process.
func TestDispatchRecoversFromPanic(t *testing.T) {
	router := &panicRouter{}
	m, gw := newDispatchTestManager(t, router, "telegram", "ws-1", "user-1")

	msg := Message{Platform: "telegram", WorkspaceID: "ws-1", PlatformUserID: "user-1", Text: "hello"}

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic escaped dispatchFunc's closure: %v", r)
			}
		}()
		m.dispatchFunc()(t.Context(), msg)
	}()

	if router.calls != 1 {
		t.Fatalf("router.Handle calls = %d, want 1", router.calls)
	}
	if len(gw.sent) != 1 {
		t.Fatalf("want exactly 1 reply attempted (the generic error), got %d: %v", len(gw.sent), gw.sent)
	}
	if !strings.Contains(gw.sent[0], "Something went wrong") {
		t.Errorf("reply = %q, want a generic error message", gw.sent[0])
	}
}

// TestDispatchSurvivesPanicForSubsequentMessage asserts the manager (and by
// extension the adapter's read loop, which keeps calling this same closure)
// is still fully usable after a panicking message — a normal message right
// after gets routed and replied to as usual.
func TestDispatchSurvivesPanicForSubsequentMessage(t *testing.T) {
	router := &panicRouter{panicOn: "boom"}
	m, gw := newDispatchTestManager(t, router, "telegram", "ws-1", "user-1")

	panicMsg := Message{Platform: "telegram", WorkspaceID: "ws-1", PlatformUserID: "user-1", Text: "boom"}
	okMsg := Message{Platform: "telegram", WorkspaceID: "ws-1", PlatformUserID: "user-1", Text: "hello"}

	m.dispatchFunc()(t.Context(), panicMsg)
	m.dispatchFunc()(t.Context(), okMsg)

	if router.calls != 2 {
		t.Fatalf("router.Handle calls = %d, want 2 (both messages reached the router)", router.calls)
	}
	if len(gw.sent) != 2 {
		t.Fatalf("want 2 sends (panic-recovery reply + normal reply), got %d: %v", len(gw.sent), gw.sent)
	}
	if !strings.Contains(gw.sent[0], "Something went wrong") {
		t.Errorf("first send = %q, want the generic panic-recovery reply", gw.sent[0])
	}
	if gw.sent[1] != "ok: hello" {
		t.Errorf("second send = %q, want the normal router reply for the follow-up message", gw.sent[1])
	}
}

// TestDispatchPanicReplySendItselfCannotEscape covers the guard specified in
// the task: a panic INSIDE the best-effort error-reply send must not itself
// escape recover() and crash the process.
func TestDispatchPanicReplySendItselfCannotEscape(t *testing.T) {
	router := &panicRouter{}
	m, _ := newDispatchTestManager(t, router, "telegram", "ws-1", "user-1")

	// Replace the fake gateway with one whose Send itself panics, simulating
	// a misbehaving adapter implementation.
	m.mu.Lock()
	m.gateways[key("telegram", "ws-1")] = &panicSendGateway{}
	m.mu.Unlock()

	msg := Message{Platform: "telegram", WorkspaceID: "ws-1", PlatformUserID: "user-1", Text: "hello"}

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic escaped dispatchFunc's closure even though it originated in the recovery reply itself: %v", r)
			}
		}()
		m.dispatchFunc()(t.Context(), msg)
	}()
}

type panicSendGateway struct{}

func (panicSendGateway) Platform() string                { return "telegram" }
func (panicSendGateway) OwnerUserID() string             { return "" }
func (panicSendGateway) Start(ctx context.Context) error { return nil }
func (panicSendGateway) Stop() error                     { return nil }
func (panicSendGateway) Send(platformUserID, text string) error {
	panic("boom: simulated panic inside Send itself")
}
