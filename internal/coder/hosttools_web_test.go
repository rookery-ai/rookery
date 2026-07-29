package coder

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ilijad1/rookery/internal/llm"
)

// newWebToolSet builds a minimal hostToolSet wired for web_fetch tests: exec tools
// enabled, a tiny retry base so transient-retry tests don't sleep for real, and the
// private-address guard disabled — every test in this file serves fixtures from an
// httptest server bound to 127.0.0.1, which the guard would otherwise refuse to dial.
func newWebToolSet() *hostToolSet {
	return &hostToolSet{includeExecTools: true, webRetryBase: time.Millisecond, allowPrivateHosts: true}
}

func webCall(url string) llm.ToolCall {
	b, _ := json.Marshal(map[string]string{"url": url})
	return llm.ToolCall{Name: "web_fetch", Args: b}
}

// TestWebFetchReturnsBody: a 200 JSON/text response is passed through to the model.
func TestWebFetchReturnsBody(t *testing.T) {
	const body = `{"temp":21,"city":"Skopje"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	h := newWebToolSet()
	res := h.execute(context.Background(), webCall(srv.URL))
	if !strings.Contains(res, body) {
		t.Fatalf("web_fetch should return the JSON body; got: %q", res)
	}
	if strings.HasPrefix(res, "error:") {
		t.Fatalf("2xx must not be an error result; got: %q", res)
	}
}

// TestWebFetchStripsHTML: an HTML page is now rendered as MARKDOWN (via
// internal/convert), not the old regex-stripped single line of plain text — so
// this asserts the markdown-specific shape (a "# " heading marker, block
// separation) rather than a substring the old plain-text output would also
// satisfy. <script>/<style> content is dropped as page chrome and entities are
// unescaped by the real HTML parser.
func TestWebFetchStripsHTML(t *testing.T) {
	html := `<html><head><style>.x{color:red}</style>` +
		`<script>var evil="DO_NOT_SHOW";</script></head>` +
		`<body><h1>Weather</h1><p>21&deg; in Skopje</p></body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	}))
	defer srv.Close()

	h := newWebToolSet()
	res := h.execute(context.Background(), webCall(srv.URL))
	if !strings.Contains(res, "# Weather") {
		t.Fatalf("the <h1> should render as a markdown heading (not just bare text, which the old plain-text output would also satisfy); got: %q", res)
	}
	if !strings.Contains(res, "Skopje") {
		t.Fatalf("stripped HTML should keep visible text; got: %q", res)
	}
	if strings.Contains(res, "DO_NOT_SHOW") || strings.Contains(res, "color:red") {
		t.Fatalf("script/style contents must be stripped; got: %q", res)
	}
	if strings.Contains(res, "<h1>") || strings.Contains(res, "<p>") {
		t.Fatalf("tags must be stripped; got: %q", res)
	}
	if !strings.Contains(res, "21° in Skopje") {
		t.Fatalf("HTML entities must be unescaped; got: %q", res)
	}
}

// TestWebFetchHardErrorOn404: a non-retryable 4xx becomes an error: result carrying the status.
func TestWebFetchHardErrorOn404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	h := newWebToolSet()
	res := h.execute(context.Background(), webCall(srv.URL))
	if !strings.HasPrefix(res, "error:") || !strings.Contains(res, "404") {
		t.Fatalf("404 should be a hard error mentioning the status; got: %q", res)
	}
}

// TestWebFetchRetriesTransient: 503 twice then 200 → the transient failures are retried
// INTERNALLY (never surfaced as error:), so the model's loop-guard is not tripped.
func TestWebFetchRetriesTransient(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) <= 2 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("RECOVERED"))
	}))
	defer srv.Close()

	h := newWebToolSet()
	res := h.execute(context.Background(), webCall(srv.URL))
	if strings.HasPrefix(res, "error:") {
		t.Fatalf("a transient 503 that recovers must not surface as error:; got: %q", res)
	}
	if !strings.Contains(res, "RECOVERED") {
		t.Fatalf("web_fetch should return the body after retrying; got: %q", res)
	}
	if atomic.LoadInt32(&n) < 3 {
		t.Fatalf("expected at least 3 attempts (2 x 503 + success), got %d", n)
	}
}

