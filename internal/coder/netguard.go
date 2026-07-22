package coder

import (
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// Private and special-purpose ranges web_fetch must never reach. Blocking is
// enforced at DIAL time rather than by inspecting the URL, which is the only
// way to catch the two cases that matter: a hostname that resolves to a private
// address, and a redirect hop into private space (the dialer control runs on
// every connection the client makes, redirects included).
//
// This became load-bearing when web_fetch was un-gated for chat: chat had no
// network at all before, and the loopback interface hosts the connector bridge,
// which holds per-run bearer tokens for the workspace's connected accounts.
var blockedCIDRStrings = []string{
	"0.0.0.0/8",      // this host
	"10.0.0.0/8",     // RFC1918
	"127.0.0.0/8",    // loopback — the connector bridge lives here
	"169.254.0.0/16", // link-local, incl. 169.254.169.254 cloud metadata
	"172.16.0.0/12",  // RFC1918
	"192.168.0.0/16", // RFC1918
	"100.64.0.0/10",  // carrier-grade NAT / tailscale range
	"192.0.0.0/24",   // IETF protocol assignments
	"198.18.0.0/15",  // benchmarking
	"::1/128",        // IPv6 loopback
	"fc00::/7",       // IPv6 unique local
	"fe80::/10",      // IPv6 link-local
	"::/128",         // unspecified

	// IPv6 transition mechanisms that embed an IPv4 address. Blocking these
	// is INHERENTLY PARTIAL: a NAT64 deployment may synthesize addresses
	// under a network-specific prefix instead of the well-known one below,
	// and such prefixes cannot be enumerated in advance. This is
	// defense-in-depth, not closure of the class — it raises the bar for
	// the well-known/standard cases, it does not eliminate them.
	"64:ff9b::/96", // NAT64 well-known prefix (e.g. embeds 127.0.0.1, 169.254.169.254)
	"2002::/16",    // 6to4 (embeds the IPv4 address in bits 16-47)
	"2001::/32",    // Teredo (embeds an obfuscated IPv4 address)
}

var blockedCIDRs = func() []*net.IPNet {
	out := make([]*net.IPNet, 0, len(blockedCIDRStrings))
	for _, c := range blockedCIDRStrings {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			// These are compile-time constants; a parse failure can only be a
			// coding mistake. Silently dropping it would shrink the blocklist
			// with no test failure to catch it — fail loudly instead.
			panic(fmt.Errorf("netguard: invalid CIDR constant %q: %w", c, err))
		}
		out = append(out, n)
	}
	return out
}()

// isBlockedIP reports whether ip falls in private, loopback, link-local, or
// otherwise non-public space.
func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true // un-parseable is not provably public
	}
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
	}
	for _, n := range blockedCIDRs {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// denyPrivateAddr is a net.Dialer Control function: it runs after DNS
// resolution with the concrete address about to be connected to, for every
// connection including redirect hops.
func denyPrivateAddr(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("blocked: cannot parse address %q", address)
	}
	ip := net.ParseIP(host)
	if isBlockedIP(ip) {
		return fmt.Errorf("blocked: %s is a private or loopback address; web_fetch may only reach public hosts", host)
	}
	return nil
}

// guardedHTTPClient returns an HTTP client that cannot open a connection to
// private address space.
func guardedHTTPClient(timeout time.Duration) *http.Client {
	return guardedHTTPClientWithControl(timeout, denyPrivateAddr)
}

// guardedHTTPClientWithControl builds the client around a supplied dial-control
// predicate. Production always uses denyPrivateAddr; the seam exists so a test
// can prove the predicate is consulted on EVERY hop of a redirect chain, which
// is otherwise unprovable hermetically — any local test server is itself a
// private address, so the first hop would be blocked before a redirect is ever
// followed.
func guardedHTTPClientWithControl(timeout time.Duration, control func(network, address string, c syscall.RawConn) error) *http.Client {
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second, Control: control}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: timeout,
			MaxIdleConns:          10,
		},
	}
}
