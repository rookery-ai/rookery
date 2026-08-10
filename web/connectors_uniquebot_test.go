package web

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ilijad1/rookery/internal/db"
	"github.com/ilijad1/rookery/internal/gateway"
)

// uniqueBotFixture builds a server with two workspaces and a platform whose
// Validate reports whichever bot id the token names, so a test can connect "the
// same bot" or "a different bot" without any network call.
func uniqueBotFixture(t *testing.T, platform string) (*Server, string, string) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })

	wsA, wsB := uuid.New().String(), uuid.New().String()
	if err := d.CreateWorkspace(&db.Workspace{ID: wsA, Name: "alpha"}); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateWorkspace(&db.Workspace{ID: wsB, Name: "beta"}); err != nil {
		t.Fatal(err)
	}

	gateway.RegisterCredSpec(gateway.CredSpec{
		Platform: platform,
		Label:    "Unique",
		Fields:   []gateway.CredField{{Key: "token"}},
		Validate: func(v map[string]string) (gateway.BotIdentity, error) {
			// The token IS the bot id here, so "same bot, rotated token" is
			// expressible: two different token strings, one identity.
			return gateway.BotIdentity{Username: "bot-" + v["token"], UserID: v["bot_id"]}, nil
		},
	})
	return &Server{db: d, systemKey: make([]byte, 32)}, wsA, wsB
}

// TestSameBotRejectedOnSecondWorkspace is the regression guard for the reported
// failure: one Discord bot connected to two workspaces produced duplicate
// replies and a second wizard that waited forever for a /start that
// platform_identities' UNIQUE(platform, platform_user_id) makes impossible.
func TestSameBotRejectedOnSecondWorkspace(t *testing.T) {
	s, wsA, wsB := uniqueBotFixture(t, "ub-same")

	if _, _, err := s.saveConnector(wsA, "ub-same", map[string]string{"token": "t1", "bot_id": "BOT-1"}); err != nil {
		t.Fatalf("first connect should succeed: %v", err)
	}

	_, _, err := s.saveConnector(wsB, "ub-same", map[string]string{"token": "t2", "bot_id": "BOT-1"})
	if err == nil {
		t.Fatal("expected the second workspace to be refused the same bot")
	}
	if !errors.Is(err, ErrBotAlreadyConnected) {
		t.Fatalf("expected ErrBotAlreadyConnected, got %v", err)
	}
	// The message has to name the workspace holding it, or the owner has no way
	// to find the conflict in a multi-workspace install.
	if !contains(err.Error(), "alpha") {
		t.Fatalf("error must name the owning workspace: %v", err)
	}

	// And nothing may have been written for the rejected workspace.
	if _, err := s.db.GetPlatformConnection(wsB, "ub-same"); err == nil {
		t.Fatal("a refused connect must not persist a connection row")
	}
}

// TestSameWorkspaceMayRotateItsToken pins the carve-out that makes the guard
// safe: re-saving the SAME workspace must keep working, or rotating a leaked
// token would lock the owner out of their own connector.
func TestSameWorkspaceMayRotateItsToken(t *testing.T) {
	s, wsA, _ := uniqueBotFixture(t, "ub-rotate")

	if _, _, err := s.saveConnector(wsA, "ub-rotate", map[string]string{"token": "old", "bot_id": "BOT-9"}); err != nil {
		t.Fatalf("first connect: %v", err)
	}
	if _, _, err := s.saveConnector(wsA, "ub-rotate", map[string]string{"token": "new", "bot_id": "BOT-9"}); err != nil {
		t.Fatalf("re-saving the same bot to the same workspace must succeed: %v", err)
	}
}

// TestDifferentBotsCoexist confirms the guard keys on the bot, not the platform
// — two workspaces each running their OWN bot is the supported arrangement.
func TestDifferentBotsCoexist(t *testing.T) {
	s, wsA, wsB := uniqueBotFixture(t, "ub-distinct")

	if _, _, err := s.saveConnector(wsA, "ub-distinct", map[string]string{"token": "t1", "bot_id": "BOT-A"}); err != nil {
		t.Fatalf("workspace A: %v", err)
	}
	if _, _, err := s.saveConnector(wsB, "ub-distinct", map[string]string{"token": "t2", "bot_id": "BOT-B"}); err != nil {
		t.Fatalf("a second workspace with its OWN bot must be allowed: %v", err)
	}
}

// TestDisconnectFreesTheBot covers why the lookup JOINs platform_connections
// instead of reading the bot_identity setting alone: disconnecting deletes the
// connection row but LEAVES that setting behind, so a settings-only query would
// keep blocking the bot forever after it was released.
func TestDisconnectFreesTheBot(t *testing.T) {
	s, wsA, wsB := uniqueBotFixture(t, "ub-free")

	if _, _, err := s.saveConnector(wsA, "ub-free", map[string]string{"token": "t1", "bot_id": "BOT-7"}); err != nil {
		t.Fatalf("workspace A: %v", err)
	}
	if err := s.db.DeletePlatformConnection(wsA, "ub-free"); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	if _, _, err := s.saveConnector(wsB, "ub-free", map[string]string{"token": "t1", "bot_id": "BOT-7"}); err != nil {
		t.Fatalf("bot must be reusable once released: %v", err)
	}
}

// TestUnknownIdentityFailsOpen pins the deliberate escape hatch: a platform
// whose Validate yields no user id cannot be checked, and blocking there would
// reject every connect on that platform to prevent a collision we cannot detect.
func TestUnknownIdentityFailsOpen(t *testing.T) {
	s, wsA, wsB := uniqueBotFixture(t, "ub-blind")

	if _, _, err := s.saveConnector(wsA, "ub-blind", map[string]string{"token": "t1"}); err != nil {
		t.Fatalf("workspace A: %v", err)
	}
	if _, _, err := s.saveConnector(wsB, "ub-blind", map[string]string{"token": "t1"}); err != nil {
		t.Fatalf("an unidentifiable bot must not be blocked: %v", err)
	}
}
