package connectors

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// maxBridgeResult mirrors coder.maxToolResult: the API engine truncates a connector result
// before it reaches the model, and a CLI coder reading this bridge must not be handed an
// unbounded one. Analytics and ad-insights responses are the payloads that make the
// difference — a 30-day report runs to megabytes and would otherwise land whole in a
// coder's context.
const maxBridgeResult = 8 * 1024

// capBridgeData bounds a connector result for the wire. Under the cap the response is
// unchanged: {"data": <original json>}. Over it, data becomes a truncated STRING plus an
// explicit truncated/note pair — cutting a JSON value in place would produce something
// that still parses as data and reads as complete, which is the worst of both.
func capBridgeData(data json.RawMessage) map[string]any {
	if len(data) <= maxBridgeResult {
		return map[string]any{"data": data}
	}
	return map[string]any{
		"data":      string(data[:maxBridgeResult]) + "…",
		"truncated": true,
		"note": "response exceeded " + strconv.Itoa(maxBridgeResult) +
			" bytes and was cut. Re-run with a narrower query — a shorter date range, " +
			"a smaller limit, or fewer dimensions.",
	}
}

// Bridge lets a CLI coder subprocess reach the SAME connectors.Execute path the API
// engine calls in-process. It runs a loopback-only HTTP listener in the host process
// (which holds the DB + system key, unsandboxed); a sandboxed coder subprocess POSTs to
// it (Landlock restricts the filesystem, not loopback TCP), so OAuth tokens never leave
// the host. Each run gets a short-lived bearer token scoped to that run's bound
// connections — the subprocess can only act on connections the agent declared.
type Bridge struct {
	reg    *Registry
	store  TokenStore
	client *http.Client

	mu       sync.Mutex
	sessions map[string]*bridgeSession // token -> session
	addr     string
}

type bridgeSession struct {
	workspaceID string
	bound       []BoundConn
	buildPhase  bool
}

// NewBridge creates a bridge over the given registry + token store. client may be nil.
func NewBridge(reg *Registry, store TokenStore, client *http.Client) *Bridge {
	return &Bridge{reg: reg, store: store, client: client, sessions: map[string]*bridgeSession{}}
}

// Start binds a loopback listener on an ephemeral port and serves the exec handler until
// ctx is cancelled. Returns the base URL (http://127.0.0.1:<port>).
func (b *Bridge) Start(ctx context.Context) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
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

// Addr is the base URL the listener is bound to (empty until Start).
func (b *Bridge) Addr() string { return b.addr }

// Register scopes a new per-run token to the run's bound connections and returns it.
// Call Unregister when the run finishes.
func (b *Bridge) Register(workspaceID string, bound []BoundConn, buildPhase bool) string {
	tok := randomToken()
	b.mu.Lock()
	b.sessions[tok] = &bridgeSession{workspaceID: workspaceID, bound: bound, buildPhase: buildPhase}
	b.mu.Unlock()
	return tok
}

// Unregister drops a run token so it can no longer be used.
func (b *Bridge) Unregister(token string) {
	b.mu.Lock()
	delete(b.sessions, token)
	b.mu.Unlock()
}

func (b *Bridge) session(token string) *bridgeSession {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sessions[token]
}

type execRequest struct {
	Tool string         `json:"tool"`
	Args map[string]any `json:"args"`
}

func (b *Bridge) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/exec", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		sess := b.session(token)
		if sess == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var req execRequest
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request body"})
			return
		}
		conn, action, ok := b.reg.ResolveTool(sess.bound, req.Tool)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown connector tool " + req.Tool})
			return
		}
		res, err := Execute(r.Context(), b.reg, b.store, b.client,
			ConnRef{ID: conn.ID, Provider: conn.Provider, AccountIdentity: conn.AccountIdentity, Extra: conn.Extra},
			action, req.Args, Policy{BuildPhase: sess.buildPhase})
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, capBridgeData(res.Data))
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func randomToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
