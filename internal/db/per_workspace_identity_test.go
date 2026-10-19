package db_test

import (
	"errors"
	"testing"

	"github.com/rookery-ai/rookery/internal/db"
)

func link(t *testing.T, database *db.DB, rowID, ws, platform, user string) error {
	t.Helper()
	return database.LinkPlatformIdentity(&db.PlatformIdentity{
		ID: rowID, WorkspaceID: ws, Platform: platform, PlatformUserID: user,
	})
}

// The reported bug: one Telegram account linked to Demo could never be linked
// to Personal, because the schema allowed one workspace per account. A workspace
// is bound to its bot, so the same account must be linkable to every workspace.
func TestOneAccountLinksToSeveralWorkspaces(t *testing.T) {
	database := newIdentityTestDB(t)
	if err := database.CreateWorkspace(&db.Workspace{ID: "ws2", Name: "personal"}); err != nil {
		t.Fatal(err)
	}

	if err := link(t, database, "a", "ws1", "telegram", "1843540314"); err != nil {
		t.Fatalf("link ws1: %v", err)
	}
	if err := link(t, database, "b", "ws2", "telegram", "1843540314"); err != nil {
		t.Fatalf("link ws2 with the same account: %v", err)
	}

	for _, ws := range []string{"ws1", "ws2"} {
		got, err := database.GetPlatformIdentity(ws, "telegram", "1843540314")
		if err != nil {
			t.Fatalf("get %s: %v", ws, err)
		}
		if got.WorkspaceID != ws {
			t.Fatalf("lookup in %s returned workspace %s", ws, got.WorkspaceID)
		}
	}
}

// The lookup is scoped to the workspace: being linked to ws1 says nothing about
// ws2. The old global lookup is what routed a Personal-bot message into Demo.
func TestGetPlatformIdentityIsScopedToTheWorkspace(t *testing.T) {
	database := newIdentityTestDB(t)
	if err := database.CreateWorkspace(&db.Workspace{ID: "ws2", Name: "personal"}); err != nil {
		t.Fatal(err)
	}
	if err := link(t, database, "a", "ws1", "telegram", "u1"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.GetPlatformIdentity("ws2", "telegram", "u1"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("ws2 lookup err = %v, want ErrNotFound", err)
	}
}

// A workspace's bot still serves exactly one chat. The insert must REFUSE a
// second chat rather than overwrite the first: an upsert keyed on
// (workspace_id, platform) would let a stranger's racing /start take over a
// linked bot.
func TestLinkRefusesASecondChatForTheSameWorkspace(t *testing.T) {
	database := newIdentityTestDB(t)
	if err := link(t, database, "a", "ws1", "telegram", "owner"); err != nil {
		t.Fatal(err)
	}
	err := link(t, database, "b", "ws1", "telegram", "stranger")
	if !errors.Is(err, db.ErrAlreadyLinked) {
		t.Fatalf("second link err = %v, want ErrAlreadyLinked", err)
	}
	rows, err := database.ListPlatformIdentities("ws1", "telegram")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].PlatformUserID != "owner" {
		t.Fatalf("link was overwritten: %+v", rows)
	}
}

// Relinking the SAME chat is not a conflict worth reporting: a repeated /start
// from the linked chat must stay harmless.
func TestLinkingTheSameChatTwiceIsHarmless(t *testing.T) {
	database := newIdentityTestDB(t)
	if err := link(t, database, "a", "ws1", "telegram", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := link(t, database, "b", "ws1", "telegram", "owner"); err != nil {
		t.Fatalf("relink same chat: %v", err)
	}
}

// A Telegram group that becomes a supergroup gets a new chat id. Re-keying moves
// every link on the old id and leaves other chats alone.
func TestRekeyPlatformIdentityMovesTheLink(t *testing.T) {
	database := newIdentityTestDB(t)
	if err := database.CreateWorkspace(&db.Workspace{ID: "ws2", Name: "personal"}); err != nil {
		t.Fatal(err)
	}
	if err := link(t, database, "a", "ws1", "telegram", "-100"); err != nil {
		t.Fatal(err)
	}
	if err := link(t, database, "b", "ws2", "telegram", "-200"); err != nil {
		t.Fatal(err)
	}

	n, err := database.RekeyPlatformIdentity("telegram", "-100", "-1009999")
	if err != nil {
		t.Fatalf("rekey: %v", err)
	}
	if n != 1 {
		t.Fatalf("rekeyed %d rows, want 1", n)
	}
	if _, err := database.GetPlatformIdentity("ws1", "telegram", "-1009999"); err != nil {
		t.Fatalf("new id not linked: %v", err)
	}
	if _, err := database.GetPlatformIdentity("ws2", "telegram", "-200"); err != nil {
		t.Fatalf("unrelated link disturbed: %v", err)
	}
}
