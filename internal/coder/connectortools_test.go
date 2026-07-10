package coder

import (
	"strings"
	"testing"

	"github.com/ilijad1/simple-agents/internal/connectors"
)

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