// TestWebFetchTruncates: an over-cap body is truncated so it can't blow the model context.
func TestWebFetchTruncates(t *testing.T) {
	big := strings.Repeat("A", maxToolResult*3)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(big))
	}))
	defer srv.Close()

	h := newWebToolSet()
	res := h.execute(context.Background(), webCall(srv.URL))
	if len(res) > maxToolResult+512 {
		t.Fatalf("web_fetch result should be truncated near the cap; got len %d", len(res))
	}
}

// TestWebFetchBinaryReturnsNote: a binary response (image/pdf) must NOT dump raw bytes into
// the model context — it returns a short note with the content type and size instead.
func TestWebFetchBinaryReturnsNote(t *testing.T) {
	raw := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x01, 0x02, 0x03} // PNG magic + noise
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(raw)
	}))
	defer srv.Close()

	h := newWebToolSet()
	res := h.execute(context.Background(), webCall(srv.URL))
	if strings.HasPrefix(res, "error:") {
		t.Fatalf("a 200 binary response is not an error; got: %q", res)
	}
	if strings.ContainsRune(res, '\x89') || strings.Contains(res, "\x00\x01\x02\x03") {
		t.Fatalf("binary bytes must not be dumped into the result; got: %q", res)
	}
	if !strings.Contains(res, "image/png") {
		t.Fatalf("binary note should name the content type; got: %q", res)
	}
}

// Web tools are read-only and cannot carry secrets, so they are offered in chat
// too. The exec gate exists for tools that run code (run_script, bash) — it
// never applied to fetch/search for the right reason.
func TestWebToolsOfferedInChat(t *testing.T) {
	h := &hostToolSet{includeExecTools: false}
	var names []string
	for _, tool := range h.tools() {
		names = append(names, tool.Name)
	}
	for _, want := range []string{"web_fetch", "web_search"} {
		if !slices.Contains(names, want) {
			t.Errorf("%s must be offered without exec tools, got %v", want, names)
		}
	}
	for _, unwanted := range []string{"run_script", "bash"} {
		if slices.Contains(names, unwanted) {
			t.Errorf("%s must stay exec-gated, got %v", unwanted, names)
		}
	}
}

func TestWebFetchExecutesWithoutExecTools(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("chat can read this"))
	}))
	defer srv.Close()

	h := &hostToolSet{includeExecTools: false, webRetryBase: time.Millisecond, allowPrivateHosts: true}
	out := h.execute(context.Background(), llm.ToolCall{Name: "web_fetch",
		Args: json.RawMessage(`{"url":"` + srv.URL + `"}`)})
	if strings.HasPrefix(out, "error:") {
		t.Fatalf("web_fetch should work in chat, got %q", out)
	}
	if !strings.Contains(out, "chat can read this") {
		t.Errorf("unexpected body: %q", out)
	}
}

func TestExecToolsStillGated(t *testing.T) {
	h := &hostToolSet{includeExecTools: false}
	for _, name := range []string{"run_script", "bash"} {
		out := h.execute(context.Background(), llm.ToolCall{Name: name, Args: json.RawMessage(`{}`)})
		if !strings.Contains(out, "not available") {
			t.Errorf("%s should be refused in chat, got %q", name, out)
		}
	}
}

func findTool(tools []llm.Tool, name string) (llm.Tool, bool) {
	for _, tl := range tools {
		if tl.Name == name {
			return tl, true
		}
	}
	return llm.Tool{}, false
}

