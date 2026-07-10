package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeStore struct{ tok string }

func (f fakeStore) AccessToken(_ context.Context, _ ConnRef) (string, error) { return f.tok, nil }

func testRegistry(t *testing.T) *Registry {
	r, err := LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestExecuteReadRewritesURLAndBearer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer AT" {
			t.Errorf("bearer missing")
		}
		w.Write([]byte(`{"messages":[{"id":"m1"}]}`))
	}))
	defer srv.Close()

	reg := testRegistry(t)
	a, _ := reg.Action("google", "gmail_search")
	a.Request.URL = srv.URL + "/messages"
	reg.actions["google"] = []Action{a}

	res, err := Execute(context.Background(), reg, fakeStore{tok: "AT"}, srv.Client(),
		ConnRef{ID: "c1", Provider: "google"}, "gmail_search", map[string]any{"query": "hi"}, false)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(string(res.Data), "m1") {
		t.Fatalf("extract failed: %s", res.Data)
	}
}

func TestExecuteBuildBlocksMutating(t *testing.T) {
	reg := testRegistry(t)
	_, err := Execute(context.Background(), reg, fakeStore{tok: "AT"}, http.DefaultClient,
		ConnRef{ID: "c1", Provider: "google"}, "gmail_send_email",
		map[string]any{"to": "a@b.com", "body": "hi"}, true) // buildPhase = true
	ce, ok := err.(*ConnectorError)
	if !ok || ce.Kind != KindBuildBlocked {
		t.Fatalf("expected KindBuildBlocked, got %v", err)
	}
}

func TestExecuteBadArgs(t *testing.T) {
	reg := testRegistry(t)
	_, err := Execute(context.Background(), reg, fakeStore{tok: "AT"}, http.DefaultClient,
		ConnRef{Provider: "google"}, "gmail_search", map[string]any{}, false) // missing query
	if ce, ok := err.(*ConnectorError); !ok || ce.Kind != KindBadArgs {
		t.Fatalf("expected KindBadArgs, got %v", err)
	}
}

func TestExecuteMapsProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":"invalid creds"}`))
	}))
	defer srv.Close()
	reg := testRegistry(t)
	a, _ := reg.Action("google", "gmail_search")
	a.Request.URL = srv.URL + "/m"
	reg.actions["google"] = []Action{a}
	_, err := Execute(context.Background(), reg, fakeStore{tok: "AT"}, srv.Client(),
		ConnRef{Provider: "google"}, "gmail_search", map[string]any{"query": "x"}, false)
	if ce, ok := err.(*ConnectorError); !ok || ce.Kind != KindAuth {
		t.Fatalf("expected KindAuth, got %v", err)
	}
}
