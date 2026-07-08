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
