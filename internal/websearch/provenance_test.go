package websearch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rookery-ai/rookery/internal/nethttp"
)

// stubProvider is a Provider whose behaviour each test dictates outright, so
// cascade bookkeeping can be exercised without any HTTP at all.
type stubProvider struct {
	name    string
	results []Result
	err     error
	calls   int
}

func (p *stubProvider) Name() string { return p.name }

func (p *stubProvider) Search(context.Context, *http.Client, string) ([]Result, error) {
	p.calls++
	return p.results, p.err
}

func TestSearchReportsTheProviderThatServed(t *testing.T) {
	empty := &stubProvider{name: "ddg-html"}
	winner := &stubProvider{name: "brave", results: []Result{{Title: "t", URL: "https://example.com"}}}

	out, err := (&Client{Providers: []Provider{empty, winner}}).Search(context.Background(), "q")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// The whole point of Outcome: the engine that answered is no longer
	// discarded at the moment it is known.
	if out.Provider != "brave" {
		t.Fatalf("Provider = %q, want brave", out.Provider)
	}
	if len(out.Results) != 1 {
		t.Fatalf("Results = %d, want 1", len(out.Results))
	}
	if len(out.Tried) != 2 || out.Tried[0] != "ddg-html" || out.Tried[1] != "brave" {
		t.Fatalf("Tried = %v, want [ddg-html brave]", out.Tried)
	}
}

func TestSearchExhaustedIsNotAnErrorButNamesWhatItTried(t *testing.T) {
	a := &stubProvider{name: "brave", err: errors.New("nope")}
	b := &stubProvider{name: "mojeek"}

	out, err := (&Client{Providers: []Provider{a, b}}).Search(context.Background(), "q")
	// Exhaustion must stay a non-error: the coder's oscillation guard treats any
	// error result as a failing call worth blocking, which is wrong for a query
	// that simply matched nothing.
	if err != nil {
		t.Fatalf("exhausting providers must not error, got %v", err)
	}
	if out.Provider != "" {
		t.Fatalf("Provider = %q, want empty", out.Provider)
	}
	if len(out.Tried) != 2 {
		t.Fatalf("Tried = %v, want both engines recorded", out.Tried)
	}
}

func TestBlockedAddressIsNotRetried(t *testing.T) {
	blocked := &stubProvider{
		name: "brave",
		err:  Transient(errors.New("dial: " + nethttp.ErrBlockedAddr.Error())),
	}
	// Wrap it properly so errors.Is finds the sentinel.
	blocked.err = Transient(wrap(nethttp.ErrBlockedAddr))

	out, err := (&Client{Providers: []Provider{blocked}, RetryBase: time.Millisecond}).
		Search(context.Background(), "q")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if out.Provider != "" {
		t.Fatalf("a blocked provider must not be reported as serving")
	}
	// A blocked host resolves to the same blocked address every time, so
	// retrying it only triples the latency before the cascade moves on.
	if blocked.calls != 1 {
		t.Fatalf("blocked provider called %d times, want 1 (no retry)", blocked.calls)
	}
}

func wrap(err error) error { return errWrap{err} }

type errWrap struct{ err error }

func (e errWrap) Error() string { return "dial tcp: " + e.err.Error() }
func (e errWrap) Unwrap() error { return e.err }

func TestLabelsCollapseTheTwoDuckDuckGoEntries(t *testing.T) {
	got := Labels([]string{"brave", "ddg-html", "ddg-lite", "mojeek"})
	want := []string{"Brave Search", "DuckDuckGo", "Mojeek"}
	if len(got) != len(want) {
		t.Fatalf("Labels = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Labels = %v, want %v", got, want)
		}
	}
	// An unknown engine passes through rather than vanishing.
	if l := Label("some-new-engine"); l != "some-new-engine" {
		t.Fatalf("Label passthrough = %q", l)
	}
}

func TestVerifyClassifiesTheProviderResponse(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   error // nil, ErrInvalidKey, or errSentinelAny for "some error"
		anyErr bool
	}{
		{name: "accepted", status: 200, body: `{"web":{"results":[{"title":"t","url":"u"}]}}`},
		// A 200 with zero results still proves the credential works — Verify
		// tests the key, not the query.
		{name: "accepted but empty", status: 200, body: `{"web":{"results":[]}}`},
		{name: "rejected", status: 401, body: `{"error":"bad key"}`, want: ErrInvalidKey},
		{name: "forbidden", status: 403, body: `{"error":"no"}`, want: ErrInvalidKey},
		{name: "rate limited", status: 429, body: `slow down`, anyErr: true},
		{name: "provider down", status: 503, body: `oops`, anyErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			p := KeyedProvider("brave", "the-key", srv.URL)
			err := Verify(context.Background(), srv.Client(), p)

			switch {
			case tc.want != nil:
				if !errors.Is(err, tc.want) {
					t.Fatalf("err = %v, want %v", err, tc.want)
				}
			case tc.anyErr:
				if err == nil {
					t.Fatal("want an error")
				}
				if errors.Is(err, ErrInvalidKey) {
					t.Fatalf("a transient failure must not be reported as a bad key: %v", err)
				}
			default:
				if err != nil {
					t.Fatalf("want nil, got %v", err)
				}
			}
		})
	}
}

func TestVerifyNeverEchoesTheKey(t *testing.T) {
	const key = "super-secret-brave-key"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		// A provider echoing the credential back in its error body is exactly
		// the case errSnippet exists for.
		_, _ = w.Write([]byte(`{"error":"invalid token ` + key + `"}`))
	}))
	defer srv.Close()

	err := Verify(context.Background(), srv.Client(), KeyedProvider("brave", key, srv.URL))
	if err == nil {
		t.Fatal("want an error")
	}
	if contains(err.Error(), key) {
		t.Fatalf("error leaked the api key: %v", err)
	}
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
