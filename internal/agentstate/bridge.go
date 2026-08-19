package agentstate

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

// maxBridgeResult mirrors connectors.maxBridgeResult / mcp.maxBridgeResult /
// coder.maxToolResult: one cap, applied identically wherever a tool result
// reaches a model. It is independent of MaxStateSize (which bounds what may be
// WRITTEN to state.md) — this bounds what is handed back to the CLI subprocess
// on a single call, the same distinction the other bridges draw.
const maxBridgeResult = 8 * 1024

// Bridge lets a CLI coder subprocess reach the SAME agentstate.Get/Apply path
// the API engine's get_state/set_state tools call in-process, so switching
// coder kind cannot change what an agent can do with its own memory.
//
// It is a sibling of connectors.Bridge, vault.Bridge and mcp.Bridge — same
// ephemeral loopback listener, same per-run bearer token in a sessions map
// guarded by a mutex, same 8 KiB result cap — deliberately kept separate from
// all three rather than added as a route on one of them, so a future reader
// copying the pattern by analogy finds one shape everywhere.
//
// A session is scoped to one agent's DIRECTORY, not to a workspace: unlike the
// KB bridge (which searches/converts across the whole vault) or the connector
// and MCP bridges (which act on named external accounts), a state operation
// always targets exactly one agent's state.md, so the path is fixed at
// Register time rather than resolved per call.
type Bridge struct {
	mu       sync.Mutex
	sessions map[string]*bridgeSession
	addr     string
}

type bridgeSession struct {
	agentDir  string
	agentName string
}

// NewBridge creates an unstarted bridge.
func NewBridge() *Bridge {
	return &Bridge{sessions: map[string]*bridgeSession{}}
}

// Start binds a loopback listener on an ephemeral port and serves until ctx is
// done, mirroring mcp.Bridge.Start exactly.
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

// Register scopes a new per-run token to one agent's directory + name.
// Call Unregister when the run ends.
func (b *Bridge) Register(agentDir, agentName string) string {
	tok := randomToken()
	b.mu.Lock()
	b.sessions[tok] = &bridgeSession{agentDir: agentDir, agentName: agentName}
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

type setRequest struct {
	Patch map[string]any `json:"patch"`
}

func (b *Bridge) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/state/get", func(w http.ResponseWriter, r *http.Request) {
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
		// The request body carries no fields today (mirrors the round-trip
		// test's `{}`), but is still drained so a client that sends one isn't
		// left with a stuck connection.
		_, _ = io.ReadAll(io.LimitReader(r.Body, 1<<20))

		st, understood, err := Get(StateFilePath(sess.agentDir))
		if err != nil {
			// 200 with an error field, matching the connector/MCP/KB bridges: the
			// CLI client prints this for the model to read, and a non-2xx would
			// look like a broken bridge rather than a readable-but-refused call.
			writeJSON(w, http.StatusOK, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, capBridgeState(st, understood))
	})
	mux.HandleFunc("/state/set", func(w http.ResponseWriter, r *http.Request) {
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
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var req setRequest
		if len(strings.TrimSpace(string(body))) > 0 {
			if err := json.Unmarshal(body, &req); err != nil {
				writeJSON(w, http.StatusOK, map[string]any{"error": "bad request body"})
				return
			}
		}
		st, err := Apply(StateFilePath(sess.agentDir), sess.agentName, req.Patch)
		if err != nil {
			// 200 with an error field (e.g. a patch that would push state.md over
			// MaxStateSize) — same convention as the get handler above and every
			// sibling bridge: a refused call is not a broken bridge.
			writeJSON(w, http.StatusOK, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, capBridgeState(st, true))
	})
	return mux
}

// capBridgeState bounds a state object for the wire, the same shape every
// other bridge's capBridgeData draws: under the cap the state rides unchanged;
// over it, "state" becomes a truncated STRING plus an explicit truncated/note
// pair, because a JSON value cut in place would still parse and read to the
// model as complete data. The cap is on the REPLY only — it never affects what
// was actually merged into state.md, so a truncated get is always safe to
// retry with a narrower question.
func capBridgeState(st map[string]any, understood bool) map[string]any {
	body, err := json.Marshal(st)
	if err != nil {
		body = []byte("{}")
	}
	if len(body) <= maxBridgeResult {
		return map[string]any{"state": json.RawMessage(body), "understood": understood}
	}
	return map[string]any{
		"state":      string(body[:maxBridgeResult]) + "…",
		"understood": understood,
		"truncated":  true,
		"note": "state exceeded " + strconv.Itoa(maxBridgeResult) +
			" bytes and was cut for this reply — the stored state.md is unaffected. " +
			"Ask for a narrower key.",
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func randomToken() string {
	buf := make([]byte, 24)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
