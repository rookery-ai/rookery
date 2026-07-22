package vault

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ilijad1/simple-agents/internal/convert"
)

const (
	// maxConvertBody bounds the /convert request body. /convert carries a whole
	// document as base64 inside JSON: base64 inflates raw bytes by 4/3, so the
	// 25 MiB document the web upload path allows (25 MiB = 26,214,400 bytes)
	// becomes 26,214,400 * 4/3 = 34,952,533.33 bytes (~33.33 MiB) once encoded.
	// Round up to 36 MiB to leave headroom for the JSON envelope itself
	// (field names/quoting plus filename/title/dest_dir/source_url) without
	// clipping a legitimate upload.
	maxConvertBody = 36 << 20 // 36 MiB

	// maxSearchBody bounds the /search request body, which carries only a
	// query string — no document payload — so a small cap stops an oversized
	// body without ever affecting a real caller.
	maxSearchBody = 64 << 10 // 64 KiB
)

// Bridge lets a CLI coder subprocess reach the knowledge base's conversion and
// search paths in the host process. It mirrors connectors.Bridge: a loopback
// listener plus a per-run bearer token scoped to exactly one workspace, so a
// coder can never read or write another tenant's vault. Landlock confines the
// filesystem, not loopback TCP, so a sandboxed child can still reach it.
type Bridge struct {
	v    *Vault
	mu   sync.RWMutex
	sess map[string]bridgeSession // token → session
	srv  *http.Server
	ln   net.Listener
}

// bridgeSession is what a token resolves to: the workspace it is scoped to,
// plus whether it was issued for a build (see ImportInput.BuildPhase).
type bridgeSession struct {
	workspaceID string
	buildPhase  bool
}

func NewBridge(v *Vault) *Bridge {
	return &Bridge{v: v, sess: map[string]bridgeSession{}}
}

// Start binds a loopback listener on an ephemeral port and serves the bridge.
func (b *Bridge) Start() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("kb bridge listen: %w", err)
	}
	b.ln = ln
	mux := http.NewServeMux()
	mux.HandleFunc("/convert", b.handleConvert)
	mux.HandleFunc("/search", b.handleSearch)
	b.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = b.srv.Serve(ln) }()
	return nil
}

func (b *Bridge) Close() {
	if b.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = b.srv.Shutdown(ctx)
	}
}

// URL is the base a subprocess should POST to, or "" when not started.
func (b *Bridge) URL() string {
	if b.ln == nil {
		return ""
	}
	return "http://" + b.ln.Addr().String()
}

// Register issues a token scoped to one workspace and returns it. buildPhase
// marks the token as issued for an agent/skill build — see
// ImportInput.BuildPhase — so a future build-time bridge registration is
// guarded by construction rather than relying on the caller remembering to
// check it elsewhere (mirrors connectors.Bridge.Register's shape).
func (b *Bridge) Register(workspaceID string, buildPhase bool) string {
	buf := make([]byte, 24)
	_, _ = rand.Read(buf)
	token := base64.RawURLEncoding.EncodeToString(buf)
	b.mu.Lock()
	b.sess[token] = bridgeSession{workspaceID: workspaceID, buildPhase: buildPhase}
	b.mu.Unlock()
	return token
}

// Unregister revokes a token when its run ends.
func (b *Bridge) Unregister(token string) {
	b.mu.Lock()
	delete(b.sess, token)
	b.mu.Unlock()
}

// authorize maps a request's bearer token to its session.
func (b *Bridge) authorize(r *http.Request) (bridgeSession, bool) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" {
		return bridgeSession{}, false
	}
	b.mu.RLock()
	sess, ok := b.sess[token]
	b.mu.RUnlock()
	return sess, ok
}

// readCapped reads at most limit+1 bytes so an over-limit body is detected
// directly (and reported with a clear message) rather than silently truncated
// into a request that then fails with an opaque "bad request" JSON error.
func readCapped(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("request body exceeds the %d byte limit", limit)
	}
	return data, nil
}

func (b *Bridge) handleConvert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
		return
	}
	sess, ok := b.authorize(r)
	if !ok {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	body, err := readCapped(r.Body, maxConvertBody)
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": err.Error()})
		return
	}
	var req struct {
		Filename  string `json:"filename"`
		Content   string `json:"content"` // base64
		SourceURL string `json:"source_url"`
		DestDir   string `json:"dest_dir"`
		Title     string `json:"title"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}
	data, err := base64.StdEncoding.DecodeString(req.Content)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "content must be base64"})
		return
	}
	res, err := b.v.ImportFile(sess.workspaceID, ImportInput{
		Data: data, Filename: req.Filename, SourceURL: req.SourceURL,
		DestDir: req.DestDir, Title: req.Title, BuildPhase: sess.buildPhase,
	})
	if err != nil {
		// Same distinction the web upload handler makes (web/api_kb.go's
		// uploadErrStatus): a bad format / rejected destination is a request
		// property the calling coder can act on (pick a different file, a
		// different dir); a genuine disk fault is a server problem the coder
		// cannot fix by retrying differently, so it gets its own status and a
		// message that doesn't claim the file itself was the issue.
		if errors.Is(err, convert.ErrUnsupportedFormat) || errors.Is(err, ErrSystemDir) || errors.Is(err, ErrEscapes) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not save this file to the knowledge base"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"note_path": res.NotePath, "original_path": res.OriginalPath,
		"kind": res.Kind, "extractor": res.Extractor, "warnings": res.Warnings,
	})
}

func (b *Bridge) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
		return
	}
	sess, ok := b.authorize(r)
	if !ok {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	body, err := readCapped(r.Body, maxSearchBody)
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": err.Error()})
		return
	}
	var req struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	hits, err := b.v.NewSearcher().Search(ctx, sess.workspaceID, req.Query)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	var sb strings.Builder
	for _, h := range hits {
		fmt.Fprintf(&sb, "%s:%d: %s\n", h.Path, h.Line, h.Snippet)
	}
	if sb.Len() == 0 {
		sb.WriteString(fmt.Sprintf("(no matches for %q)\n", req.Query))
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": sb.String()})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
