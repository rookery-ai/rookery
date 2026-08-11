package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The tests in this file drive a REAL in-process MCP server over httptest rather than
// a mock. The SDK ships a server implementation, so there is no reason to assert
// against a hand-written fake of the wire format — which would only prove our fake
// matches our client.

type echoIn struct {
	Text string `json:"text"`
}

type echoOut struct {
	Echoed string `json:"echoed"`
}

// testServer builds an MCP server with a representative spread of tools: one
// structured, one plain-text, one image-returning, one that reports a tool execution
// error, and one whose name is spec-legal but hostile to a provider's charset.
func testServer(t *testing.T) *sdk.Server {
	t.Helper()
	s := sdk.NewServer(&sdk.Implementation{Name: "test-server", Version: "0.1"}, nil)

	sdk.AddTool(s, &sdk.Tool{
		Name:        "echo",
		Description: "Echo the input back",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
	}, func(_ context.Context, _ *sdk.CallToolRequest, in echoIn) (*sdk.CallToolResult, echoOut, error) {
		return nil, echoOut{Echoed: in.Text}, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "admin.tools.list",
		Description: "A spec-legal name a provider would reject verbatim",
	}, func(_ context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "listed"}}}, nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "screenshot",
		Description: "Returns an image",
	}, func(_ context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{
			&sdk.ImageContent{Data: []byte(strings.Repeat("A", 40000)), MIMEType: "image/png"},
			&sdk.TextContent{Text: "captured"},
		}}, nil, nil
	})

	sdk.AddTool(s, &sdk.Tool{
		Name:        "explode",
		Description: "Reports a tool execution error",
	}, func(_ context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{
			IsError: true,
			Content: []sdk.Content{&sdk.TextContent{Text: "date must be in the future"}},
		}, nil, nil
	})

	return s
}

func serve(t *testing.T, s *sdk.Server) string {
	t.Helper()
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return s }, nil)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

func boundFor(url string) BoundServer {
	return BoundServer{ID: "srv1", Name: "Test", Slug: "test", URL: url}
}

func TestListToolsDiscoversTheServersCatalog(t *testing.T) {
	url := serve(t, testServer(t))
	c := NewClient(nil)
	defer c.Close()

	cat, err := c.ListTools(context.Background(), boundFor(url))
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	byName := map[string]DiscoveredTool{}
	for _, tl := range cat.Tools {
		byName[tl.Name] = tl
	}
	for _, want := range []string{"echo", "admin.tools.list", "screenshot", "explode"} {
		if _, ok := byName[want]; !ok {
			t.Errorf("catalog missing %q; got %v", want, cat.Tools)
		}
	}
	// The annotation is carried through as a HINT that seeds the owner's column —
	// never as the authority Execute reads.
	if !byName["echo"].ReadOnlyHint {
		t.Error("echo lost its readOnlyHint")
	}
	if byName["explode"].ReadOnlyHint {
		t.Error("explode gained a readOnlyHint it never declared")
	}
	// Every discovered tool must carry a usable object schema, or a provider would
	// reject the whole tool list.
	for _, tl := range cat.Tools {
		if len(tl.InputSchema) == 0 || !strings.HasPrefix(strings.TrimSpace(string(tl.InputSchema)), "{") {
			t.Errorf("%s has an unusable input schema %q", tl.Name, tl.InputSchema)
		}
	}
}

