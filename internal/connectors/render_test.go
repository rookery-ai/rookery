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
	_, u, _, _, err := renderRequest(a, map[string]any{"query": "from:boss", "max": float64(5)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(u, "q=from%3Aboss") || !strings.Contains(u, "maxResults=5") {
		t.Fatalf("bad url: %s", u)
	}
}

func TestRenderDropsEmptyQuery(t *testing.T) {
	a := Action{Request: RequestTemplate{Method: "GET", URL: "https://api/m", Query: map[string]string{"q": "{{query}}", "maxResults": "{{max}}"}}}
	_, u, _, _, _ := renderRequest(a, map[string]any{"query": "hi"}) // max omitted
	if strings.Contains(u, "maxResults") {
		t.Fatalf("empty query param should be dropped: %s", u)
	}
}

func TestRenderGmailRFC822(t *testing.T) {
	a := Action{Request: RequestTemplate{Method: "POST", URL: "https://api/send", BodyBuilder: "gmail_rfc822"}}
	_, _, body, ct, err := renderRequest(a, map[string]any{"to": "a@b.com", "subject": "Hi", "body": "Hello"})
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
