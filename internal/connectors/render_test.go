package connectors

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderQuerySubstitution(t *testing.T) {
	a := Action{Request: RequestTemplate{
		Method: "GET",
		URL:    "https://api/messages",
		Query:  map[string]string{"q": "{{query}}", "maxResults": "{{max}}"},
	}}
	_, u, _, _, err := renderRequest(a, map[string]any{"query": "from:boss", "max": float64(5)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(u, "q=from%3Aboss") || !strings.Contains(u, "maxResults=5") {
		t.Fatalf("bad url: %s", u)
	}
}

func TestRenderDropsEmptyQuery(t *testing.T) {
	a := Action{Request: RequestTemplate{Method: "GET", URL: "https://api/m", Query: map[string]string{"q": "{{query}}", "maxResults": "{{max}}"}}}
	_, u, _, _, _ := renderRequest(a, map[string]any{"query": "hi"}, nil) // max omitted
	if strings.Contains(u, "maxResults") {
		t.Fatalf("empty query param should be dropped: %s", u)
	}
}

func TestRenderGmailRFC822(t *testing.T) {
	a := Action{Request: RequestTemplate{Method: "POST", URL: "https://api/send", BodyBuilder: "gmail_rfc822"}}
	_, _, body, ct, err := renderRequest(a, map[string]any{"to": "a@b.com", "subject": "Hi", "body": "Hello"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type: %s", ct)
	}
	var env struct {
		Raw string `json:"raw"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	dec, err := base64.URLEncoding.DecodeString(env.Raw)
	if err != nil {
		t.Fatalf("raw not base64url: %v", err)
	}
	if !strings.Contains(string(dec), "To: a@b.com") || !strings.Contains(string(dec), "Hello") {
		t.Fatalf("rfc822 missing fields: %s", dec)
	}
}

func TestRenderBodyArrayPassthroughAndOmit(t *testing.T) {
	a := Action{Request: RequestTemplate{Method: "POST", URL: "https://api/modify", Body: map[string]any{
		"addLabelIds":    "{{add}}",
		"removeLabelIds": "{{remove}}",
	}}}
	_, _, body, ct, err := renderRequest(a, map[string]any{"add": []any{"L1", "L2"}}, nil) // remove omitted
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ct, "application/json") {
		t.Fatalf("ct=%s", ct)
	}
	var got map[string]any
	json.Unmarshal(body, &got)
	arr, ok := got["addLabelIds"].([]any)
	if !ok || len(arr) != 2 || arr[0] != "L1" {
		t.Fatalf("addLabelIds not passed through as array: %s", body)
	}
	if _, present := got["removeLabelIds"]; present {
		t.Fatalf("absent optional key must be omitted: %s", body)
	}
}

func TestRenderBodyNestedAndEmbedded(t *testing.T) {
	a := Action{Request: RequestTemplate{Method: "POST", URL: "https://api/chat", Body: map[string]any{
		"model": "{{model}}",
		"messages": []any{
			map[string]any{"role": "user", "content": "{{prompt}}"},
		},
	}}}
	_, _, body, _, _ := renderRequest(a, map[string]any{"model": "gpt-4o", "prompt": "hi \"there\""}, nil)
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("body must be valid json even with quotes in arg: %v (%s)", err, body)
	}
	msgs := got["messages"].([]any)
	m0 := msgs[0].(map[string]any)
	if m0["content"] != `hi "there"` {
		t.Fatalf("nested content wrong: %v", m0["content"])
	}
}

func TestRenderGmailReply(t *testing.T) {
	a := Action{Request: RequestTemplate{Method: "POST", URL: "https://api/threads/send", BodyBuilder: "gmail_reply"}}
	_, _, body, ct, err := renderRequest(a, map[string]any{
		"thread_id": "T1", "to": "a@b.com", "subject": "Re: hi", "body": "reply text",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ct, "application/json") {
		t.Fatalf("ct=%s", ct)
	}
	var env struct {
		Raw      string `json:"raw"`
		ThreadID string `json:"threadId"`
	}
	json.Unmarshal(body, &env)
	if env.ThreadID != "T1" {
		t.Fatalf("threadId missing: %s", body)
	}
	dec, _ := base64.URLEncoding.DecodeString(env.Raw)
	if !strings.Contains(string(dec), "reply text") {
		t.Fatalf("body missing: %s", dec)
	}
}
