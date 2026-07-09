package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// statusServer returns status for the first n requests, then 200 with the
// given body for the rest. It counts how many requests it received.
func statusServer(t *testing.T, firstStatuses []int, okBody string) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		idx := int(n) - 1
		if idx < len(firstStatuses) {
			if ra := r.Header.Get("X-Test-Retry-After"); ra != "" {
				w.Header().Set("Retry-After", ra)
			}
			w.WriteHeader(firstStatuses[idx])
			fmt.Fprint(w, `{"error":"throttle"}`)
			return
		}
		w.WriteHeader(200)
		fmt.Fprint(w, okBody)
	}))
	return srv, &calls
}

func TestDoJSON_Transient429RetriesUntilSuccess(t *testing.T) {
	srv, calls := statusServer(t, []int{429, 429}, `{"ok":true}`)
	defer srv.Close()

	// Use a ctx with a generous deadline so the backoff (1s, then 2s) can elapse.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	body, code, err := doJSON(ctx, srv.Client(), http.MethodPost, srv.URL, nil, []byte(`{}`))
	if err != nil {
		t.Fatalf("doJSON: %v (calls=%d)", err, atomic.LoadInt32(calls))
	}
	if code != 200 {
		t.Fatalf("code=%d, want 200", code)
	}
	if !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("body=%q, want ok body", string(body))
	}
	if got := atomic.LoadInt32(calls); got != 3 {
		t.Fatalf("calls=%d, want 3 (two 429s then a 200)", got)
	}
}

func TestDoJSON_QuotaExhaustedDoesNotRetry(t *testing.T) {
	// 402 is not transient — must return immediately without retrying.
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(402)
		fmt.Fprint(w, `{"error":"payment required"}`)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, _, err := doJSON(ctx, srv.Client(), http.MethodPost, srv.URL, nil, []byte(`{}`))
	if !errors.Is(err, ErrQuotaExhausted) {
		t.Fatalf("err=%v, want ErrQuotaExhausted", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("calls=%d, want 1 (402 must not be retried)", got)
	}
}

func TestDoJSON_RateLimitAfterExhaustedBudgetReturnsErrRateLimit(t *testing.T) {
	// Always 429 — must exhaust the attempt budget and return ErrRateLimit.
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(429)
		fmt.Fprint(w, `{"error":"rate limited"}`)
	}))
	defer srv.Close()

	// Short deadline so the test doesn't wait through the full 7-attempt
	// backoff schedule; we just need to confirm it eventually gives up with
	// ErrRateLimit rather than hanging or returning a different error.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, _, err := doJSON(ctx, srv.Client(), http.MethodPost, srv.URL, nil, []byte(`{}`))
	if !errors.Is(err, ErrRateLimit) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v, want ErrRateLimit or deadline (budget exhausted)", err)
	}
}

// emptyThenOKServer returns 200 with an EMPTY body for the first emptyN requests
// (the OpenRouter upstream-hiccup signature), then 200 with okBody. It counts calls.
func emptyThenOKServer(t *testing.T, emptyN int, okBody string) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(atomic.AddInt32(&calls, 1))
		if n <= emptyN {
			// 200 OK with an empty body — the transport hiccup we must retry through.
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(200)
		fmt.Fprint(w, okBody)
	}))
	return srv, &calls
}

func TestDoJSON_Empty200BodyRetriesUntilSuccess(t *testing.T) {
	// Two empty 200s, then a real 200 body. Must retry and succeed (not die with
	// "unexpected end of JSON input"). Generous ctx so the backoff can elapse.
	srv, calls := emptyThenOKServer(t, 2, `{"ok":true}`)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	body, code, err := doJSON(ctx, srv.Client(), http.MethodPost, srv.URL, nil, []byte(`{}`))
	if err != nil {
		t.Fatalf("doJSON: %v (calls=%d)", err, atomic.LoadInt32(calls))
	}
	if code != 200 {
		t.Fatalf("code=%d, want 200", code)
	}
	if !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("body=%q, want ok body", string(body))
	}
	if got := atomic.LoadInt32(calls); got != 3 {
		t.Fatalf("calls=%d, want 3 (two empty 200s then a real 200)", got)
	}
}

func TestDoJSON_PersistentEmpty200ReturnsClearError(t *testing.T) {
	// Always 200 with an empty body — must keep retrying through the empty 200s
	// (the fix) rather than dying on the first hit with a JSON parse failure. With a
	// short ctx the 7-attempt backoff (~68s total) won't fully elapse, so we accept
	// either the explicit "empty response body" error OR a deadline — but NEVER an
	// opaque "unexpected end of JSON input" parse error.
	srv, calls := emptyThenOKServer(t, 100, `{"ok":true}`)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, _, err := doJSON(ctx, srv.Client(), http.MethodPost, srv.URL, nil, []byte(`{}`))
	if err == nil {
		t.Fatal("expected an error for a persistently empty 200 body")
	}
	if strings.Contains(err.Error(), "unexpected end of JSON") || strings.Contains(err.Error(), "parse openai response") {
		t.Fatalf("err=%v, an empty 200 must NOT surface as a parse failure", err)
	}
	if got := atomic.LoadInt32(calls); got < 2 {
		t.Fatalf("calls=%d, want >=2 (empty 200 must be retried, not returned on first hit)", got)
	}
}
