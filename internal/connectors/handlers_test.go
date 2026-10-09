package connectors

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A `handler:` naming a function nobody registered would fail only at call time — at
// 03:00, inside a scheduled run. Catch it at build time instead.
func TestHandlerActionsAreRegistered(t *testing.T) {
	r := testRegistry(t)
	for _, prov := range r.ProviderNames() {
		for _, a := range r.Actions(prov) {
			if a.Handler == "" {
				continue
			}
			if _, ok := handlers[a.Handler]; !ok {
				t.Errorf("%s/%s names handler %q, which is not registered", prov, a.Name, a.Handler)
			}
		}
	}
}

// The hygiene tests skip handler actions because their parameters are read in Go. This
// is the replacement guarantee: every parameter a handler action offers appears as a
// string literal in the package's non-test Go source, so a parameter the model is
// offered but nothing reads — the quiet failure those tests exist for — still fails.
func TestHandlerParamsAreRead(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	var src strings.Builder
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src.Write(b)
	}
	all := src.String()
	r := testRegistry(t)
	for _, prov := range r.ProviderNames() {
		for _, a := range r.Actions(prov) {
			if a.Handler == "" {
				continue
			}
			var schema struct {
				Properties map[string]json.RawMessage `json:"properties"`
			}
			json.Unmarshal(a.Params, &schema)
			for name := range schema.Properties {
				if ignoredLegacyParams[a.Name+"."+name] {
					continue
				}
				if !strings.Contains(all, `"`+name+`"`) {
					t.Errorf("%s/%s offers %q but no handler code reads it", prov, a.Name, name)
				}
			}
		}
	}
}

// Moving an action to a handler must not move it past a gate: the build guard refuses
// it before any network call.
func TestHandlerRunsAfterBuildGuard(t *testing.T) {
	called := false
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, http.ErrHandlerTimeout
	})}
	_, err := Execute(context.Background(), testRegistry(t), fakeStore{tok: "AT"}, client,
		ConnRef{ID: "c", Provider: "google_docs"}, "docs_insert_text",
		map[string]any{"document_id": "D", "text": "x", "anchor_text": "y"}, Policy{BuildPhase: true})
	ce, ok := err.(*ConnectorError)
	if !ok || ce.Kind != KindBuildBlocked {
		t.Fatalf("want build-blocked, got %v", err)
	}
	if called {
		t.Fatal("handler reached the network during a build")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
