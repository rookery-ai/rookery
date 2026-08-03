package nethttp

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestBlockedAddrSurvivesTheHTTPErrorChain is the load-bearing one. The dial
// guard's error is returned from a net.Dialer Control func, which the stdlib
// wraps in *net.OpError and then again in *url.Error before a caller sees it.
// If errors.Is could not see through that, every consumer would be back to
// string-matching "blocked", which is exactly what the sentinel exists to
// avoid — and the failure would be silent, since the error still reads fine.
func TestBlockedAddrSurvivesTheHTTPErrorChain(t *testing.T) {
	// httptest binds to loopback, which the guard refuses by design.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, err := GuardedClient(5 * time.Second).Get(srv.URL)
	if err == nil {
		t.Fatal("guarded client reached a loopback address")
	}
	if !errors.Is(err, ErrBlockedAddr) {
		t.Fatalf("errors.Is(err, ErrBlockedAddr) = false through the http chain: %v", err)
	}
}

func TestOrdinaryDialFailureIsNotReportedAsBlocked(t *testing.T) {
	// A public address that will not connect: the guard permits the dial, so
	// whatever error comes back must NOT carry the blocked sentinel, or callers
	// would tell the user to check their DNS filter over an unrelated outage.
	_, err := GuardedClientWithControl(time.Second, nil).Get("http://198.51.100.1:9/")
	if err == nil {
		t.Skip("unexpectedly connected to the TEST-NET-2 discard address")
	}
	if errors.Is(err, ErrBlockedAddr) {
		t.Fatalf("ordinary dial failure misreported as blocked: %v", err)
	}
}
