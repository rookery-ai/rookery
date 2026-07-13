package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBridgeExecEndToEnd(t *testing.T) {
	// Fake provider API that gmail_search will hit.
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer AT" {
			t.Errorf("bearer not forwarded to provider: %q", r.Header.Get("Authorization"))
		}
		w.Write([]byte(`{"messages":[{"id":"mB"}]}`))
	}))
	defer provider.Close()

	reg := testRegistry(t)
	a, _ := reg.Action("google", "gmail_search")
	a.Request.URL = provider.URL + "/messages"
	reg.SetActionsForTest("google", []Action{a})

	br := NewBridge(reg, fakeStore{tok: "AT"}, provider.Client())
	addr, err := br.Start(context.Background())
	if err != nil {
		t.Fatalf("bridge start: %v", err)
	}
	token := br.Register("ws1", []BoundConn{{ID: "c1", Provider: "google", AccountLabel: "work"}}, false)

	// A valid exec call returns the provider data.
	body, _ := json.Marshal(execRequest{Tool: "gmail_search", Args: map[string]any{"query": "hi"}})
	req, _ := http.NewRequest("POST", addr+"/exec", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	out, _ := readBody(resp)
	if !strings.Contains(out, "mB") {
		t.Fatalf("exec did not return provider data: %s", out)
	}

	// A bad token is rejected.
	req2, _ := http.NewRequest("POST", addr+"/exec", bytes.NewReader(body))
	req2.Header.Set("Authorization", "Bearer wrong")
	resp2, _ := http.DefaultClient.Do(req2)
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad token should be 401, got %d", resp2.StatusCode)
	}

	// After Unregister the token no longer works.
	br.Unregister(token)
	req3, _ := http.NewRequest("POST", addr+"/exec", bytes.NewReader(body))
	req3.Header.Set("Authorization", "Bearer "+token)
	resp3, _ := http.DefaultClient.Do(req3)
	if resp3.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unregistered token should be 401, got %d", resp3.StatusCode)
	}
}

func TestBridgeBuildPhaseBlocksMutating(t *testing.T) {
	reg := testRegistry(t)
	br := NewBridge(reg, fakeStore{tok: "AT"}, http.DefaultClient)
	addr, _ := br.Start(context.Background())
	token := br.Register("ws1", []BoundConn{{ID: "c1", Provider: "google", AccountLabel: "work"}}, true) // buildPhase

	body, _ := json.Marshal(execRequest{Tool: "gmail_send_email", Args: map[string]any{"to": "a@b.com", "body": "hi"}})
	req, _ := http.NewRequest("POST", addr+"/exec", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, _ := http.DefaultClient.Do(req)
	out, _ := readBody(resp)
	if !strings.Contains(strings.ToLower(out), "build-time guard") {
		t.Fatalf("mutating action at build time should be guarded, got: %s", out)
	}
}

func readBody(resp *http.Response) (string, error) {
	defer resp.Body.Close()
	b := make([]byte, resp.ContentLength)
	if resp.ContentLength <= 0 {
		return "", nil
	}
	_, err := resp.Body.Read(b)
	return string(b), err
}