// TestWebFetchSchemaIsSimple: web_fetch advertises a minimal, flat schema (url + method) so
// weak/OpenAI-compatible models (Mistral) reliably accept and call it. In particular it must
// NOT use additionalProperties (a free-form map) — the one construct such models handle
// unevenly, which can make the whole tool silently unavailable.
func TestWebFetchSchemaIsSimple(t *testing.T) {
	h := &hostToolSet{includeExecTools: true}
	wf, ok := findTool(h.tools(), "web_fetch")
	if !ok {
		t.Fatal("web_fetch not offered")
	}
	schema := string(wf.Parameters)
	if strings.Contains(schema, "additionalProperties") {
		t.Errorf("web_fetch schema must avoid additionalProperties for weak-model interop; got: %s", schema)
	}
	if strings.Contains(schema, "headers") {
		t.Errorf("web_fetch schema must not advertise a free-form headers map; got: %s", schema)
	}
	if !strings.Contains(schema, `"url"`) {
		t.Errorf("web_fetch must require a url; got: %s", schema)
	}
}

// TestExecToolDescriptionsHaveExamples: each exec tool's description carries a concrete
// example — weak models pattern-match on examples when choosing a tool.
func TestExecToolDescriptionsHaveExamples(t *testing.T) {
	h := &hostToolSet{includeExecTools: true}
	for _, name := range []string{"web_fetch", "web_search", "run_script", "bash"} {
		tl, ok := findTool(h.tools(), name)
		if !ok {
			t.Fatalf("%s not offered", name)
		}
		if !strings.Contains(tl.Description, "e.g.") {
			t.Errorf("%s description should carry a concrete example (e.g. ...) for weak-model tool selection", name)
		}
	}
}

// ── web_search ────────────────────────────────────────────────────────────────

func webSearchCall(query string) llm.ToolCall {
	b, _ := json.Marshal(map[string]string{"query": query})
	return llm.ToolCall{Name: "web_search", Args: b}
}

// ddgHTML serves a minimal DuckDuckGo-shaped HTML results page. The real DDG html
// endpoint wraps each hit in a .result block with a class="result__a" link (whose
// href is a //duckduckgo.com/l/?uddg=<encoded real URL> redirect) and a
// class="result__snippet" anchor with the snippet text.
func ddgHTML(results [][2]string, snippets []string) string {
	var sb strings.Builder
	sb.WriteString(`<html><body>`)
	for i, r := range results {
		title, rawURL := r[0], r[1]
		sb.WriteString(`<div class="result"><a class="result__a" href="//duckduckgo.com/l/?uddg=`)
		sb.WriteString(url.QueryEscape(rawURL))
		sb.WriteString(`&amp;rut=abc">` + title + `</a>`)
		if i < len(snippets) {
			sb.WriteString(`<a class="result__snippet" href="x">` + snippets[i] + `</a>`)
		}
		sb.WriteString(`</div>`)
	}
	sb.WriteString(`</body></html>`)
	return sb.String()
}

// TestWebSearchReturnsResults: titles and snippets have HTML stripped, and the real
// result URL is recovered from the DDG redirect's uddg param.
func TestWebSearchReturnsResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(ddgHTML(
			[][2]string{
				{"Weather <b>Skopje</b>", "https://example.com/weather"},
				{"Top News", "https://news.example.org"},
			},
			[]string{"Sunny, <b>21°C</b> in Skopje today.", "Headlines from the region."},
		)))
	}))
	defer srv.Close()

	h := &hostToolSet{includeExecTools: true, webRetryBase: time.Millisecond, ddgBaseURL: srv.URL, allowPrivateHosts: true}
	res := h.execute(context.Background(), webSearchCall("weather skopje"))
	if strings.HasPrefix(res, "error:") {
		t.Fatalf("web_search should succeed; got %q", res)
	}
	if !strings.Contains(res, "Weather Skopje") {
		t.Fatalf("title should be HTML-stripped and present; got %q", res)
	}
	if !strings.Contains(res, "https://example.com/weather") {
		t.Fatalf("real URL should be decoded from the uddg redirect param; got %q", res)
	}
	if !strings.Contains(res, "Sunny, 21°C in Skopje today.") {
		t.Fatalf("snippet should be HTML-stripped (no <b> tags); got %q", res)
	}
	if !strings.Contains(res, "news.example.org") {
		t.Fatalf("second result URL should be present; got %q", res)
	}
}

