// Package nethttp holds a single, shared dial-control primitive for outbound
// HTTP clients that must refuse to reach private/loopback address space. It
// exists so the guard is implemented exactly once: internal/coder (the API
// engine's web_fetch/web_search tools) and internal/gateway (Discord
// attachment downloads) both need the same SSRF protection, and a second
// hand-rolled copy is exactly how that protection drifts out of sync.
package nethttp

import (
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// BlockedCIDRStrings lists private, loopback, and other special-purpose
// ranges a guarded client must never dial. Blocking is enforced at DIAL time
// (see DenyPrivateAddr) rather than by inspecting the URL up front, which is
// the only way to catch both cases that matter: a hostname that resolves to
// a private address, and a redirect hop into private space (the dialer
// control runs on every connection the client makes, redirects included).
var BlockedCIDRStrings = []string{
	"0.0.0.0/8",      // this host
	"10.0.0.0/8",     // RFC1918
	"127.0.0.0/8",    // loopback — e.g. the connector bridge lives here
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

// BlockedCIDRs is BlockedCIDRStrings, parsed once. Exported (rather than
// computed lazily behind IsBlockedIP) so callers that need the raw net.IPNet
// list — as internal/coder's tests do, to prove no entry was silently
// dropped — don't need a second parse of the same constants.
var BlockedCIDRs = parseCIDRs(BlockedCIDRStrings)

func parseCIDRs(list []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(list))
	for _, c := range list {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			// These are compile-time constants; a parse failure can only be a
			// coding mistake. Silently dropping it would shrink the blocklist
			// with no test failure to catch it — fail loudly instead.
			panic(fmt.Errorf("nethttp: invalid CIDR constant %q: %w", c, err))
		}
		out = append(out, n)
	}
	return out
}

// IsBlockedIP reports whether ip falls in private, loopback, link-local, or
// otherwise non-public space.
func IsBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true // un-parseable is not provably public
	}
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
	}
	for _, n := range BlockedCIDRs {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// DenyPrivateAddr is a net.Dialer Control function: it runs after DNS
// resolution with the concrete address about to be connected to, for every
// connection including redirect hops.
func DenyPrivateAddr(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("blocked: cannot parse address %q", address)
	}
	ip := net.ParseIP(host)
	if IsBlockedIP(ip) {
		return fmt.Errorf("blocked: %s is a private or loopback address", host)
	}
	return nil
}

// GuardedClient returns an HTTP client that cannot open a connection to
// private address space.
func GuardedClient(timeout time.Duration) *http.Client {
	return GuardedClientWithControl(timeout, DenyPrivateAddr)
}

// GuardedClientWithControl builds the client around a supplied dial-control
// predicate. Production code always uses DenyPrivateAddr; the seam exists so
// a test can prove the predicate is consulted on EVERY hop of a redirect
// chain, which is otherwise unprovable hermetically — any local test server
// is itself a private address, so the first hop would be blocked before a
// redirect is ever followed. It also lets a test stand up a loopback-bound
// httptest server and still exercise the guarded client's request path,
// since the real DenyPrivateAddr would refuse loopback outright.
func GuardedClientWithControl(timeout time.Duration, control func(network, address string, c syscall.RawConn) error) *http.Client {
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
