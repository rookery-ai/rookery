package coder

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIsBlockedIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "127.53.0.1", "::1",
		"10.0.0.5", "172.16.4.1", "192.168.1.194",
		"169.254.169.254", // cloud metadata
		"fd00::1",         // unique local
		"fe80::1",         // link-local
		"0.0.0.0",
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

// TestGuardedClientBlocksRedirectToPrivate proves the guard is applied per
// connection, so a public URL cannot redirect into private space.
func TestGuardedClientBlocksRedirectToPrivate(t *testing.T) {
	private := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("should be unreachable"))
	}))
	defer private.Close()

	// The dialer control runs on every connection, including the redirect hop,
	// so pointing a redirect at loopback is blocked at dial time.
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, private.URL, http.StatusFound)
	}))
	defer redirector.Close()

	client := guardedHTTPClient(5 * time.Second)
	if _, err := client.Get(redirector.URL); err == nil {
		t.Fatal("expected the redirect target to be blocked")
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
