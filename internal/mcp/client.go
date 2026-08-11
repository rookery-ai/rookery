package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// clientName/clientVersion identify Rookery in the MCP initialize handshake. Servers
// log this and some gate features on it, so it is a real identifier rather than a
// placeholder.
const (
	clientName    = "rookery"
	clientVersion = "1"
)

// dialTimeout bounds the initialize handshake; callTimeout bounds one tools/call.
// They are separate because a slow TOOL is normal (a report, a browser step) while a
// slow HANDSHAKE means the server is not really there.
const (
	dialTimeout = 15 * time.Second
	callTimeout = 60 * time.Second
	// idleTTL evicts a pooled session that has gone unused. A self-hosted server
	// that sleeps will drop the session anyway; holding a dead one costs a wasted
	// round-trip on the next call.
	idleTTL = 5 * time.Minute
)

// Client speaks MCP to servers, pooling one session per (workspace, server).
//
// Pooling matters because streamable HTTP is stateful: initialize establishes a
// session id, and a chat turn making three calls should not pay three handshakes.
// The pool's other half is the single reconnect-and-retry in CallTool — a homelab
// server that slept has dropped its session, and the first call afterwards must not
// surface to the user as a failure.
type Client struct {
	httpClient *http.Client

	mu       sync.Mutex
	sessions map[string]*pooled
}

type pooled struct {
	sess     *sdk.ClientSession
	lastUsed time.Time
	// fingerprint captures the connection parameters. A server whose URL or
	// credential was edited must not keep serving from a session opened against the
	// old ones.
	fingerprint string
}

// NewClient returns a Client. A nil httpClient gets a plain default.
//
// Deliberately NOT nethttp.GuardedClient: see the package comment. Self-hosted MCP
// servers live at exactly the private addresses that guard blocks.
func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: callTimeout}
	}
	return &Client{httpClient: httpClient, sessions: map[string]*pooled{}}
}

// authTransport injects the server's credential on every request.
//
// MCP requires the credential on EVERY request rather than only the handshake, since
// the transport is stateless at the HTTP layer.
type authTransport struct {
	base       http.RoundTripper
	authKind   string
	headerName string
	token      string
}

func (t *authTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	// Clone before mutating: the caller owns the request, and RoundTrippers are
	// documented not to modify it.
	r2 := r.Clone(r.Context())
	switch t.authKind {
	case "bearer":
		r2.Header.Set("Authorization", "Bearer "+t.token)
	case "header":
		if t.headerName != "" {
			r2.Header.Set(t.headerName, t.token)
		}
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(r2)
}

func fingerprint(srv BoundServer) string {
	return strings.Join([]string{srv.URL, srv.AuthKind, srv.HeaderName, srv.Token}, "\x00")
}

func (c *Client) httpFor(srv BoundServer) *http.Client {
	if srv.AuthKind == "" || srv.AuthKind == "none" {
		return c.httpClient
	}
	return &http.Client{
		Timeout: c.httpClient.Timeout,
		Transport: &authTransport{
			base:       c.httpClient.Transport,
			authKind:   srv.AuthKind,
			headerName: srv.HeaderName,
			token:      srv.Token,
		},
	}
}

// connect performs a fresh initialize handshake.
//
// The SDK infers advertised capabilities from which handlers are set, so leaving
// CreateMessageHandler and ElicitationHandler nil is exactly how Rookery declines
// sampling and elicitation. Do not add them without reading the package comment.
//
// DisableStandaloneSSE is set because this wave does not consume server-initiated
// notifications (tools/list_changed is deferred to TTL-aware polling). Holding a
// persistent GET stream per pooled session would buy nothing and cost a connection.
func (c *Client) connect(ctx context.Context, srv BoundServer) (*sdk.ClientSession, error) {
	if srv.URL == "" {
		return nil, errf(KindBadArgs, "MCP server "+srv.Name+" has no URL")
	}
	cl := sdk.NewClient(&sdk.Implementation{Name: clientName, Version: clientVersion}, nil)
	tr := &sdk.StreamableClientTransport{
		Endpoint:             srv.URL,
		HTTPClient:           c.httpFor(srv),
		DisableStandaloneSSE: true,
	}
	dctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	sess, err := cl.Connect(dctx, tr, nil)
	if err != nil {
		return nil, classifyDialError(srv, err)
	}
	return sess, nil
}

// classifyDialError decides between "this credential was rejected" and "this server
// did not answer".
//
// The split is the whole reason the caller can safely flip a server to NEEDS_AUTH:
// only a definitive 401/403 is a rejection. A 5xx, a timeout or a DNS failure is
// UNREACHABLE, which does not alert and does not remove the server from the retry
// path. Anything unrecognised fails OPEN to UNREACHABLE — one more retry is cheap,
// a wrongly disabled server is not.
func classifyDialError(srv BoundServer, err error) error {
	msg := err.Error()
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "401") || strings.Contains(low, "unauthorized"):
		return errf(KindAuth, fmt.Sprintf("MCP server %q rejected the credential (401). Reconnect it with a valid token.", srv.Name))
	case strings.Contains(low, "403") || strings.Contains(low, "forbidden"):
		return errf(KindAuth, fmt.Sprintf("MCP server %q refused access (403). The credential may lack the required scope.", srv.Name))
	case strings.Contains(low, "www-authenticate") || strings.Contains(low, "oauth"):
		// Wave 1 is static-token only. Saying so beats an opaque 401, because the
		// owner's next move is different: they cannot fix this by pasting a token.
		return errf(KindUnsupported, fmt.Sprintf("MCP server %q requires OAuth, which this version does not support yet (static tokens only).", srv.Name))
	}
	return errf(KindUnreachable, fmt.Sprintf("MCP server %q is unreachable: %s", srv.Name, msg))
}

