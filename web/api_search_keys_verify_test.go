package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/ilijad1/rookery/internal/nethttp"
	"github.com/ilijad1/rookery/internal/websearch"
)

// TestSearchKeyRejectedWhenProviderRefusesIt: a key the provider rejects is not
// stored at all. Previously any string was accepted, GET reported "configured"
// because a row existed, and the only symptom was search silently degrading to
// keyless scraping forever.
func TestSearchKeyRejectedWhenProviderRefusesIt(t *testing.T) {
	s, _ := newAPITestServer(t)
	s.searchKeyVerify = func(context.Context, string, string) error {
		return fmt.Errorf("%w: HTTP 401", websearch.ErrInvalidKey)
	}
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPut, "/api/v1/search-keys",
		map[string]string{"provider": "brave", "key": "typo"}, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for a rejected key, got %d %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), "invalid_key") {
		t.Fatalf("want invalid_key code: %s", rec.Body.String())
	}

	// And nothing was written.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/search-keys", nil, cookies)
	if !contains(rec.Body.String(), `"brave":false`) {
		t.Fatalf("a rejected key must not be stored: %s", rec.Body.String())
	}
}

// TestSearchKeyStoredWhenProviderUnreachable: a provider outage must not stop
// the user saving a good key — it is stored, flagged unverified, and explained.
func TestSearchKeyStoredWhenProviderUnreachable(t *testing.T) {
	s, _ := newAPITestServer(t)
	s.searchKeyVerify = func(context.Context, string, string) error {
		return errors.New("dial tcp: i/o timeout")
	}
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPut, "/api/v1/search-keys",
		map[string]string{"provider": "brave", "key": "probably-fine"}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("an outage must not block the save: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !contains(body, `"verified":false`) {
		t.Fatalf("want verified:false: %s", body)
	}
	if !contains(body, "could not be verified") {
		t.Fatalf("want an explanatory note: %s", body)
	}

	rec = doJSON(t, s, http.MethodGet, "/api/v1/search-keys", nil, cookies)
	if !contains(rec.Body.String(), `"brave":true`) {
		t.Fatalf("the key should still be stored: %s", rec.Body.String())
	}
}

// TestSearchKeyBlockedHostPointsAtDNS: when the provider's API host resolves
// into blocked address space, the note must name local DNS filtering rather
// than blaming the provider. This is the exact shape of the failure that
// prompted the change — an AdGuard rule answering a public hostname with
// 0.0.0.0, which then reads as an ordinary connection error.
func TestSearchKeyBlockedHostPointsAtDNS(t *testing.T) {
	s, _ := newAPITestServer(t)
	s.searchKeyVerify = func(context.Context, string, string) error {
		return fmt.Errorf("dial tcp: %w", nethttp.ErrBlockedAddr)
	}
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPut, "/api/v1/search-keys",
		map[string]string{"provider": "brave", "key": "fine-key"}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("a blocked host must not reject the key: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !contains(body, "DNS") {
		t.Fatalf("the note should point at local DNS filtering: %s", body)
	}
	if !contains(body, `"verified":false`) {
		t.Fatalf("want verified:false: %s", body)
	}
}

// TestSearchKeyVerifiedOnSuccess covers the happy path's new field.
func TestSearchKeyVerifiedOnSuccess(t *testing.T) {
	s, _ := newAPITestServer(t)
	s.searchKeyVerify = func(context.Context, string, string) error { return nil }
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPut, "/api/v1/search-keys",
		map[string]string{"provider": "brave", "key": "good"}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"verified":true`) {
		t.Fatalf("want verified:true: %s", rec.Body.String())
	}
}
