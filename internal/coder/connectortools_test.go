package coder

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/connectors"
	"github.com/rookery-ai/rookery/internal/llm"
)

type fakeTokenStore struct{ tok string }

func (f fakeTokenStore) AccessToken(context.Context, connectors.ConnRef) (string, error) {
	return f.tok, nil
}

func loadReg(t *testing.T) *connectors.Registry {
	r, err := connectors.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestConnectorToolsSingleAccountBareNames(t *testing.T) {
	h := &hostToolSet{
		connReg:    loadReg(t),
		boundConns: []connectors.BoundConn{{ID: "c1", Provider: "google", AccountLabel: "work"}},
	}
	names := map[string]bool{}
	for _, tl := range h.connectorTools() {
		names[tl.Name] = true
	}
	if !names["gmail_send_email"] || !names["gmail_search"] {
		t.Fatalf("expected bare gmail tools, got %v", names)
	}
	for n := range names {
		if strings.Contains(n, "__") {
			t.Fatalf("single account must not suffix: %s", n)
		}
	}
}

func TestConnectorToolsMultiAccountSuffixed(t *testing.T) {
	h := &hostToolSet{
		connReg: loadReg(t),
		boundConns: []connectors.BoundConn{
			{ID: "c1", Provider: "google", AccountLabel: "work"},
			{ID: "c2", Provider: "google", AccountLabel: "personal"},
		},
	}
	names := map[string]bool{}
	for _, tl := range h.connectorTools() {
		names[tl.Name] = true
	}
	if !names["gmail_send_email__work"] || !names["gmail_send_email__personal"] {
		t.Fatalf("expected suffixed tools, got %v", names)
	}
	if names["gmail_send_email"] {
		t.Fatalf("bare name must not appear when multi-account")
	}
}

func TestConnectorToolsNoneWhenUnbound(t *testing.T) {
	h := &hostToolSet{connReg: loadReg(t)}
	if got := h.connectorTools(); got != nil {
		t.Fatalf("unbound agent must expose no connector tools, got %v", got)
	}
}

func TestResolveConnectorCall(t *testing.T) {
	h := &hostToolSet{
		connReg: loadReg(t),
		boundConns: []connectors.BoundConn{
			{ID: "c1", Provider: "google", AccountLabel: "work"},
			{ID: "c2", Provider: "google", AccountLabel: "personal"},
		},
	}
	conn, action, ok := h.resolveConnectorTool("gmail_send_email__personal")
	if !ok || conn.ID != "c2" || action != "gmail_send_email" {
		t.Fatalf("resolve: %+v %q %v", conn, action, ok)
	}
	if _, _, ok := h.resolveConnectorTool("read_file"); ok {
		t.Fatal("non-connector tool must not resolve")
	}
}

func TestResolveConnectorSingleAccountBare(t *testing.T) {
	h := &hostToolSet{
		connReg:    loadReg(t),
		boundConns: []connectors.BoundConn{{ID: "c1", Provider: "google", AccountLabel: "work"}},
	}
	conn, action, ok := h.resolveConnectorTool("gmail_search")
	if !ok || conn.ID != "c1" || action != "gmail_search" {
		t.Fatalf("resolve bare: %+v %q %v", conn, action, ok)
	}
}

// TestConnectorToolNamesAreProviderSafe guards against free-text labels producing tool
// names the LLM provider would reject (must match ^[a-zA-Z0-9_-]{1,64}$).
func TestConnectorToolNamesAreProviderSafe(t *testing.T) {
	h := &hostToolSet{
		connReg: loadReg(t),
		boundConns: []connectors.BoundConn{
			{ID: "c1", Provider: "google", AccountLabel: "My Work"},
			{ID: "c2", Provider: "google", AccountLabel: "home@x.com"},
		},
	}
	for _, tl := range h.connectorTools() {
		for _, r := range tl.Name {
			ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
			if !ok {
				t.Fatalf("tool name %q contains invalid char %q", tl.Name, r)
			}
		}
		if len(tl.Name) > 64 {
			t.Fatalf("tool name too long: %q", tl.Name)
		}
	}
	// The slugged multi-account tool must still resolve back to its connection.
	if conn, _, ok := h.resolveConnectorTool("gmail_send_email__My_Work"); !ok || conn.ID != "c1" {
		t.Fatalf("slugged tool did not resolve: %+v %v", conn, ok)
	}
}

// TestExecuteDispatchesConnectorTool covers the real seam: execute() default-case →
// resolveConnectorTool → executeConnectorTool → connectors.Execute, with a nil
// httpClient (Execute builds its own) hitting an httptest provider.
func TestExecuteDispatchesConnectorTool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer AT" {
			t.Errorf("bearer not forwarded: %q", r.Header.Get("Authorization"))
		}
		w.Write([]byte(`{"messages":[{"id":"mX"}]}`))
	}))
	defer srv.Close()

	reg := loadReg(t)
	a, _ := reg.Action("google", "gmail_search")
	a.Request.URL = srv.URL + "/messages"
	reg.SetActionsForTest("google", []connectors.Action{a})

	h := &hostToolSet{
		connReg:    reg,
		connStore:  fakeTokenStore{tok: "AT"},
		boundConns: []connectors.BoundConn{{ID: "c1", Provider: "google", AccountLabel: "work"}},
		// httpClient deliberately nil → Execute constructs its own client.
	}
	out := h.execute(context.Background(), llm.ToolCall{Name: "gmail_search", Args: []byte(`{"query":"hi"}`)})
	if !strings.Contains(out, "mX") {
		t.Fatalf("connector dispatch did not return provider data: %q", out)
	}
	// Unknown tool still errors through the default case.
	if got := h.execute(context.Background(), llm.ToolCall{Name: "totally_unknown", Args: []byte(`{}`)}); !strings.Contains(got, "unknown tool") {
		t.Fatalf("unknown tool should error: %q", got)
	}
}
