package coder

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

var errBlockedForTest = errors.New("blocked-for-test")

func TestIsBlockedIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "127.53.0.1", "::1",
		"10.0.0.5", "172.16.4.1", "192.168.1.194",
		"169.254.169.254", // cloud metadata
		"fd00::1",         // unique local
		"fe80::1",         // link-local
		"0.0.0.0",
		"64:ff9b::7f00:1",    // NAT64 well-known prefix encoding 127.0.0.1
		"64:ff9b::a9fe:a9fe", // NAT64 well-known prefix encoding 169.254.169.254
		"2002:7f00:1::",      // 6to4 encoding 127.0.0.1
	}
	for _, s := range blocked {
		if !isBlockedIP(net.ParseIP(s)) {
			t.Errorf("isBlockedIP(%s) = false, want true", s)
		}
	}
	allowed := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:2800:220:1:248:1893:25c8:1946"}
	for _, s := range allowed {
		if isBlockedIP(net.ParseIP(s)) {
			t.Errorf("isBlockedIP(%s) = true, want false", s)
		}
	}
}

// TestBlockedCIDRsAllParse guards against a typo in blockedCIDRStrings
// silently shrinking the blocklist. blockedCIDRs panics on a parse failure
// (see netguard.go), so if this test compiles and runs at all, every entry
// parsed — but assert the counts match too, so the invariant is explicit and
// doesn't rely on remembering why panicking is safe here.
func TestBlockedCIDRsAllParse(t *testing.T) {
	if len(blockedCIDRs) != len(blockedCIDRStrings) {
		t.Fatalf("blockedCIDRs has %d entries, want %d (source list) — an entry was silently dropped",
			len(blockedCIDRs), len(blockedCIDRStrings))
	}
}

// TestGuardedClientBlocksLoopback is the load-bearing test: chat previously had
// no network at all, and the connector bridge listens on loopback holding
// per-run bearer tokens. A guarded client must not be able to reach it.
func TestGuardedClientBlocksLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("secret bridge response"))
	}))
	defer srv.Close()

	client := guardedHTTPClient(5 * time.Second)
	_, err := client.Get(srv.URL) // httptest always binds 127.0.0.1
	if err == nil {
		t.Fatal("guarded client reached a loopback address")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Errorf("error should name the block, got: %v", err)
	}
}

// TestGuardedClientEnforcesOnFirstHop proves the real guard (denyPrivateAddr)
// blocks a loopback target reached via a redirect. It does NOT prove the guard
// runs on the second hop specifically — both httptest servers here bind
// 127.0.0.1, so the redirector itself is already blocked before the redirect
// is ever followed. See TestGuardedClientEnforcesOnEveryHop for per-hop proof.
func TestGuardedClientEnforcesOnFirstHop(t *testing.T) {
	private := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("should be unreachable"))
	}))
	defer private.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, private.URL, http.StatusFound)
	}))
	defer redirector.Close()

	client := guardedHTTPClient(5 * time.Second)
	if _, err := client.Get(redirector.URL); err == nil {
		t.Fatal("expected the redirect target to be blocked")
	}
}

// TestGuardedClientEnforcesOnEveryHop proves the dial-control predicate is
// consulted on EVERY hop of a redirect chain, not just the first. Both
// httptest servers bind loopback, so a hermetic test can't rely on the real
// denyPrivateAddr to distinguish "blocked on hop 1" from "blocked on hop 2" —
// instead it supplies its own control that ALLOWS the first address it sees
// and DENIES every subsequent one, and asserts the control was invoked twice
// (once per hop) with the request failing specifically on the second.
func TestGuardedClientEnforcesOnEveryHop(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("should be unreachable"))
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	var mu sync.Mutex
	var seen []string
	control := func(network, address string, _ syscall.RawConn) error {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, address)
		if len(seen) == 1 {
			return nil // allow hop 1 (the redirector)
		}
		return errBlockedForTest // deny hop 2 (the redirect target)
	}

	client := guardedHTTPClientWithControl(5*time.Second, control)
	_, err := client.Get(redirector.URL)

	mu.Lock()
	n := len(seen)
	mu.Unlock()

	if n != 2 {
		t.Fatalf("dial control invoked %d times, want 2 (one per hop)", n)
	}
	if err == nil {
		t.Fatal("expected the second hop to be denied")
	}
	if !strings.Contains(err.Error(), "blocked-for-test") {
		t.Errorf("expected the request to fail on the second hop's denial, got: %v", err)
	}
}

func TestGuardedClientAllowsPublicDial(t *testing.T) {
	// Dial control is what enforces policy; verify a public IP passes the check
	// itself rather than making a real network call.
	if err := denyPrivateAddr("tcp4", "93.184.216.34:443", nil); err != nil {
		t.Errorf("public address should dial: %v", err)
	}
	if err := denyPrivateAddr("tcp4", "127.0.0.1:8080", nil); err == nil {
		t.Error("loopback address must be refused")
	}
}

var _ = context.Background
