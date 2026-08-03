package coder

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// toolDesc returns the named tool's description, or fails the test.
func toolDesc(t *testing.T, h *hostToolSet, name string) string {
	t.Helper()
	tl, ok := findTool(h.tools(), name)
	if !ok {
		t.Fatalf("tool %q not offered", name)
	}
	return tl.Description
}

// TestWebSearchDescriptionNamesTheConfiguredEngine is the fix for the reported
// bug: the description used to be the literal string "(DuckDuckGo)", so a
// workspace with a Brave key was told — and told the user — that it was
// searching DuckDuckGo.
func TestWebSearchDescriptionNamesTheConfiguredEngine(t *testing.T) {
	keyed := &hostToolSet{subprocessEnv: map[string]string{"SEARCH_KEY_BRAVE": "k"}}
	desc := toolDesc(t, keyed, "web_search")
	if !strings.Contains(desc, "Brave Search") {
		t.Fatalf("a workspace with a Brave key must see Brave named: %q", desc)
	}
	if strings.Index(desc, "Brave Search") > strings.Index(desc, "DuckDuckGo") &&
		strings.Contains(desc, "DuckDuckGo") {
		t.Fatalf("the keyed provider must be named first: %q", desc)
	}

	keyless := &hostToolSet{}
	desc = toolDesc(t, keyless, "web_search")
	if strings.Contains(desc, "Brave Search") {
		t.Fatalf("no key configured, Brave must not be named: %q", desc)
	}
	if !strings.Contains(desc, "DuckDuckGo") {
		t.Fatalf("the keyless cascade should still be named: %q", desc)
	}
}

// TestWebSearchResultCarriesProvenance covers the stronger of the two
// mechanisms. In the transcript that prompted this change the model reported
// the wrong engine while holding a real result set in context; the result tag
// is what makes that impossible, because the engine name travels with the data.
func TestWebSearchResultCarriesProvenance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(ddgHTML(
			[][2]string{{"Time MK", "https://time.mk"}},
			[]string{"Macedonian news aggregator."},
		)))
	}))
	defer srv.Close()

	h := &hostToolSet{webRetryBase: time.Millisecond, ddgBaseURL: srv.URL, allowPrivateHosts: true}
	res := h.execute(context.Background(), webSearchCall("time.mk"))
	if strings.HasPrefix(res, "error:") {
		t.Fatalf("web_search failed: %q", res)
	}
	if !strings.HasPrefix(res, "Results via DuckDuckGo:") {
		t.Fatalf("result must lead with the engine that served it; got %q", res)
	}
	if !strings.Contains(res, "time.mk") {
		t.Fatalf("results missing: %q", res)
	}
}

// TestWebSearchEmptyNamesWhatWasTried: a total cascade failure and a genuinely
// empty query used to be indistinguishable to the model — both rendered as a
// bare "(no search results)".
func TestWebSearchEmptyNamesWhatWasTried(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>no results here</body></html>"))
	}))
	defer srv.Close()

	h := &hostToolSet{webRetryBase: time.Millisecond, ddgBaseURL: srv.URL, allowPrivateHosts: true}
	res := h.execute(context.Background(), webSearchCall("nothing matches this"))
	if strings.HasPrefix(res, "error:") {
		t.Fatalf("an empty result set must not be an error: %q", res)
	}
	if !strings.Contains(res, "no search results") || !strings.Contains(res, "DuckDuckGo") {
		t.Fatalf("empty notice should name the engines tried; got %q", res)
	}
}

// TestNoHardcodedEngineInStaticToolText guards the regression directly: the
// engine name must come from the configured provider list, never from a literal
// baked into the tool description.
func TestNoHardcodedEngineInStaticToolText(t *testing.T) {
	h := &hostToolSet{subprocessEnv: map[string]string{"SEARCH_KEY_BRAVE": "k"}}
	for _, tl := range h.tools() {
		if tl.Name == "web_search" {
			continue // its engine list is generated, and asserted above
		}
		for _, engine := range []string{"DuckDuckGo", "Brave Search", "Mojeek"} {
			if strings.Contains(tl.Description, engine) {
				t.Fatalf("tool %q hardcodes the search engine %q", tl.Name, engine)
			}
		}
	}
}
