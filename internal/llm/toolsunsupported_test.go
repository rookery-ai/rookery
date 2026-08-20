package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The condition is classified by STATUS + BODY, and both halves are load-bearing.
//
// The status half: openaiProvider.Complete only ever asked about 400, but an
// OpenAI-compatible gateway does not have to answer 400. OpenRouter answers 404
// with "No endpoints found that support tool use." — so ErrToolsUnsupported was
// never returned, api_engine's degrade-to-a-no-tool-turn never fired, and the run
// died with exit=-1 instead. Observed live on
// meta-llama/llama-3.1-8b-instruct, run e9ecf3db-c935-430b-8b1f-9409bad03cb5.
//
// The body half: a 404 is ALSO what a wrong model slug returns. Treating every
// 404 as "tools unsupported" would silently retry without tools and hand back a
// degraded answer, hiding a configuration error. So the body must actually say
// so. The second test is the one that keeps that distinction honest — deleting
// the body check leaves the first test passing.
func newTestProvider(t *testing.T, status int, body string) Provider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	p, err := New(Config{Provider: "openrouter", APIKey: "k", BaseURL: srv.URL, Model: "m"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func toolReq() Request {
	return Request{
		Messages: []Message{{Role: "user", Content: "hi"}},
		Tools:    []Tool{{Name: "read_file", Description: "read a file"}},
	}
}

func TestOpenRouter404WithToolBodyIsToolsUnsupported(t *testing.T) {
	const body = `{"error":{"message":"No endpoints found that support tool use. ` +
		`Try disabling \"read_file\".","code":404}}`
	p := newTestProvider(t, http.StatusNotFound, body)

	_, err := p.Complete(context.Background(), toolReq())
	if !errors.Is(err, ErrToolsUnsupported) {
		t.Fatalf("a 404 naming tool support must classify as ErrToolsUnsupported so the\n"+
			"engine can degrade to a no-tool turn; got %v", err)
	}
}

// A wrong model slug also 404s. That must stay a real error, or a configuration
// mistake becomes an invisible quality regression.
func TestUnrelated404IsNotToolsUnsupported(t *testing.T) {
	p := newTestProvider(t, http.StatusNotFound,
		`{"error":{"message":"No such model: totally-made-up","code":404}}`)

	_, err := p.Complete(context.Background(), toolReq())
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, ErrToolsUnsupported) {
		t.Fatal("a 404 that says nothing about tools is a bad model/endpoint, not a " +
			"tool-support problem — misclassifying it hides the real cause")
	}
}

// 422 is the other status OpenAI-compatible gateways use for a rejected request
// shape, guarded identically.
func TestUnprocessableWithToolBodyIsToolsUnsupported(t *testing.T) {
	p := newTestProvider(t, http.StatusUnprocessableEntity,
		`{"error":{"message":"function calling is not supported by this model"}}`)

	if _, err := p.Complete(context.Background(), toolReq()); !errors.Is(err, ErrToolsUnsupported) {
		t.Fatalf("422 naming function calling must classify; got %v", err)
	}
}

// The pre-existing 400 path must keep working.
func TestBadRequestWithToolBodyStillClassifies(t *testing.T) {
	p := newTestProvider(t, http.StatusBadRequest,
		`{"error":{"message":"tools are not supported"}}`)

	if _, err := p.Complete(context.Background(), toolReq()); !errors.Is(err, ErrToolsUnsupported) {
		t.Fatalf("the original 400 classification regressed; got %v", err)
	}
}

// With no tools offered there is nothing to degrade to, so the classification
// must not fire — otherwise a genuine 404 on a tool-less call would be retried
// as if tools were the problem.
func TestNoToolsOfferedNeverClassifies(t *testing.T) {
	const body = `{"error":{"message":"No endpoints found that support tool use.","code":404}}`
	p := newTestProvider(t, http.StatusNotFound, body)

	_, err := p.Complete(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if errors.Is(err, ErrToolsUnsupported) {
		t.Fatal("must not classify when the request offered no tools")
	}
}
