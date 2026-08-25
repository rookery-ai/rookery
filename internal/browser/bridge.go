package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Environment variables the server writes into a CLI coder's subprocess so it
// can reach this bridge. Internal, like the connector/KB/MCP equivalents.
const (
	EnvBridgeURL   = "ROOKERY_BROWSER_URL"
	EnvBridgeToken = "ROOKERY_BROWSER_TOKEN_BRIDGE"
)

// Bridge lets a CLI coder subprocess reach the same browser path the API
// engine's native tools reach in-process.
//
// This is the fifth instance of a pattern the codebase already uses for
// connectors, MCP, the knowledge base and agent state, and it exists for a rule
// stated plainly elsewhere: changing coder kind must never change what an agent
// can do. Without it, an install running Claude Code would silently have no
// browser while an API-engine install had one, and both would share a system
// prompt claiming the tool exists.
type Bridge struct {
	mgr     *Manager
	resolve SecretResolver

	mu   sync.Mutex
	sess map[string]*bridgeSession
	addr string
}

type bridgeSession struct {
	workspaceID string
	// contextKey names the browser context this run owns. It is minted HERE
	// from the run's identity and never accepted from the caller — a
	// caller-supplied key would let one agent attach to another agent's logged-in
	// browser session, which is the whole store of credentials this feature
	// creates.
	contextKey string
	policy     Policy
}

func NewBridge(mgr *Manager, resolve SecretResolver) *Bridge {
	return &Bridge{mgr: mgr, resolve: resolve, sess: map[string]*bridgeSession{}}
}

// Start binds a loopback listener and serves until ctx is cancelled, mirroring
// connectors.Bridge.Start so the loopback bridges share one lifecycle shape.
func (b *Bridge) Start(ctx context.Context) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("browser bridge listen: %w", err)
	}
	b.addr = "http://" + ln.Addr().String()
	srv := &http.Server{Handler: b.handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	go func() { _ = srv.Serve(ln) }()
	return b.addr, nil
}

func (b *Bridge) Addr() string { return b.addr }

// Register scopes a token to one run.
func (b *Bridge) Register(workspaceID, contextKey string, pol Policy) string {
	tok, err := randomToken()
	if err != nil {
		return ""
	}
	b.mu.Lock()
	b.sess[tok] = &bridgeSession{workspaceID: workspaceID, contextKey: contextKey, policy: pol}
	b.mu.Unlock()
	return tok
}

func (b *Bridge) Unregister(token string) {
	b.mu.Lock()
	s := b.sess[token]
	delete(b.sess, token)
	b.mu.Unlock()
	if s != nil && s.contextKey != "" {
		b.mgr.CloseSession(context.Background(), s.contextKey)
	}
}

func (b *Bridge) session(token string) *bridgeSession {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sess[token]
}

func (b *Bridge) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/read", b.serve(b.handleRead))
	mux.HandleFunc("/act", b.serve(b.handleAct))
	return mux
}

func (b *Bridge) serve(fn func(context.Context, *bridgeSession, map[string]any) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess := b.session(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if sess == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&payload); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		out, err := fn(r.Context(), sess, payload)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			// A refusal or a page failure is DATA the model must read and react
			// to, not a transport error — the same reason connectors.Bridge
			// returns its taxonomy in the body.
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(out)
	}
}

func (b *Bridge) handleRead(ctx context.Context, sess *bridgeSession, p map[string]any) (any, error) {
	res, err := b.mgr.Render(ctx, Request{
		URL:     str(p, "url"),
		WaitFor: str(p, "wait_for"),
		Offset:  num(p, "offset"),
		Session: sess.contextKey,
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (b *Bridge) handleAct(ctx context.Context, sess *bridgeSession, p map[string]any) (any, error) {
	action := Action(str(p, "action"))
	ref := str(p, "ref")

	// The element's name is needed for the irreversibility check, and the model
	// does not supply it — it supplies a ref. Reading the CURRENT page is what
	// makes the check meaningful: a name taken from the model's last listing
	// could describe a control the page has since replaced.
	name := ""
	if IsMutating(action) && ref != "" {
		found, ok := b.currentElementName(ctx, sess.contextKey, ref)
		if !ok {
			return nil, fmt.Errorf("ref %q is not on the page any more — read the page again and use a ref from the new listing", ref)
		}
		name = found
	}
	if err := CheckAct(sess.policy, action, name); err != nil {
		return nil, err
	}

	value, isSecret, err := ResolveSecretValue(ctx, b.resolve, sess.workspaceID, str(p, "value"))
	if err != nil {
		return nil, err
	}
	return b.mgr.Act(ctx, ActRequest{
		Session:       sess.contextKey,
		Action:        action,
		Ref:           ref,
		Value:         value,
		Key:           str(p, "key"),
		WaitFor:       str(p, "wait_for"),
		ValueIsSecret: isSecret,
	})
}

// currentElementName reads the LIVE page to find what a ref currently points
// at, and reports whether it could be identified at all.
//
// It re-reads rather than trusting a name the caller passes in, because the
// irreversibility check is only as good as the name it judges: a name from the
// model's previous listing may describe a control the page has since
// re-rendered — which is exactly the situation where a "Next" button has become
// a "Pay now" button.
//
// When the ref cannot be identified the caller REFUSES rather than proceeding
// with an empty name. An empty name matches no irreversible hint, so continuing
// would quietly demote an unidentifiable click to the lower grant — fail-open on
// the one check that guards payments. Refusing costs nothing real: a ref the
// page no longer contains cannot be clicked anyway, so the alternative outcome
// was a failure with a worse error message.
func (b *Bridge) currentElementName(ctx context.Context, contextKey, ref string) (string, bool) {
	cur, err := b.mgr.Act(ctx, ActRequest{Session: contextKey, Action: ActionRead})
	if err != nil {
		return "", false
	}
	for _, e := range cur.Elements {
		if e.Ref == ref {
			return e.Name, true
		}
	}
	return "", false
}

func str(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func num(m map[string]any, k string) int {
	switch v := m[k].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

// CallBridge posts one request from the CLI subcommand to the bridge.
func CallBridge(ctx context.Context, base, token, path string, payload map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+path, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 3 * time.Minute}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}
