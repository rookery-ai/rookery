package connectors

import (
	"testing"

	"github.com/rookery-ai/rookery/internal/db"
)

func TestActiveBoundConns(t *testing.T) {
	rows := []db.ServiceConnection{
		{ID: "c1", Provider: "google", AccountLabel: "work", AccountIdentity: "a@b.com", Status: "ACTIVE", Extra: `{"cloudid":"x"}`},
		{ID: "c2", Provider: "github", AccountLabel: "personal", AccountIdentity: "me", Status: "NEEDS_REAUTH"},
		{ID: "c3", Provider: "notion", AccountLabel: "team", AccountIdentity: "n@b.com", Status: "ACTIVE"},
	}
	out := ActiveBoundConns(rows)
	if len(out) != 2 {
		t.Fatalf("want 2 bound conns, got %d: %+v", len(out), out)
	}
	if out[0].ID != "c1" || out[0].Provider != "google" || out[0].Extra["cloudid"] != "x" {
		t.Fatalf("unexpected first conn: %+v", out[0])
	}
	if out[1].ID != "c3" || out[1].Provider != "notion" {
		t.Fatalf("unexpected second conn: %+v", out[1])
	}
}
