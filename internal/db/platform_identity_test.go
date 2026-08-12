package db_test

import (
	"path/filepath"
	"testing"

	"github.com/rookery-ai/rookery/internal/db"
)

func newIdentityTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.CreateWorkspace(&db.Workspace{ID: "ws1", Name: "tester"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	return database
}

// Identities linked in the same second tie on linked_at, which is stored as a
// string. Platform order is deterministic by alphabetic ordering.
func TestListPlatformIdentitiesIsDeterministic(t *testing.T) {
	database := newIdentityTestDB(t)
	for _, id := range []struct{ rowID, platform string }{
		{"id-c", "telegram"},
		{"id-a", "slack"},
		{"id-b", "discord"},
	} {
		if err := database.UpsertPlatformIdentity(&db.PlatformIdentity{
			ID: id.rowID, WorkspaceID: "ws1", Platform: id.platform, PlatformUserID: "u-" + id.platform,
		}); err != nil {
			t.Fatalf("upsert %s: %v", id.platform, err)
		}
	}

	var first []string
	for i := 0; i < 5; i++ {
		rows, err := database.ListPlatformIdentities("ws1", "")
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, len(rows))
		for j, r := range rows {
			got[j] = r.Platform
		}
		if i == 0 {
			first = got
			continue
		}
		if len(got) != len(first) {
			t.Fatalf("row count changed: %v vs %v", got, first)
		}
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("order not stable: run %d = %v, run 0 = %v", i, got, first)
			}
		}
	}

	// All three tie on linked_at (datetime('now') has one-second granularity),
	// so `platform` decides — alphabetically, and independently of the random
	// UUIDs a real install generates. `id` is the final tiebreaker and never
	// fires here, since one workspace has at most one identity per platform.
	want := []string{"discord", "slack", "telegram"}
	for i := range want {
		if first[i] != want[i] {
			t.Fatalf("order = %v, want %v", first, want)
		}
	}
}

func TestDeletePlatformIdentityRemovesOnlyThatPlatform(t *testing.T) {
	database := newIdentityTestDB(t)
	for _, p := range []string{"telegram", "discord"} {
		if err := database.UpsertPlatformIdentity(&db.PlatformIdentity{
			ID: "id-" + p, WorkspaceID: "ws1", Platform: p, PlatformUserID: "u-" + p,
		}); err != nil {
			t.Fatal(err)
		}
	}

	if err := database.DeletePlatformIdentity("ws1", "discord"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	rows, err := database.ListPlatformIdentities("ws1", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Platform != "telegram" {
		t.Fatalf("after delete, rows = %+v", rows)
	}

	// Deleting an absent identity is a no-op, not an error — the Unlink button
	// may race a link that was already removed.
	if err := database.DeletePlatformIdentity("ws1", "discord"); err != nil {
		t.Fatalf("second delete should be a no-op: %v", err)
	}
}
