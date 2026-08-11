package mcp

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

// maxBridgeResult mirrors connectors.maxBridgeResult and coder.maxToolResult: one
// cap, applied identically wherever a tool result reaches a model.
//
// It matters more for MCP than for a connector. A connector action's response shape
// is known from vendored YAML; an MCP server returns whatever it likes, and a
// browser-automation or log-query server will happily return megabytes.
const maxBridgeResult = 8 * 1024

// capBridgeData bounds a result for the wire. Under the cap the envelope is
// unchanged. Over it, data becomes a truncated STRING plus an explicit
// truncated/note pair — cutting a JSON value in place would produce something that
// still parses and reads to the model as complete data, which is the worst outcome
// available.
func capBridgeData(data json.RawMessage) map[string]any {
	if len(data) <= maxBridgeResult {
		return map[string]any{"data": data}
	}
	return map[string]any{
		"data":      string(data[:maxBridgeResult]) + "…",
		"truncated": true,
		"note": "response exceeded " + strconv.Itoa(maxBridgeResult) +
			" bytes and was cut. Re-run with a narrower query — a smaller limit, " +
			"a shorter range, or fewer fields.",
	}
}

// Bridge lets a CLI coder subprocess reach the SAME mcp.Execute path the API engine
// calls in-process, so both coder kinds are gated identically.
//
// It is a sibling of connectors.Bridge rather than a route on it, for one structural
// reason: internal/connectors is a self-contained integration layer and must not
// import internal/mcp to serve it. The cost is a second ephemeral listener and a
// second per-run token — which the design already implied by giving the CLI its own
// ROOKERY_MCP_URL / ROOKERY_MCP_TOKEN pair so the two could never become coupled.
//
// The credential never leaves the host: the subprocess holds only a run-scoped bearer
// token, and this process resolves it to the real server credential. Landlock
// restricts the filesystem, not loopback TCP, so a sandboxed coder can still reach
// it. That property is exactly what native --mcp-config passthrough would have given
// up, since it requires writing the MCP token to a file the subprocess reads.
type Bridge struct {
	client Caller

	mu       sync.Mutex
	sessions map[string]*bridgeSession
	addr     string
}

type bridgeSession struct {
	workspaceID string
	bound       []BoundServer
	buildPhase  bool
	parker      Parker
}

// NewBridge creates a bridge over a Caller (an *mcp.Client in production).
func NewBridge(c Caller) *Bridge {
	return &Bridge{client: c, sessions: map[string]*bridgeSession{}}
}

// Start binds a loopback listener on an ephemeral port and serves until ctx is done.
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

// Register scopes a new per-run token to the run's bound servers.
func (b *Bridge) Register(workspaceID string, bound []BoundServer, buildPhase bool) string {
	return b.RegisterGated(workspaceID, bound, buildPhase, nil)
}

// RegisterGated is Register plus the approval gate for this run.
//
// A CLI coder must be gated identically to the API engine, or switching coder kind
// would silently disable the owner's approval setting.
func (b *Bridge) RegisterGated(workspaceID string, bound []BoundServer, buildPhase bool, parker Parker) string {
	tok := randomToken()
	b.mu.Lock()
	b.sessions[tok] = &bridgeSession{workspaceID: workspaceID, bound: bound, buildPhase: buildPhase, parker: parker}
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
	mux.HandleFunc("/mcp/exec", func(w http.ResponseWriter, r *http.Request) {
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
		srv, tool, ok := ResolveTool(sess.bound, req.Tool)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown MCP tool " + req.Tool})
			return
		}
		res, err := Execute(r.Context(), b.client, srv, tool, req.Args,
			Policy{BuildPhase: sess.buildPhase, Parker: sess.parker})
		if err != nil {
			// 200 with an error field, matching the connector bridge: the CLI client
			// prints this for the model to read, and a non-2xx would look like a
			// broken bridge rather than a refused call.
			writeJSON(w, http.StatusOK, map[string]string{"error": err.Error()})
			return
		}
		out := capBridgeData(res.Data)
		if res.IsError {
			// Carried as a FLAG, not an "error" field. The CLI client renders it
			// without the `error:` prefix so a self-correctable tool failure is not
			// counted as a failing call by the coder's own loop guard.
			out["tool_error"] = true
		}
		writeJSON(w, http.StatusOK, out)
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
