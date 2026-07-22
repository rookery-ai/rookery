// Package websearch turns a query into web results using a cascade of
// providers. A single keyless scrape is structurally unreliable — one layout
// change or JS-challenge interstitial and it silently returns nothing — so this
// package treats "this engine produced no parseable results" as a reason to try
// the next engine rather than as an answer.
package websearch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ilijad1/simple-agents/internal/nethttp"
)

// Result is one search hit.
type Result struct {
	Title   string
	URL     string
	Snippet string
}

// Provider is one search backend. Name identifies it in logs. Search returns
// results, or an error; returning zero results with a nil error is a valid
// outcome that the cascade treats as "try the next provider".
type Provider interface {
	Name() string
	Search(ctx context.Context, hc *http.Client, query string) ([]Result, error)
}

// maxAttempts bounds the per-provider transient-retry loop.
const maxAttempts = 3

// Client runs providers in order.
type Client struct {
	HTTP      *http.Client
	RetryBase time.Duration
	Providers []Provider
}

// transientError marks a failure worth retrying against the SAME provider
// (429, 5xx, network, timeout) rather than moving on to the next one.
type transientError struct{ err error }

func (e transientError) Error() string { return e.err.Error() }
func (e transientError) Unwrap() error { return e.err }

// Transient wraps err as a retryable failure. Providers use it to opt into the
// per-provider retry loop.
func Transient(err error) error { return transientError{err} }

// isTransient reports whether err (or anything it wraps) is a transientError.
// errors.As already walks the Unwrap() chain, which is all a hand-rolled loop
// would do here, so we lean on the stdlib rather than reimplement it.
func isTransient(err error) bool {
	var t transientError
	return errors.As(err, &t)
}

// Search tries each provider in order and returns the first non-empty result
// set. A provider that errors, or that returns zero results, falls through to
// the next one — the single most important reliability property here, because
// every keyless engine fails in both of those ways routinely.
//
// Exhausting every provider is NOT an error: it returns an empty slice with a
// nil error. The caller renders that as an explicit "no results" notice. An
// error result would be treated by the coder's oscillation guard as a failing
// call worth blocking, which is wrong for a query that simply matched nothing.
func (c *Client) Search(ctx context.Context, query string) ([]Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}
	hc := c.HTTP
	if hc == nil {
		// Every real caller (hosttools.go's webSearch) injects HTTP explicitly —
		// this default only guards a hypothetical future caller that doesn't —
		// but it should still be the SAME guarded client every other outbound
		// path in this codebase uses, not a bare client that can reach loopback
		// and private address space.
		hc = nethttp.GuardedClient(30 * time.Second)
	}
	base := c.RetryBase
	if base <= 0 {
		base = 500 * time.Millisecond
	}

	for _, p := range c.Providers {
		results, err := c.runProvider(ctx, hc, base, p, query)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue // hard failure for this engine — try the next
		}
		if len(results) > 0 {
			return dedupe(results), nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, nil
}

// runProvider retries one provider's transient failures with exponential backoff.
func (c *Client) runProvider(ctx context.Context, hc *http.Client, base time.Duration, p Provider, query string) ([]Result, error) {
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			if !sleepCtx(ctx, base<<(attempt-1)) {
				return nil, ctx.Err()
			}
		}
		results, err := p.Search(ctx, hc, query)
		if err == nil {
			return results, nil
		}
		lastErr = err
		if !isTransient(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// dedupe collapses results whose URLs differ only in trailing slash, a
// leading "www.", scheme (normalizeURL drops the scheme entirely, not just
// its case — http vs https is the same page), or fragment (normalizeURL
// drops it too — "#section-a" vs "#section-b" is the same page for
// search-result purposes). Both drops are a deliberate trade-off: a
// same-host, same-path, same-query result is treated as a duplicate even
// when the scheme or fragment differs, because the same page surfaced twice
// is noise.
func dedupe(in []Result) []Result {
	seen := make(map[string]bool, len(in))
	out := make([]Result, 0, len(in))
	for _, r := range in {
		key := normalizeURL(r.URL)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	return out
}

func normalizeURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return strings.TrimSpace(raw)
	}
	host := strings.ToLower(strings.TrimPrefix(u.Host, "www."))
	path := strings.TrimSuffix(u.Path, "/")
	return host + path + "?" + u.RawQuery
}
