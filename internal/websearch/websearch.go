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
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rookery-ai/rookery/internal/nethttp"
)

// Outcome is the result of one cascade run. Provider names the engine that
// actually served the results — the single most useful fact this package can
// report, and the one it used to throw away at the moment it was known.
// It is empty when every provider was exhausted. Tried lists every engine
// attempted, in order, so a caller can tell the user what was actually looked
// at rather than a bare "no results".
type Outcome struct {
	Results  []Result
	Provider string
	Tried    []string
}

// Label maps a provider's internal Name() to a human display string. It is the
// single source for these strings: the web_search tool description and the
// per-result provenance tag both render through it, so they cannot drift apart
// and start naming the same engine differently.
func Label(name string) string {
	switch name {
	case "brave":
		return "Brave Search"
	case "tavily":
		return "Tavily"
	case "ddg-html", "ddg-lite":
		return "DuckDuckGo"
	case "mojeek":
		return "Mojeek"
	case "bing":
		return "Bing"
	}
	return name
}

// Labels maps provider names to display strings, collapsing duplicates while
// preserving order — the keyless cascade holds two DuckDuckGo entries
// (ddg-html, ddg-lite) that a user has no reason to see listed twice.
func Labels(names []string) []string {
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))
	for _, n := range names {
		l := Label(n)
		if seen[l] {
			continue
		}
		seen[l] = true
		out = append(out, l)
	}
	return out
}

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
//
// A dial-guard rejection is explicitly NOT transient even though it arrives as
// a network error: the host resolved into blocked address space, and it will
// resolve there again on every retry. Retrying it just triples the latency
// before the cascade moves on.
func isTransient(err error) bool {
	if errors.Is(err, nethttp.ErrBlockedAddr) {
		return false
	}
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
// Every log line here carries the provider, the status and the result count —
// and deliberately NEVER the query, which is user content. Knowing that Brave
// served six results is what an operator needs; knowing what was searched for
// is not theirs to have in a log file.
func (c *Client) Search(ctx context.Context, query string) (Outcome, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return Outcome{}, fmt.Errorf("query is required")
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

	out := Outcome{}
	for _, p := range c.Providers {
		out.Tried = append(out.Tried, p.Name())
		results, err := c.runProvider(ctx, hc, base, p, query)
		if err != nil {
			if ctx.Err() != nil {
				return Outcome{}, ctx.Err()
			}
			// A blocked host is logged distinctly: it reads as a network error
			// but the cause is local DNS policy, and telling the two apart is
			// the difference between "search is flaky" and "your resolver is
			// answering api.search.brave.com with 0.0.0.0".
			if errors.Is(err, nethttp.ErrBlockedAddr) {
				slog.Warn("websearch provider blocked",
					"provider", p.Name(), "err", err,
					"hint", "resolved into blocked address space; check local DNS filtering")
			} else {
				slog.Warn("websearch provider failed", "provider", p.Name(), "err", err)
			}
			continue // hard failure for this engine — try the next
		}
		if len(results) > 0 {
			out.Results = dedupe(results)
			out.Provider = p.Name()
			slog.Debug("websearch provider served",
				"provider", p.Name(), "results", len(out.Results))
			return out, nil
		}
		slog.Debug("websearch provider empty", "provider", p.Name())
		if ctx.Err() != nil {
			return Outcome{}, ctx.Err()
		}
	}
	slog.Warn("websearch exhausted every provider", "tried", strings.Join(out.Tried, ","))
	return out, nil
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
