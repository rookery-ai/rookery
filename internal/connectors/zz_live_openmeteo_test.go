//go:build livecheck

// Network-dependent live verification, excluded from the normal test run: CI must not
// depend on a third party being up. Run it deliberately with:
//
//	go test ./internal/connectors/ -tags livecheck -run TestLiveOpenMeteo -v
//
// Open-Meteo is the one wave-1 provider this can cover, because it is keyless — every
// other provider needs a credential and goes through cmd/livecheck instead.

package connectors

import (
	"context"
	"strings"
	"testing"
)

type nilStore struct{}

func (nilStore) AccessToken(context.Context, ConnRef) (string, error) { return "", nil }

// TestLiveOpenMeteo hits the REAL Open-Meteo API through the shipped manifest. It is
// keyless, so this needs no credential and no DB — which is exactly why Open-Meteo is
// the one wave-1 provider that can be live-verified in CI-like conditions.
func TestLiveOpenMeteo(t *testing.T) {
	reg, err := LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	ref := ConnRef{ID: "live", Provider: "open_meteo"}
	ctx := context.Background()

	geo, err := Execute(ctx, reg, nilStore{}, nil, ref, "weather_geocode",
		map[string]any{"name": "Skopje", "count": 1}, Policy{})
	if err != nil {
		t.Fatalf("weather_geocode: %v", err)
	}
	t.Logf("geocode (%d bytes): %s", len(geo.Data), truncStr(string(geo.Data), 300))
	if !strings.Contains(string(geo.Data), "latitude") {
		t.Errorf("geocode payload has no latitude: %s", geo.Data)
	}

	for _, tc := range []struct {
		action string
		args   map[string]any
		want   string
	}{
		{"weather_current", map[string]any{"latitude": 41.99, "longitude": 21.43, "timezone": "auto"}, "temperature_2m"},
		{"weather_forecast", map[string]any{"latitude": 41.99, "longitude": 21.43, "forecast_days": 3, "timezone": "auto"}, "temperature_2m_max"},
		{"weather_air_quality", map[string]any{"latitude": 41.99, "longitude": 21.43, "timezone": "auto"}, "pm2_5"},
	} {
		res, err := Execute(ctx, reg, nilStore{}, nil, ref, tc.action, tc.args, Policy{})
		if err != nil {
			t.Errorf("%s: %v", tc.action, err)
			continue
		}
		t.Logf("%s (%d bytes): %s", tc.action, len(res.Data), truncStr(string(res.Data), 300))
		if !strings.Contains(string(res.Data), tc.want) {
			t.Errorf("%s payload missing %q: %s", tc.action, tc.want, res.Data)
		}
		// The bridge caps a result at 8 KiB; a narrowed forecast must fit well under.
		if len(res.Data) > 8192 {
			t.Errorf("%s returned %d bytes, over the 8 KiB bridge cap", tc.action, len(res.Data))
		}
	}
}

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
