package web

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/gateway"
)

// stoppableGW is a Gateway whose Start blocks until cancelled, so the manager's
// start/stop bookkeeping is exercised rather than raced past.
type stoppableGW struct{ platform, ws string }

func (f *stoppableGW) Platform() string    { return f.platform }
func (f *stoppableGW) OwnerUserID() string { return f.ws }
func (f *stoppableGW) Start(ctx context.Context) error {
	<-ctx.Done()
	return nil
}
func (f *stoppableGW) Stop() error            { return nil }
func (f *stoppableGW) Send(_, _ string) error { return nil }

// TestDisconnectActuallyStopsTheBot is the regression guard for an ordering bug
// that made Disconnect cosmetic.
//
// apiDeleteConnector called GatewayManager.Reload BEFORE deleting the row.
// Reload stops the adapter, re-reads the connection, and starts it again when
// the row is still present and active — so the bot was stopped and immediately
// RESTARTED, and the delete then removed the row out from under a live adapter.
// The bot kept its gateway session and went on receiving and answering messages
// for a connector the UI showed as disconnected, until the next server restart.
//
// Observed in production as a workspace that still produced duplicate replies
// after its connector had been removed.
func TestDisconnectActuallyStopsTheBot(t *testing.T) {
	const platform = "disc-stop"

	gateway.RegisterCredSpec(gateway.CredSpec{
		Platform: platform,
		Label:    "DiscStop",
		Fields:   []gateway.CredField{{Key: "token"}},
	})
	gateway.RegisterAdapter(platform, func(_, _, ws string, _ gateway.DispatchFunc) (gateway.Gateway, error) {
		return &stoppableGW{platform: platform, ws: ws}, nil
	})
	// The adapter registry is global, and TestBrandLogoCoverage reads it to
	// decide which platforms must ship a brand logo — leaving this fixture
	// behind makes that test demand a disc-stop.svg.
	t.Cleanup(func() { gateway.UnregisterAdapter(platform) })

	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	gm := gateway.New(s.db, s.systemKey, nil)
	s.gateway = gm

	encToken, err := gateway.EncryptToken("tok", s.systemKey)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if err := s.db.UpsertPlatformConnection(&db.PlatformConnection{
		ID: uuid.New().String(), WorkspaceID: wsID, Platform: platform,
		EncryptedToken: encToken, Active: true,
	}); err != nil {
		t.Fatalf("insert connection: %v", err)
	}

	// Bring the adapter up the way the server does at boot.
	if err := gm.StartAll(context.Background()); err != nil {
		t.Fatalf("StartAll: %v", err)
	}
	if !gm.IsRunning(wsID, platform) {
		t.Fatal("precondition: adapter should be running before disconnect")
	}

	rec := doJSON(t, s, http.MethodDelete, "/api/v1/connectors/"+platform, nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("disconnect: %d %s", rec.Code, rec.Body.String())
	}

	// The assertion the ordering exists for: a disconnected platform must have
	// no live adapter. Under the old order this stayed true — the bot kept
	// running and kept answering.
	if gm.IsRunning(wsID, platform) {
		t.Fatal("adapter still running after disconnect — the bot keeps answering for a removed connector")
	}
	if _, err := s.db.GetPlatformConnection(wsID, platform); err == nil {
		t.Fatal("connection row should be gone after disconnect")
	}
}
