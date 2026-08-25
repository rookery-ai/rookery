package browser

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rookery-ai/rookery/internal/nethttp"
)

// guardedProxy is how the browser is kept out of private address space.
//
// Chromium resolves DNS itself, so nethttp's usual enforcement point — a
// net.Dialer Control hook — cannot reach it. Inspecting the URL before
// navigating would miss the two cases that matter most: a public hostname that
// RESOLVES into private space, and a redirect hop. Routing the browser through a
// proxy whose dial decision IS nethttp.DenyPrivateAddr catches every request,
// every redirect and every subresource through a single policy, with no second
// copy of the blocklist to drift out of step.
//
// Guard can be turned off for an owner who genuinely wants to read a
// self-hosted dashboard. That is a deliberate, documented escape and the reason
// the field exists rather than the guard being unconditional: connectors and MCP
// already skip the guard entirely because their hosts come from vendored YAML or
// an owner-typed URL. Here the model picks the URL out of untrusted search
// results and page content, which is exactly nethttp's threat model — so the
// DEFAULT is guarded and relaxing it is a decision the owner makes once.
type guardedProxy struct {
	ln    net.Listener
	guard bool

	mu      sync.Mutex
	refused []string
}

func startGuardedProxy(guard bool) (*guardedProxy, string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", fmt.Errorf("browser proxy listen: %w", err)
	}
	p := &guardedProxy{ln: ln, guard: guard}
	srv := &http.Server{Handler: p, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return p, ln.Addr().String(), nil
}

func (p *guardedProxy) close() {
	if p != nil && p.ln != nil {
		_ = p.ln.Close()
	}
}

// refusedHosts returns the hosts this proxy blocked, for diagnostics. A render
// that comes back empty because its subresources were refused looks identical to
// a render of an empty page; this is how the two are told apart in a log.
func (p *guardedProxy) refusedHosts() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.refused...)
}

func (p *guardedProxy) note(addr string) {
	p.mu.Lock()
	if len(p.refused) < 32 { // bounded: a hostile page can request endlessly
		p.refused = append(p.refused, addr)
	}
	p.mu.Unlock()
}

func (p *guardedProxy) dial(addr string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 15 * time.Second}
	if p.guard {
		d.Control = nethttp.DenyPrivateAddr
	}
	return d.Dial("tcp", addr)
}

func (p *guardedProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	p.handlePlain(w, r)
}

// handleConnect tunnels HTTPS. The guard runs on the CONNECT target before a
// single byte is proxied, which is the whole point: TLS means we cannot inspect
// the traffic, but we do not need to — the dial is the decision.
func (p *guardedProxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	addr := withDefaultPort(r.Host, "443")
	upstream, err := p.dial(addr)
	if err != nil {
		p.note(addr)
		slog.Debug("browser proxy refused CONNECT", "addr", addr, "err", err)
		http.Error(w, "blocked", http.StatusForbidden)
		return
	}
	defer upstream.Close()

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "cannot hijack", http.StatusInternalServerError)
		return
	}
	client, _, err := hj.Hijack()
	if err != nil {
		return
	}
	defer client.Close()
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(upstream, client) }()
	go func() { defer wg.Done(); _, _ = io.Copy(client, upstream) }()
	wg.Wait()
}

// handlePlain proxies cleartext HTTP. The request is forwarded verbatim over a
// guarded dial rather than through an http.Client, because a client would
// follow redirects itself and rewrite headers — and it is the BROWSER's job to
// decide what to do with a 302, with each hop dialing back through here.
func (p *guardedProxy) handlePlain(w http.ResponseWriter, r *http.Request) {
	if r.URL.Host == "" {
		http.Error(w, "not a proxy request", http.StatusBadRequest)
		return
	}
	addr := withDefaultPort(r.URL.Host, "80")
	conn, err := p.dial(addr)
	if err != nil {
		p.note(addr)
		slog.Debug("browser proxy refused request", "addr", addr, "err", err)
		http.Error(w, "blocked", http.StatusForbidden)
		return
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))

	outbound := r.Clone(r.Context())
	outbound.RequestURI = ""
	if err := outbound.Write(conn); err != nil {
		http.Error(w, "upstream write failed", http.StatusBadGateway)
		return
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "cannot hijack", http.StatusInternalServerError)
		return
	}
	client, _, err := hj.Hijack()
	if err != nil {
		return
	}
	defer client.Close()
	// Raw relay: the browser parses the response, not us.
	_, _ = io.Copy(client, conn)
}

func withDefaultPort(host, port string) string {
	if host == "" {
		return ""
	}
	if strings.LastIndex(host, ":") > strings.LastIndex(host, "]") {
		return host
	}
	return net.JoinHostPort(host, port)
}
