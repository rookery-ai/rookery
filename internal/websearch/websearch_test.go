package websearch

import (
	"context"
	"net/http"
	"net/http/httptest"
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
