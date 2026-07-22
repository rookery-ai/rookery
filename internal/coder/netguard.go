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
var blockedCIDRs = func() []*net.IPNet {
	cidrs := []string{
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
	}
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		if _, n, err := net.ParseCIDR(c); err == nil {
			out = append(out, n)
		}
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
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second, Control: denyPrivateAddr}
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
