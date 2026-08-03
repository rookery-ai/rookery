//go:build livecheck

// Network-dependent live verification, excluded from the normal test run: CI must not
// depend on a third party being up. Run deliberately with:
//
//	go test ./internal/connectors/ -tags livecheck -run TestLiveWave2 -v
//
// Only the KEYLESS wave-2 providers can be covered here — everything else needs a
// credential and goes through cmd/livecheck instead.

package connectors

import (
	"context"
	"strings"
	"testing"
)

type wave2Store struct{}

func (wave2Store) AccessToken(context.Context, ConnRef) (string, error) { return "", nil }

func liveCall(t *testing.T, provider, action string, args map[string]any, want string) {
	t.Helper()
	reg, err := LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	res, err := Execute(context.Background(), reg, wave2Store{}, nil,
		ConnRef{ID: "live", Provider: provider}, action, args, Policy{})
	if err != nil {
		t.Errorf("%s/%s: %v", provider, action, err)
		return
	}
	body := string(res.Data)
	t.Logf("%s (%d bytes): %s", action, len(body), trunc2(body, 220))
	if want != "" && !strings.Contains(body, want) {
		t.Errorf("%s payload missing %q", action, want)
	}
	// The connector bridge caps a result at 8 KiB; anything over truncates before the
	// model sees it, and truncated JSON still parses, so it reads as complete data.
	if len(res.Data) > 8192 {
		t.Errorf("%s returned %d bytes, over the 8 KiB bridge cap", action, len(res.Data))
	}
}

func TestLiveWave2Keyless(t *testing.T) {
	t.Run("wikipedia", func(t *testing.T) {
		liveCall(t, "wikipedia", "wikipedia_search", map[string]any{"query": "Skopje", "limit": 3}, "Skopje")
		liveCall(t, "wikipedia", "wikipedia_summary", map[string]any{"title": "Skopje"}, "extract")
	})
	t.Run("hackernews", func(t *testing.T) {
		liveCall(t, "hackernews", "hn_top_stories", nil, "")
		liveCall(t, "hackernews", "hn_get_item", map[string]any{"item_id": 8863}, "title")
	})
	t.Run("frankfurter", func(t *testing.T) {
		liveCall(t, "frankfurter", "fx_latest", map[string]any{"base": "EUR", "symbols": "USD,GBP"}, "rates")
		liveCall(t, "frankfurter", "fx_currencies", nil, "EUR")
	})
}

func trunc2(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
