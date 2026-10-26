package coder

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// searchFixture serves DDG-shaped pages: a query containing "hit" gets one
// result, anything else gets an empty page — or, with fail set, a 403 for every
// request. hits counts requests that reached the server, which is the property
// the allowance exists to bound.
func searchFixture(t *testing.T, fail bool) (*hostToolSet, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if fail {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		if strings.Contains(r.URL.RawQuery, "hit") {
			_, _ = w.Write([]byte(ddgHTML([][2]string{{"Found", "https://example.com/a"}}, []string{"ok"})))
			return
		}
		_, _ = w.Write([]byte(`<html><body>nothing</body></html>`))
	}))
	t.Cleanup(srv.Close)
	return &hostToolSet{includeExecTools: true, webRetryBase: time.Millisecond, ddgBaseURL: srv.URL, allowPrivateHosts: true}, &hits
}

// The reported loop in one assertion: an empty search used to count as a
// SUCCESSFUL call, which reset the unproductive streak on every turn, so a
// model rephrasing a query that could not be answered was never stopped.
func TestEmptySearchIsNotProductive(t *testing.T) {
	h, _ := searchFixture(t, false)
	h.executeOrNudge(context.Background(), webSearchCall("nothing here"))
	st := h.callStats()
	if st.Productive != 0 {
		t.Fatalf("an empty search must not count as progress; Productive = %d", st.Productive)
	}
	if st.Failed != 0 {
		t.Fatalf("an empty search must not count as a failure either; Failed = %d", st.Failed)
	}
	h.executeOrNudge(context.Background(), webSearchCall("hit please"))
	if got := h.callStats().Productive; got != 1 {
		t.Fatalf("a search with results is progress; Productive = %d, want 1", got)
	}
}

func TestSearchAllowanceStopsAfterThreeEmptyInARow(t *testing.T) {
	h, hits := searchFixture(t, false)
	for _, q := range []string{"one", "two", "three"} {
		h.executeOrNudge(context.Background(), webSearchCall(q))
	}
	before := hits.Load()
	res := h.executeOrNudge(context.Background(), webSearchCall("a different wording"))
	if hits.Load() != before {
		t.Fatalf("the 4th empty-streak search reached the network; it must be refused")
	}
	if strings.HasPrefix(res, "error:") {
		t.Fatalf("the allowance notice is a settled outcome, not an error: %q", res)
	}
	if !strings.Contains(res, "search allowance") {
		t.Fatalf("expected the allowance notice, got %q", res)
	}
	if got := h.callStats().Productive; got != 0 {
		t.Fatalf("a refused search is not progress; Productive = %d", got)
	}
}

func TestSearchResultResetsStreak(t *testing.T) {
	h, hits := searchFixture(t, false)
	for _, q := range []string{"one", "two", "hit", "three", "four"} {
		h.executeOrNudge(context.Background(), webSearchCall(q))
	}
	before := hits.Load()
	h.executeOrNudge(context.Background(), webSearchCall("five"))
	if hits.Load() == before {
		t.Fatalf("a search that found results must reset the streak; the 6th call was refused")
	}
}

func TestSearchAllowanceTotalCap(t *testing.T) {
	h, hits := searchFixture(t, false)
	// Never three empties in a row, so only the per-call total can stop it.
	for i := 0; i < maxEmptySearches; i++ {
		h.executeOrNudge(context.Background(), webSearchCall("empty"+string(rune('a'+i))))
		h.executeOrNudge(context.Background(), webSearchCall("hit"+string(rune('a'+i))))
	}
	before := hits.Load()
	res := h.executeOrNudge(context.Background(), webSearchCall("one more"))
	if hits.Load() != before || !strings.Contains(res, "search allowance") {
		t.Fatalf("after %d empty searches the next must be refused; got %q", maxEmptySearches, res)
	}
}

// A blocked network cannot be fixed by rewording, so search that is DOWN must
// say so and switch itself off, rather than spending the allowance one 15-second
// cascade at a time.
func TestAllProvidersFailingDisablesSearch(t *testing.T) {
	h, hits := searchFixture(t, true)
	res := h.executeOrNudge(context.Background(), webSearchCall("one"))
	if !strings.Contains(res, "unavailable") {
		t.Fatalf("every engine failing must be reported as unavailable, got %q", res)
	}
	if strings.HasPrefix(res, "error:") {
		t.Fatalf("unavailable is reported, not raised: %q", res)
	}
	h.executeOrNudge(context.Background(), webSearchCall("two"))
	before := hits.Load()
	h.executeOrNudge(context.Background(), webSearchCall("three"))
	if hits.Load() != before {
		t.Fatalf("after %d unavailable outcomes search must stop reaching the network", maxUnavailableSearches)
	}
}
