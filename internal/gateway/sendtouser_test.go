package gateway_test

import (
	"path/filepath"
	"testing"

	"github.com/ilijad1/rookery/internal/db"
	"github.com/ilijad1/rookery/internal/gateway"
)

func TestPrimaryPlatformSettingKeyIsStable(t *testing.T) {
	// The SPA and the settings row both hardcode this string; changing it
	// silently resets every workspace's choice.
	if gateway.PrimaryPlatformSettingKey != "chat.primary_platform" {
		t.Fatalf("key = %q", gateway.PrimaryPlatformSettingKey)
	}
}

func TestResolveDeliveryOrderPrefersPrimary(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "t.db"), "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.CreateWorkspace(&db.Workspace{ID: "ws1", Name: "w"}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"discord", "telegram"} {
		if err := database.UpsertPlatformIdentity(&db.PlatformIdentity{
			ID: "id-" + p, WorkspaceID: "ws1", Platform: p, PlatformUserID: "u-" + p,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Unset primary: defined order (first linked), not arbitrary.
	got, err := gateway.ResolveDeliveryOrder(database, "ws1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Platform != "discord" {
		t.Fatalf("unset primary: order = %+v, want discord first", got)
	}

	// Set primary: it moves to the front, the rest keep their relative order
	// so a primary that is down still falls back predictably.
	if err := database.SetSetting("ws1", gateway.PrimaryPlatformSettingKey, "telegram"); err != nil {
		t.Fatal(err)
	}
	got, err = gateway.ResolveDeliveryOrder(database, "ws1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Platform != "telegram" || got[1].Platform != "discord" {
		t.Fatalf("with primary: order = %+v", got)
	}

	// A primary naming a platform that is no longer linked must not drop the
	// remaining targets — otherwise unlinking the primary silences delivery.
	if err := database.SetSetting("ws1", gateway.PrimaryPlatformSettingKey, "slack"); err != nil {
		t.Fatal(err)
	}
	got, err = gateway.ResolveDeliveryOrder(database, "ws1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Platform != "discord" {
		t.Fatalf("stale primary: order = %+v", got)
	}
}