// session returns a pooled session, opening one if needed.
func (c *Client) session(ctx context.Context, srv BoundServer) (*sdk.ClientSession, error) {
	fp := fingerprint(srv)
	c.mu.Lock()
	if p, ok := c.sessions[srv.ID]; ok {
		if p.fingerprint == fp && time.Since(p.lastUsed) < idleTTL {
			p.lastUsed = time.Now()
			c.mu.Unlock()
			return p.sess, nil
		}
		// Stale or reconfigured — drop it.
		delete(c.sessions, srv.ID)
		go p.sess.Close()
	}
	c.mu.Unlock()

	sess, err := c.connect(ctx, srv)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.sessions[srv.ID] = &pooled{sess: sess, lastUsed: time.Now(), fingerprint: fp}
	c.mu.Unlock()
	return sess, nil
}

// drop discards a pooled session, so the next call reconnects.
func (c *Client) drop(serverID string) {
	c.mu.Lock()
	p, ok := c.sessions[serverID]
	delete(c.sessions, serverID)
	c.mu.Unlock()
	if ok {
		_ = p.sess.Close()
	}
}

// Close tears down every pooled session. Called on shutdown.
func (c *Client) Close() {
	c.mu.Lock()
	sessions := c.sessions
	c.sessions = map[string]*pooled{}
	c.mu.Unlock()
	for _, p := range sessions {
		_ = p.sess.Close()
	}
}

// Catalog is one tools/list snapshot.
type Catalog struct {
	Tools []DiscoveredTool
	// TTLMs is the server's own cache hint, or 0 when it supplied none.
	TTLMs      int
	ServerInfo string
}

// DiscoveredTool is a tool as the server described it, before the owner's overrides
// are applied. ReadOnlyHint is carried separately from Tool.ReadOnly precisely to
// keep "what the server claimed" distinct from "what the owner decided".
type DiscoveredTool struct {
	Name         string
	Title        string
	Description  string
	InputSchema  json.RawMessage
	ReadOnlyHint bool
}

