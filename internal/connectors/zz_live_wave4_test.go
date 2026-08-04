//go:build livecheck

// Network-dependent live verification, excluded from the normal test run. Run with:
//
//	go test ./internal/connectors/ -tags livecheck -run TestLiveWave4 -v
//
// Only the KEYLESS wave-4 providers are covered — the rest need credentials.

package connectors

import (
	"context"
	"strings"
	"testing"
)

type w4Store struct{}

func (w4Store) AccessToken(context.Context, ConnRef) (string, error) { return "", nil }

func w4Call(t *testing.T, provider, action string, args map[string]any, want string) {
	t.Helper()
	reg, err := LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	res, err := Execute(context.Background(), reg, w4Store{}, nil,
		ConnRef{ID: "live", Provider: provider}, action, args, Policy{})
	if err != nil {
		t.Errorf("%s/%s: %v", provider, action, err)
		return
	}
	body := string(res.Data)
	t.Logf("%s (%d bytes): %s", action, len(body), w4trunc(body, 200))
	if want != "" && !strings.Contains(body, want) {
		t.Errorf("%s payload missing %q", action, want)
	}
	if len(res.Data) > 8192 {
		t.Errorf("%s returned %d bytes, over the 8 KiB bridge cap", action, len(res.Data))
	}
}

func TestLiveWave4Keyless(t *testing.T) {
	t.Run("openlibrary", func(t *testing.T) {
		w4Call(t, "openlibrary", "openlibrary_search",
			map[string]any{"query": "The Bridge on the Drina", "limit": 3}, "title")
		w4Call(t, "openlibrary", "openlibrary_get_by_isbn",
			map[string]any{"isbn": "9780226143552"}, "title")
	})
	t.Run("openstreetmap", func(t *testing.T) {
		w4Call(t, "openstreetmap", "osm_geocode",
			map[string]any{"query": "Skopje, North Macedonia", "limit": 2}, "lat")
		w4Call(t, "openstreetmap", "osm_reverse_geocode",
			map[string]any{"latitude": 41.9965, "longitude": 21.4314}, "address")
	})
	t.Run("openfoodfacts", func(t *testing.T) {
		// Nutella, a barcode that exists in every Open Food Facts mirror.
		w4Call(t, "openfoodfacts", "off_get_product",
			map[string]any{"barcode": "3017624010701"}, "product_name")
	})
}

func w4trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
