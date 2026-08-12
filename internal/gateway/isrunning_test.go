package gateway

import (
	"context"
	"testing"

	"github.com/rookery-ai/rookery/internal/db"
)

// fakeRunningGW is a minimal Gateway whose Start blocks until its context is
// cancelled — the shape a real adapter has, so start/stop bookkeeping is
// exercised rather than raced past by an immediately-returning Start.
type fakeRunningGW struct {
	platform string
	ws       string
	stopped  chan struct{}
}

func (f *fakeRunningGW) Platform() string    { return f.platform }
func (f *fakeRunningGW) OwnerUserID() string { return f.ws }
func (f *fakeRunningGW) Start(ctx context.Context) error {
	<-ctx.Done()
	return nil
}
func (f *fakeRunningGW) Stop() error {
	select {
	case <-f.stopped:
	default:
		close(f.stopped)
	}
	return nil
}
func (f *fakeRunningGW) Send(_, _ string) error { return nil }

// TestIsRunningReflectsStartAndStop pins the liveness signal the connections
// UI reports as bot_online. Without it a saved connection whose server is down
// is indistinguishable from one merely waiting for /start — the exact
// ambiguity that made a dead server look like a misconfigured Discord app.
//
// GatewayManager is constructed directly rather than via New(): start() never
// touches m.db (only dispatch does), so a nil DB is sufficient here and avoids
// standing up a real database for what is bookkeeping over one map.
func TestIsRunningReflectsStartAndStop(t *testing.T) {
	key := make([]byte, 32)
	tok, err := EncryptToken("t0ken", key)
	if err != nil {
		t.Fatalf("encrypt token: %v", err)
	}

	RegisterAdapter("fakeplat", func(_, _, ws string, _ DispatchFunc) (Gateway, error) {
		return &fakeRunningGW{platform: "fakeplat", ws: ws, stopped: make(chan struct{})}, nil
	})

	m := &GatewayManager{
		systemKey: key,
		gateways:  map[string]Gateway{},
		cancels:   map[string]context.CancelFunc{},
	}

	if m.IsRunning("ws1", "fakeplat") {
		t.Fatal("expected not running before start")
	}

	conn := &db.PlatformConnection{
		WorkspaceID:    "ws1",
		Platform:       "fakeplat",
		EncryptedToken: tok,
		Active:         true,
	}
	if err := m.start(context.Background(), conn); err != nil {
		t.Fatalf("start: %v", err)
	}

	if !m.IsRunning("ws1", "fakeplat") {
		t.Fatal("expected running after start")
	}
	// Liveness is per workspace+platform, not per platform: two workspaces can
	// each hold their own bot for the same platform.
	if m.IsRunning("ws2", "fakeplat") {
		t.Fatal("a different workspace must not report running")
	}
	if m.IsRunning("ws1", "otherplat") {
		t.Fatal("a different platform must not report running")
	}

	m.stop("ws1", "fakeplat")
	if m.IsRunning("ws1", "fakeplat") {
		t.Fatal("expected not running after stop")
	}
}
