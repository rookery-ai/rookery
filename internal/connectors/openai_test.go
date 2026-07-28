package connectors

import (
	"encoding/json"
	"testing"
)

func TestOpenAIChatBody(t *testing.T) {
	r, _ := LoadBundled()
	a, ok := r.Action("openai", "openai_chat_completion")
	if !ok {
		t.Fatal("openai_chat_completion missing")
	}
	_, _, body, _, err := renderRequest(a, map[string]any{
		"model":    "gpt-4o-mini",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.Unmarshal(body, &got)
	if got["model"] != "gpt-4o-mini" {
		t.Fatalf("bad body: %s", body)
	}
	if _, ok := got["messages"].([]any); !ok {
		t.Fatalf("messages not an array: %s", body)
	}
}

func TestOpenAIActionCount(t *testing.T) {
	r, _ := LoadBundled()
	if n := len(r.Actions("openai")); n < 8 {
		t.Fatalf("expected >=8 openai actions, got %d", n)
	}
}
