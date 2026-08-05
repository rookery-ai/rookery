package web

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestAPIConnectors_ReportsBotOnline pins the liveness field the link step
// renders. Before it existed, a connection whose server was down looked
// identical to one merely waiting for the operator's /start — the UI spun
// indefinitely in both cases, which is how a dead process was misread as a
// misconfigured Discord app.
//
// newAPITestServer wires no GatewayManager, so this also covers the nil-gateway
// branch: report offline rather than claim a liveness we cannot observe.
func TestAPIConnectors_ReportsBotOnline(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/connectors", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var got struct {
		Platforms []struct {
			Platform  string `json:"platform"`
			Connected bool   `json:"connected"`
			BotOnline bool   `json:"bot_online"`
		} `json:"platforms"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v — body %s", err, rec.Body.String())
	}
	if len(got.Platforms) == 0 {
		t.Fatalf("expected platforms, got none: %s", rec.Body.String())
	}

	// The field must be present in the JSON, not merely default to false when
	// absent — the SPA branches on it, so a missing key would read as offline
	// forever without any test noticing.
	if !contains(rec.Body.String(), `"bot_online"`) {
		t.Fatalf("bot_online must be serialised: %s", rec.Body.String())
	}
	for _, p := range got.Platforms {
		if p.BotOnline {
			t.Fatalf("platform %q reported online with no gateway wired", p.Platform)
		}
	}
}
