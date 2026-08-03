package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// keylessStore is a TokenStore for a provider with no credential.
type keylessStore struct{}

func (keylessStore) AccessToken(context.Context, ConnRef) (string, error) { return "", nil }

// Execute must be able to reach a loopback/RFC1918 address. This is DELIBERATE: the
// self-hosted connector tier (Home Assistant, Immich, Paperless-ngx) exists to talk to
// a box on the user's own LAN, and internal/nethttp.GuardedClient blocks exactly those
// ranges at dial time.
//
// The guard is right for websearch and the coder's web_fetch, where a URL can be chosen
// by untrusted content. It is wrong here: a connector's host comes either from vendored
// YAML or from a value the single owner typed into their own install.
//
// If this test fails because someone routed connectors through GuardedClient, the fix
// is NOT to weaken this test — it is to decide, deliberately, that self-hosted
// connectors are being dropped.
func TestExecuteReachesPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()
	// httptest binds 127.0.0.1 — a loopback address GuardedClient refuses to dial.
	if !strings.Contains(srv.URL, "127.0.0.1") {
		t.Fatalf("expected a loopback test server, got %s", srv.URL)
	}

	reg := &Registry{
		providers: map[string]Provider{
			"selfhosted_probe": {Name: "selfhosted_probe", Auth: AuthConfig{Kind: "none"}},
		},
		actions: map[string][]Action{
			"selfhosted_probe": {{
				Name:     "probe_status",
				Mutating: false,
				Request:  RequestTemplate{Method: "GET", URL: srv.URL + "/status"},
			}},
		},
	}

	res, err := Execute(context.Background(), reg, keylessStore{}, nil,
		ConnRef{ID: "c1", Provider: "selfhosted_probe"}, "probe_status", nil, Policy{})
	if err != nil {
		t.Fatalf("Execute against a loopback address failed: %v\n\n"+
			"If connectors were switched to nethttp.GuardedClient, every self-hosted "+
			"connector just stopped working.", err)
	}
	if !strings.Contains(string(res.Data), `"ok"`) {
		t.Errorf("payload = %s, want the probe body", res.Data)
	}
}
