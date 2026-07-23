package websearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// ddgPage renders a minimal DuckDuckGo html result page.
func ddgPage(title, target, snippet string) string {
	return `<html><body><div class="result">` +
		`<a class="result__a" href="//duckduckgo.com/l/?uddg=` + target + `">` + title + `</a>` +
		`<a class="result__snippet">` + snippet + `</a>` +
		`</div></body></html>`
}

func testClient(providers ...Provider) *Client {
	return &Client{HTTP: &http.Client{Timeout: 5 * time.Second}, RetryBase: time.Millisecond, Providers: providers}
}

func TestSearchFirstProviderWins(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(ddgPage("Weather Skopje", "https%3A%2F%2Fexample.com%2Fwx", "Sunny, 24C")))
	}))
	defer srv.Close()

	c := testClient(&ddgProvider{name: "ddg-html", base: srv.URL})
	got, err := c.Search(context.Background(), "weather skopje")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d results, want 1", len(got))
	}
	if got[0].URL != "https://example.com/wx" {
		t.Errorf("URL = %q, want the decoded redirect target", got[0].URL)
	}
	if got[0].Title != "Weather Skopje" {
		t.Errorf("Title = %q", got[0].Title)
	}
}

func TestSearchFallsThroughOnZeroResults(t *testing.T) {
	// First engine returns 200 with a JS challenge page (no result blocks) —
	// the exact real-world failure that made single-engine search unreliable.
	challenge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body><noscript>Please enable JavaScript</noscript></body></html>`))
	}))
	defer challenge.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(ddgPage("Second engine", "https%3A%2F%2Fexample.org%2Fb", "from the fallback")))
	}))
	defer good.Close()

	c := testClient(
		&ddgProvider{name: "ddg-html", base: challenge.URL},
		&ddgProvider{name: "ddg-lite", base: good.URL},
	)
	got, err := c.Search(context.Background(), "anything")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Second engine" {
		t.Fatalf("expected fallback engine result, got %+v", got)
	}
}

func TestSearchFallsThroughOnHardFailure(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer dead.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(ddgPage("Alive", "https%3A%2F%2Fexample.org%2Fc", "ok")))
	}))
	defer good.Close()

	c := testClient(&ddgProvider{name: "a", base: dead.URL}, &ddgProvider{name: "b", base: good.URL})
	got, err := c.Search(context.Background(), "q")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Alive" {
		t.Fatalf("expected second engine, got %+v", got)
	}
}

func TestSearchRetriesTransientWithinProvider(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(ddgPage("Recovered", "https%3A%2F%2Fexample.org%2Fd", "ok")))
	}))
	defer srv.Close()

	c := testClient(&ddgProvider{name: "a", base: srv.URL})
	got, err := c.Search(context.Background(), "q")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Recovered" {
		t.Fatalf("429 should be retried inside the provider, got %+v", got)
	}
	if calls < 2 {
		t.Errorf("expected a retry, saw %d calls", calls)
	}
}

func TestSearchAllEnginesFailReturnsEmptyNotError(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer dead.Close()

	c := testClient(&ddgProvider{name: "a", base: dead.URL}, &ddgProvider{name: "b", base: dead.URL})
	got, err := c.Search(context.Background(), "q")
	if err != nil {
		t.Fatalf("all-engines-fail must NOT be an error (it would trip the tool oscillation guard): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d results, want 0", len(got))
	}
}

func TestSearchDedupesByURL(t *testing.T) {
	page := `<html><body>` +
		`<a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fx">One</a><a class="result__snippet">a</a>` +
		`<a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fx%2F">Dup</a><a class="result__snippet">b</a>` +
		`</body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(page))
	}))
	defer srv.Close()

	c := testClient(&ddgProvider{name: "a", base: srv.URL})
	got, _ := c.Search(context.Background(), "q")
	if len(got) != 1 {
		t.Errorf("trailing-slash duplicate should collapse, got %d: %+v", len(got), got)
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	c := testClient(&ddgProvider{name: "a", base: "http://unused"})
	if _, err := c.Search(context.Background(), "  "); err == nil {
		t.Error("empty query must be an error (it is a caller bug, not a transient condition)")
	}
}

// TestSearchRetryBudgetIsPerProvider pins one of this package's load-bearing
// properties: a provider's transient retries consume only that provider's
// own attempt budget, never bleeding into the next provider's. Two
// providers that both always return 429 must each be hit exactly
// maxAttempts times — never fewer (retries dropped) and never more (budget
// leaking across providers, or the cascade retrying the whole provider list).
func TestSearchRetryBudgetIsPerProvider(t *testing.T) {
	var calls1, calls2 int64

	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls1, 1)
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	defer srv1.Close()
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls2, 1)
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	defer srv2.Close()

	c := testClient(
		&ddgProvider{name: "a", base: srv1.URL},
		&ddgProvider{name: "b", base: srv2.URL},
	)
	got, err := c.Search(context.Background(), "q")
	if err != nil {
		t.Fatalf("all-engines-fail must NOT be an error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d results, want 0", len(got))
	}

	if n := atomic.LoadInt64(&calls1); n != maxAttempts {
		t.Errorf("provider a: got %d requests, want exactly maxAttempts (%d)", n, maxAttempts)
	}
	if n := atomic.LoadInt64(&calls2); n != maxAttempts {
		t.Errorf("provider b: got %d requests, want exactly maxAttempts (%d)", n, maxAttempts)
	}
	total := atomic.LoadInt64(&calls1) + atomic.LoadInt64(&calls2)
	if want := int64(2 * maxAttempts); total != want {
		t.Errorf("total requests = %d, want %d (2 providers x maxAttempts)", total, want)
	}
}

// TestDecodeDDGRedirect pins the redirect-vs-direct-link distinction from
// Finding 1: a wrapper whose uddg can't be recovered must be dropped ("")
// rather than falling back to the wrapper URL itself.
func TestDecodeDDGRedirect(t *testing.T) {
	cases := []struct {
		name string
		href string
		want string
	}{
		{
			name: "valid uddg redirect",
			href: "//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fx",
			want: "https://example.com/x",
		},
		{
			name: "malformed uddg on a redirect wrapper is dropped, not returned as the wrapper",
			href: "//duckduckgo.com/l/?uddg=%zz",
			want: "",
		},
		{
			name: "direct non-wrapper link is returned as-is",
			href: "https://example.com/direct-page",
			want: "https://example.com/direct-page",
		},
		{
			name: "protocol-relative wrapper href",
			href: "//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.org%2Fy",
			want: "https://example.org/y",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decodeDDGRedirect(tc.href)
			if got != tc.want {
				t.Errorf("decodeDDGRedirect(%q) = %q, want %q", tc.href, got, tc.want)
			}
		})
	}
}
