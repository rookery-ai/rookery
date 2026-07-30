package coder

import (
	"net"
	"net/http"
	"syscall"
	"time"

	"github.com/ilijad1/rookery/internal/nethttp"
)

// The actual dial-control implementation lives in internal/nethttp so
// internal/gateway (Discord attachment downloads) can share the exact same
// guard instead of a second hand-rolled copy. This file re-exposes it under
// the package-private names this package's tests and call sites already use,
// so nothing downstream of coder had to change.

// blockedCIDRStrings mirrors nethttp.BlockedCIDRStrings — kept as a
// package-local alias so TestBlockedCIDRsAllParse (which predates the move)
// keeps working unchanged.
var blockedCIDRStrings = nethttp.BlockedCIDRStrings

// blockedCIDRs mirrors nethttp.BlockedCIDRs (the parsed form).
var blockedCIDRs = nethttp.BlockedCIDRs

// isBlockedIP reports whether ip falls in private, loopback, link-local, or
// otherwise non-public space.
func isBlockedIP(ip net.IP) bool {
	return nethttp.IsBlockedIP(ip)
}

// denyPrivateAddr is a net.Dialer Control function: it runs after DNS
// resolution with the concrete address about to be connected to, for every
// connection including redirect hops.
func denyPrivateAddr(network, address string, c syscall.RawConn) error {
	return nethttp.DenyPrivateAddr(network, address, c)
}

// guardedHTTPClient returns an HTTP client that cannot open a connection to
// private address space.
func guardedHTTPClient(timeout time.Duration) *http.Client {
	return nethttp.GuardedClient(timeout)
}

// guardedHTTPClientWithControl builds the client around a supplied dial-control
// predicate. Production always uses denyPrivateAddr; the seam exists so a test
// can prove the predicate is consulted on EVERY hop of a redirect chain, which
// is otherwise unprovable hermetically — any local test server is itself a
// private address, so the first hop would be blocked before a redirect is ever
// followed.
func guardedHTTPClientWithControl(timeout time.Duration, control func(network, address string, c syscall.RawConn) error) *http.Client {
	return nethttp.GuardedClientWithControl(timeout, control)
}