// ListTools runs the discovery handshake and returns the server's catalog.
//
// Pagination is handled by the SDK's Tools iterator, which follows nextCursor.
func (c *Client) ListTools(ctx context.Context, srv BoundServer) (Catalog, error) {
	sess, err := c.session(ctx, srv)
	if err != nil {
		return Catalog{}, err
	}

	var cat Catalog
	if init := sess.InitializeResult(); init != nil {
		info := map[string]any{"protocol_version": init.ProtocolVersion}
		if init.ServerInfo != nil {
			info["name"] = init.ServerInfo.Name
			info["version"] = init.ServerInfo.Version
		}
		if b, err := json.Marshal(info); err == nil {
			cat.ServerInfo = string(b)
		}
	}

	// One page is fetched directly first so the TTL hint is available: the iterator
	// hides the per-page result that carries it.
	page, err := sess.ListTools(ctx, nil)
	if err != nil {
		c.drop(srv.ID)
		return Catalog{}, errf(KindUnreachable, fmt.Sprintf("MCP server %q failed to list tools: %s", srv.Name, err.Error()))
	}
	cat.TTLMs = page.GetTTLMs()

	seen := map[string]bool{}
	appendTools := func(tools []*sdk.Tool) {
		for _, t := range tools {
			if t == nil || t.Name == "" || seen[t.Name] {
				continue
			}
			schema, ok := normalizeSchema(t.InputSchema)
			if !ok {
				// A tool whose schema we cannot represent is EXCLUDED rather than
				// allowed to fail the whole list — one malformed definition must not
				// take out the other tools on this server. The MCP spec requires this
				// same containment for a malformed x-mcp-header.
				continue
			}
			seen[t.Name] = true
			dt := DiscoveredTool{
				Name:        t.Name,
				Title:       t.Title,
				Description: t.Description,
				InputSchema: schema,
			}
			if t.Annotations != nil {
				dt.ReadOnlyHint = t.Annotations.ReadOnlyHint
			}
			cat.Tools = append(cat.Tools, dt)
		}
	}
	appendTools(page.Tools)

	if page.NextCursor != "" {
		for t, err := range sess.Tools(ctx, nil) {
			if err != nil {
				// Partial catalogs are worse than none: reconcile would mark every
				// unlisted tool missing and disable the owner's working setup.
				c.drop(srv.ID)
				return Catalog{}, errf(KindUnreachable, fmt.Sprintf("MCP server %q failed mid-pagination: %s", srv.Name, err.Error()))
			}
			appendTools([]*sdk.Tool{t})
		}
	}
	return cat, nil
}

// CallTool performs one tools/call, retrying once against a fresh session.
//
// The retry covers exactly one failure mode and is worth its complexity: a pooled
// session whose server restarted or expired it is indistinguishable from a real
// failure until you try again. It is NOT a general retry — a server that answers and
// reports an error is not retried, or a failing mutating call could run twice.
func (c *Client) CallTool(ctx context.Context, srv BoundServer, tool string, args map[string]any) (Result, error) {
	res, err := c.callOnce(ctx, srv, tool, args)
	if err == nil {
		return res, nil
	}
	var e *Error
	if errors.As(err, &e) && e.Kind == KindUnreachable {
		c.drop(srv.ID)
		return c.callOnce(ctx, srv, tool, args)
	}
	return res, err
}

func (c *Client) callOnce(ctx context.Context, srv BoundServer, tool string, args map[string]any) (Result, error) {
	sess, err := c.session(ctx, srv)
	if err != nil {
		return Result{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	out, err := sess.CallTool(cctx, &sdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		// A JSON-RPC protocol error (unknown tool, malformed request) or a transport
		// failure. Both are real errors — as opposed to isError:true, which is the
		// tool reporting a problem the model can fix.
		return Result{}, errf(KindUnreachable, fmt.Sprintf("MCP call %s on %q failed: %s", tool, srv.Name, err.Error()))
	}
	return mapCallResult(out)
}

// normalizeSchema coerces a server's inputSchema into the JSON-Schema object a
// provider will accept.
//
// A provider rejects a function whose parameters are absent or not an object, and it
// rejects the WHOLE tool list when one entry is bad — so a server omitting
// inputSchema (permitted: the field is only SHOULD-shaped in practice) would break
// every other tool the agent has. Returns ok=false for a schema that is present but
// unusable, which the caller treats as "exclude this tool".
func normalizeSchema(raw any) (json.RawMessage, bool) {
	if raw == nil {
		return emptyObjectSchema, true
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, false
	}
	trimmed := strings.TrimSpace(string(b))
	if trimmed == "" || trimmed == "null" {
		return emptyObjectSchema, true
	}
	if !strings.HasPrefix(trimmed, "{") {
		return nil, false
	}
	return json.RawMessage(trimmed), true
}