// TestWebSearchRetriesTransient: 503 twice then 200 → the transient failures are
// retried INTERNALLY (never surfaced as error:), so the loop-guard is not tripped.
func TestWebSearchRetriesTransient(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) <= 2 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(ddgHTML([][2]string{{"Result", "https://x.example"}}, []string{"snip"})))
	}))
	defer srv.Close()

	h := &hostToolSet{includeExecTools: true, webRetryBase: time.Millisecond, ddgBaseURL: srv.URL, allowPrivateHosts: true}
	res := h.execute(context.Background(), webSearchCall("anything"))
	if strings.HasPrefix(res, "error:") {
		t.Fatalf("a transient 503 that recovers must not surface as error:; got %q", res)
	}
	if !strings.Contains(res, "x.example") {
		t.Fatalf("web_search should return results after retrying; got %q", res)
	}
	if atomic.LoadInt32(&n) < 3 {
		t.Fatalf("expected at least 3 attempts (2 x 503 + success), got %d", n)
	}
}

// TestWebSearchNoResultsNonError: a 200 page with no parseable result blocks is a
// valid empty result ("no search results"), NOT an error — so the model can fall
// back to web_fetch without tripping the oscillation guard.
func TestWebSearchNoResultsNonError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body>no results here</body></html>`))
	}))
	defer srv.Close()

	h := &hostToolSet{includeExecTools: true, webRetryBase: time.Millisecond, ddgBaseURL: srv.URL, allowPrivateHosts: true}
	res := h.execute(context.Background(), webSearchCall("zzz"))
	if strings.HasPrefix(res, "error:") {
		t.Fatalf("no results must not surface as error:; got %q", res)
	}
	if !strings.Contains(res, "no search results") {
		t.Fatalf("expected a no-search-results notice; got %q", res)
	}
}

// TestWebSearchBlocksPrivateAddressByDefault pins Fix 6: web_search must use
// the SAME guarded client web_fetch/save_to_kb do by default — otherwise it is
// a second, unguarded way to reach the loopback connector bridge or other
// private address space that the SSRF guard exists to close off everywhere
// else. Search()'s provider cascade swallows a single provider's failure into
// a non-error "no results" (by design — see websearch.go), so the observable
// property here is that the guarded server is never actually reached, not
// that web_search surfaces an "error:" result.
func TestWebSearchBlocksPrivateAddressByDefault(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(ddgHTML([][2]string{{"leak", "https://x.example"}}, []string{"snip"})))
	}))
	defer srv.Close()

	h := &hostToolSet{includeExecTools: true, webRetryBase: time.Millisecond, ddgBaseURL: srv.URL} // guard ON (allowPrivateHosts unset)
	h.execute(context.Background(), webSearchCall("anything"))
	if atomic.LoadInt32(&hits) != 0 {
		t.Fatalf("web_search must never reach a loopback address by default, but the server was hit %d time(s)", hits)
	}
}

// TestWebSearchRequiresQuery: an empty query is a hard (non-retryable) error.
func TestWebSearchRequiresQuery(t *testing.T) {
	h := &hostToolSet{includeExecTools: true, webRetryBase: time.Millisecond}
	res := h.execute(context.Background(), webSearchCall(""))
	if !strings.HasPrefix(res, "error:") || !strings.Contains(res, "query") {
		t.Fatalf("empty query must be a hard error mentioning 'query'; got %q", res)
	}
}

// TestWebSearchSchemaIsSimple: web_search takes only a query — no headers, no
// secrets, no additionalProperties (same weak-model interop rule as web_fetch).
func TestWebSearchSchemaIsSimple(t *testing.T) {
	h := &hostToolSet{includeExecTools: true}
	ws, ok := findTool(h.tools(), "web_search")
	if !ok {
		t.Fatal("web_search not offered")
	}
	schema := string(ws.Parameters)
	if strings.Contains(schema, "additionalProperties") {
		t.Errorf("web_search schema must avoid additionalProperties; got %s", schema)
	}
	if strings.Contains(schema, "headers") || strings.Contains(schema, "secret") {
		t.Errorf("web_search schema must not advertise headers/secrets (query-only); got %s", schema)
	}
	if !strings.Contains(schema, `"query"`) {
		t.Errorf("web_search must require a query; got %s", schema)
	}
}

// TestWebSearchOfferedWhenExec: web_search IS offered alongside the other exec
// tools when includeExecTools is on.
func TestWebSearchOfferedWhenExec(t *testing.T) {
	h := &hostToolSet{includeExecTools: true}
	if _, ok := findTool(h.tools(), "web_search"); !ok {
		t.Fatal("web_search must be offered when exec tools are on")
	}
}

func TestWebFetchRendersHTMLAsMarkdown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><nav>Menu</nav><main><h1>Title</h1>
			<p>Body text with <a href="https://example.com/x">a link</a>.</p></main>
			<footer>Legal</footer></body></html>`))
	}))
	defer srv.Close()

	h := &hostToolSet{includeExecTools: true, webRetryBase: time.Millisecond, allowPrivateHosts: true}
	out, err := h.webFetch(context.Background(), srv.URL, "GET", nil, "")
	if err != nil {
		t.Fatalf("webFetch: %v", err)
	}
	if !strings.Contains(out, "# Title") {
		t.Errorf("expected a markdown heading, got:\n%s", out)
	}
	if !strings.Contains(out, "[a link](https://example.com/x)") {
		t.Errorf("expected the link preserved as markdown, got:\n%s", out)
	}
	if strings.Contains(out, "Menu") || strings.Contains(out, "Legal") {
		t.Errorf("page chrome should be dropped, got:\n%s", out)
	}
}