func TestCallToolPrefersStructuredContent(t *testing.T) {
	url := serve(t, testServer(t))
	c := NewClient(nil)
	defer c.Close()

	res, err := c.CallTool(context.Background(), boundFor(url), "echo", map[string]any{"text": "hi"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	var got echoOut
	if err := json.Unmarshal(res.Data, &got); err != nil {
		t.Fatalf("structured content was not returned as JSON: %s (%v)", res.Data, err)
	}
	if got.Echoed != "hi" {
		t.Fatalf("echoed = %q", got.Echoed)
	}
}

func TestCallToolReplacesImageDataWithAPlaceholder(t *testing.T) {
	// A screenshot's base64 runs to hundreds of kilobytes. Passing it through would
	// consume the whole 8 KiB result budget and teach the model nothing it can act
	// on, so the bytes are replaced by a placeholder that still says an image came
	// back.
	url := serve(t, testServer(t))
	c := NewClient(nil)
	defer c.Close()

	res, err := c.CallTool(context.Background(), boundFor(url), "screenshot", map[string]any{})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	var text string
	if err := json.Unmarshal(res.Data, &text); err != nil {
		t.Fatalf("expected a JSON string, got %s", res.Data)
	}
	if !strings.Contains(text, "[image omitted") {
		t.Errorf("image was not replaced by a placeholder: %q", text)
	}
	if !strings.Contains(text, "captured") {
		t.Errorf("the accompanying text block was dropped: %q", text)
	}
	if len(res.Data) > 4096 {
		t.Errorf("result is %d bytes — the image data leaked through", len(res.Data))
	}
}

func TestToolExecutionErrorIsFlaggedNotReturnedAsAnError(t *testing.T) {
	// The MCP spec says clients should hand execution errors to the model so it can
	// self-correct. Returning a Go error here would make the caller prefix it with
	// `error:`, which the API engine's oscillation guard counts as a failing call —
	// killing exactly the retry-with-fixed-args the server is inviting.
	url := serve(t, testServer(t))
	c := NewClient(nil)
	defer c.Close()

	res, err := c.CallTool(context.Background(), boundFor(url), "explode", map[string]any{})
	if err != nil {
		t.Fatalf("a tool execution error must not surface as a Go error: %v", err)
	}
	if !res.IsError {
		t.Fatal("IsError was not set, so the caller cannot tell the model it failed")
	}
	if !strings.Contains(string(res.Data), "future") {
		t.Errorf("the server's actionable message was lost: %s", res.Data)
	}
}

func TestUnknownToolIsAProtocolError(t *testing.T) {
	url := serve(t, testServer(t))
	c := NewClient(nil)
	defer c.Close()

	if _, err := c.CallTool(context.Background(), boundFor(url), "nope", map[string]any{}); err == nil {
		t.Fatal("calling an unknown tool should be an error, not a flagged result")
	}
}

func TestAuthCredentialIsSentOnEveryRequest(t *testing.T) {
	// MCP is stateless at the HTTP layer, so the credential rides every request
	// rather than only the handshake.
	var seen []string
	inner := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return testServer(t) }, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		inner.ServeHTTP(w, r)
	}))
	defer srv.Close()

	c := NewClient(nil)
	defer c.Close()
	b := boundFor(srv.URL)
	b.AuthKind = "bearer"
	b.Token = "s3cret"

	if _, err := c.CallTool(context.Background(), b, "echo", map[string]any{"text": "x"}); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(seen) == 0 {
		t.Fatal("no requests observed")
	}
	for i, h := range seen {
		if h != "Bearer s3cret" {
			t.Errorf("request %d carried Authorization %q", i, h)
		}
	}
}

func TestCustomHeaderAuthUsesTheNamedHeader(t *testing.T) {
	var seen string
	inner := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return testServer(t) }, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v := r.Header.Get("X-Api-Key"); v != "" {
			seen = v
		}
		inner.ServeHTTP(w, r)
	}))
	defer srv.Close()

	c := NewClient(nil)
	defer c.Close()
	b := boundFor(srv.URL)
	b.AuthKind = "header"
	b.HeaderName = "X-Api-Key"
	b.Token = "abc123"

	if _, err := c.CallTool(context.Background(), b, "echo", map[string]any{"text": "x"}); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if seen != "abc123" {
		t.Fatalf("X-Api-Key = %q", seen)
	}
}

func TestUnreachableServerIsNotTreatedAsARejectedCredential(t *testing.T) {
	// This is the DBTokenStore lesson applied on day one. Only a definitive 401 may
	// flip a server to NEEDS_AUTH; a dead host, a 5xx or a DNS failure must stay
	// UNREACHABLE, which does not alert and does not remove the server from the
	// retry path. Failing open here costs one retry; failing closed costs the owner
	// a working server until they reconnect it by hand.
	c := NewClient(nil)
	defer c.Close()

	b := boundFor("http://127.0.0.1:1") // nothing listens on port 1
	_, err := c.ListTools(context.Background(), b)
	if err == nil {
		t.Fatal("expected an error")
	}
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("error is %T, want *mcp.Error", err)
	}
	if e.Kind != KindUnreachable {
		t.Fatalf("Kind = %v, want KindUnreachable (a dead host is not a rejection)", e.Kind)
	}
}

func TestUnauthorizedServerIsClassifiedAsAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := NewClient(nil)
	defer c.Close()
	_, err := c.ListTools(context.Background(), boundFor(srv.URL))
	if err == nil {
		t.Fatal("expected an error")
	}
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("error is %T, want *mcp.Error", err)
	}
	if e.Kind != KindAuth {
		t.Fatalf("Kind = %v, want KindAuth for a 401", e.Kind)
	}
}

// TestExecuteReachesPrivateAddresses pins that this package does NOT use the
// internal/nethttp private-address dial guard.
//
// This is deliberate and mirrors connectors.TestExecuteReachesPrivateAddresses. The
// entire self-hosted tier — an MCP server in front of Home Assistant, Immich or
// Paperless — lives at exactly the RFC1918 and Tailscale addresses that guard blocks
// at dial time. The guard's threat model is untrusted content steering a fetch; an
// MCP server's host is a value the single owner typed into their own install.
//
// If this test fails because someone wrapped the client in nethttp.GuardedClient,
// the fix is NOT to change the test: every self-hosted MCP server would stop working.
// Revisit only if Rookery becomes multi-tenant — this is where that conversation
// should start.
func TestExecuteReachesPrivateAddresses(t *testing.T) {
	url := serve(t, testServer(t)) // httptest binds 127.0.0.1 — a blocked address
	if !strings.Contains(url, "127.0.0.1") {
		t.Skipf("httptest did not bind loopback (%s)", url)
	}
	c := NewClient(nil)
	defer c.Close()

	if _, err := c.ListTools(context.Background(), boundFor(url)); err != nil {
		t.Fatalf("a loopback MCP server must be reachable, got %v", err)
	}
}
