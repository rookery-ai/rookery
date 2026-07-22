package websearch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBraveProvider(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-Subscription-Token")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"web":{"results":[
			{"title":"Brave hit","url":"https://example.com/b","description":"desc here"}
		]}}`))
	}))
	defer srv.Close()

	p := KeyedProvider("brave", "secret-key", srv.URL)
	if p == nil {
		t.Fatal("KeyedProvider returned nil for a non-empty key")
	}
	got, err := p.Search(context.Background(), &http.Client{Timeout: 5 * time.Second}, "q")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotKey != "secret-key" {
		t.Errorf("api key header = %q, want the configured key", gotKey)
	}
	if len(got) != 1 || got[0].URL != "https://example.com/b" || got[0].Snippet != "desc here" {
		t.Fatalf("unexpected results: %+v", got)
	}
}

func TestTavilyProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"results":[{"title":"Tavily hit","url":"https://example.com/t","content":"body"}]}`))
	}))
	defer srv.Close()

	p := KeyedProvider("tavily", "k", srv.URL)
	got, err := p.Search(context.Background(), &http.Client{Timeout: 5 * time.Second}, "q")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Tavily hit" {
		t.Fatalf("unexpected results: %+v", got)
	}
}

func TestTavilyProviderAuthTransport(t *testing.T) {
	var gotAuth string
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"results":[{"title":"Tavily hit","url":"https://example.com/t","content":"body"}]}`))
	}))
	defer srv.Close()

	p := KeyedProvider("tavily", "secret-key", srv.URL)
	if p == nil {
		t.Fatal("KeyedProvider returned nil for a non-empty key")
	}
	if _, err := p.Search(context.Background(), &http.Client{Timeout: 5 * time.Second}, "q"); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotAuth != "Bearer secret-key" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "Bearer secret-key")
	}
	if strings.Contains(gotBody, "api_key") {
		t.Errorf("request body must not contain an api_key field, got %q", gotBody)
	}
}

func TestTavilyKeyDoesNotLeakIntoErrorMessage(t *testing.T) {
	const key = "super-secret-key-12345"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate a provider that echoes the request (headers/body) back in
		// a validation error — the exact reproduction from the finding.
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"error":"bad request","authorization":%q,"max_results":6,"query":"q"}`, r.Header.Get("Authorization"))
	}))
	defer srv.Close()

	p := KeyedProvider("tavily", key, srv.URL)
	_, err := p.Search(context.Background(), &http.Client{Timeout: 5 * time.Second}, "q")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), key) {
		t.Fatalf("error must not contain the raw API key, got %v", err)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("error must contain a redaction marker, got %v", err)
	}
}

func TestKeyedProviderNilWithoutKey(t *testing.T) {
	if p := KeyedProvider("brave", "", ""); p != nil {
		t.Error("KeyedProvider must return nil when no key is configured")
	}
	if p := KeyedProvider("unknown-engine", "k", ""); p != nil {
		t.Error("KeyedProvider must return nil for an unknown engine")
	}
}

func TestKeyedProviderTransientOn429(t *testing.T) {
	for _, engine := range []string{"brave", "tavily"} {
		t.Run(engine, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "rate limited", http.StatusTooManyRequests)
			}))
			defer srv.Close()

			p := KeyedProvider(engine, "k", srv.URL)
			_, err := p.Search(context.Background(), &http.Client{Timeout: 5 * time.Second}, "q")
			if err == nil {
				t.Fatal("expected an error")
			}
			if !isTransient(err) {
				t.Errorf("429 must be transient so the provider is retried, got %v", err)
			}
		})
	}
}
