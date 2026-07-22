package vault

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Bridge lets a CLI coder subprocess reach the knowledge base's conversion and
// search paths in the host process. It mirrors connectors.Bridge: a loopback
// listener plus a per-run bearer token scoped to exactly one workspace, so a
// coder can never read or write another tenant's vault. Landlock confines the
// filesystem, not loopback TCP, so a sandboxed child can still reach it.
type Bridge struct {
	v   *Vault
	mu  sync.RWMutex
	tok map[string]string // token → workspaceID
	srv *http.Server
	ln  net.Listener
}

func NewBridge(v *Vault) *Bridge {
	return &Bridge{v: v, tok: map[string]string{}}
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

// Register issues a token scoped to one workspace and returns it.
func (b *Bridge) Register(workspaceID string) string {
	buf := make([]byte, 24)
	_, _ = rand.Read(buf)
	token := base64.RawURLEncoding.EncodeToString(buf)
	b.mu.Lock()
	b.tok[token] = workspaceID
	b.mu.Unlock()
	return token
}

// Unregister revokes a token when its run ends.
func (b *Bridge) Unregister(token string) {
	b.mu.Lock()
	delete(b.tok, token)
	b.mu.Unlock()
}

// authorize maps a request's bearer token to its workspace.
func (b *Bridge) authorize(r *http.Request) (string, bool) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" {
		return "", false
	}
	b.mu.RLock()
	ws, ok := b.tok[token]
	b.mu.RUnlock()
	return ws, ok
}

func (b *Bridge) handleConvert(w http.ResponseWriter, r *http.Request) {
	ws, ok := b.authorize(r)
	if !ok {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	var req struct {
		Filename  string `json:"filename"`
		Content   string `json:"content"` // base64
		SourceURL string `json:"source_url"`
		DestDir   string `json:"dest_dir"`
		Title     string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}
	data, err := base64.StdEncoding.DecodeString(req.Content)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "content must be base64"})
		return
	}
	res, err := b.v.ImportFile(ws, ImportInput{
		Data: data, Filename: req.Filename, SourceURL: req.SourceURL,
		DestDir: req.DestDir, Title: req.Title,
	})
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"note_path": res.NotePath, "original_path": res.OriginalPath,
		"kind": res.Kind, "extractor": res.Extractor, "warnings": res.Warnings,
	})
}

func (b *Bridge) handleSearch(w http.ResponseWriter, r *http.Request) {
	ws, ok := b.authorize(r)
	if !ok {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	var req struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	hits, err := b.v.NewSearcher().Search(ctx, ws, req.Query)
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