func TestWebFetchMemoizesWithinToolset(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte("hello"))
	}))
	defer srv.Close()

	h := &hostToolSet{includeExecTools: true, webRetryBase: time.Millisecond, allowPrivateHosts: true}
	for i := 0; i < 3; i++ {
		if _, err := h.webFetch(context.Background(), srv.URL, "GET", nil, ""); err != nil {
			t.Fatalf("webFetch %d: %v", i, err)
		}
	}
	if calls != 1 {
		t.Errorf("identical GETs in one toolset should hit the network once, saw %d", calls)
	}
}

func TestWebFetchBlocksPrivateAddressByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("bridge secrets"))
	}))
	defer srv.Close()

	h := &hostToolSet{includeExecTools: true, webRetryBase: time.Millisecond} // guard ON
	if _, err := h.webFetch(context.Background(), srv.URL, "GET", nil, ""); err == nil {
		t.Fatal("web_fetch must not reach a loopback address")
	}
}

// A JSON API response is web_fetch's most common target and its own tool-
// description example. It must survive Phase 1, where no JSON converter exists.
func TestWebFetchJSONPassesThrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"current":{"temperature_2m":24.1}}`))
	}))
	defer srv.Close()

	h := &hostToolSet{includeExecTools: true, webRetryBase: time.Millisecond, allowPrivateHosts: true}
	out, err := h.webFetch(context.Background(), srv.URL, "GET", nil, "")
	if err != nil {
		t.Fatalf("webFetch: %v", err)
	}
	if !strings.Contains(out, `"temperature_2m":24.1`) {
		t.Errorf("json body must pass through verbatim, got:\n%s", out)
	}
}

func TestWebFetchPDFIsConverted(t *testing.T) {
	// Phase 1 has no PDF converter yet, so a PDF must fail LOUDLY with a clear
	// message rather than returning the old "[not text]" dead end silently.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte("%PDF-1.7\nnot a real pdf"))
	}))
	defer srv.Close()

	h := &hostToolSet{includeExecTools: true, webRetryBase: time.Millisecond, allowPrivateHosts: true}
	out, err := h.webFetch(context.Background(), srv.URL, "GET", nil, "")
	if err == nil && !strings.Contains(out, "pdf") {
		t.Errorf("a pdf response should mention the format either way, got err=%v out=%q", err, out)
	}
}
