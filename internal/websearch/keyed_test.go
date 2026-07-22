package websearch

import (
	"context"
	"net/http"
	"net/http/httptest"
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

func TestKeyedProviderNilWithoutKey(t *testing.T) {
	if p := KeyedProvider("brave", "", ""); p != nil {
		t.Error("KeyedProvider must return nil when no key is configured")
	}
	if p := KeyedProvider("unknown-engine", "k", ""); p != nil {
		t.Error("KeyedProvider must return nil for an unknown engine")
	}
}

func TestKeyedProviderTransientOn429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	p := KeyedProvider("brave", "k", srv.URL)
	_, err := p.Search(context.Background(), &http.Client{Timeout: 5 * time.Second}, "q")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !isTransient(err) {
		t.Errorf("429 must be transient so the provider is retried, got %v", err)
	}
}
