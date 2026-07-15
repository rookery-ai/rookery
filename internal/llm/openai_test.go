package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestOpenAIBuildBody_ToolMessageAlwaysHasContent is the regression guard for the
// Mistral 422 ("messages[n].tool.content: Field required"): a tool-result message with
// empty content must still serialize a `content` field. `content,omitempty` would drop
// it, which OpenAI tolerates but Mistral rejects, failing the whole run.
func TestOpenAIBuildBody_ToolMessageAlwaysHasContent(t *testing.T) {
	p := &openaiProvider{model: "mistral-small"}
	body := p.buildBody("mistral-small", Request{
		Messages: []Message{
			{Role: "user", Content: "do it"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call1", Name: "run_script", Args: json.RawMessage(`{"path":"tools/x.py"}`)}}},
			{Role: "tool", ToolCallID: "call1", Name: "run_script", Content: ""}, // empty stdout
		},
	})

	var payload struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}

	var toolMsg map[string]any
	for _, m := range payload.Messages {
		if m["role"] == "tool" {
			toolMsg = m
		}
	}
	if toolMsg == nil {
		t.Fatal("no tool message serialized")
	}
	content, ok := toolMsg["content"]
	if !ok {
		t.Fatalf("tool message is missing the required `content` field: %v", toolMsg)
	}
	if s, _ := content.(string); strings.TrimSpace(s) == "" {
		t.Errorf("tool message content must be non-empty, got %q", s)
	}
}

// TestOpenAIToolCall_ThoughtSignatureRoundTrips guards the Gemini-3 tool-calling
// break: Gemini attaches a required `thought_signature` under a non-standard
// `tool_calls[N].extra_content` field. If we drop it, the SECOND turn's replayed
// history is missing the signature and Gemini rejects the whole request with a 400
// ("Function call is missing a thought_signature"). parseOpenAIResponse must capture
// extra_content, and buildBody must re-emit it verbatim on the next turn.
func TestOpenAIToolCall_ThoughtSignatureRoundTrips(t *testing.T) {
	// 1. Parse a Gemini-style response that carries extra_content on the tool call.
	respJSON := []byte(`{
      "choices": [{
        "message": {
          "role": "assistant",
          "tool_calls": [{
            "id": "call_1",
            "type": "function",
            "function": {"name": "list_dir", "arguments": "{\"path\":\".\"}"},
            "extra_content": {"google": {"thought_signature": "SIG_ABC123"}}
          }]
        },
        "finish_reason": "tool_calls"
      }]
    }`)
	resp, err := parseOpenAIResponse(respJSON)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("want 1 tool call, got %d", len(resp.ToolCalls))
	}
	if len(resp.ToolCalls[0].Extra) == 0 || !strings.Contains(string(resp.ToolCalls[0].Extra), "SIG_ABC123") {
		t.Fatalf("extra_content not captured on parse, got %q", string(resp.ToolCalls[0].Extra))
	}

	// 2. Replay that tool call in the next turn's request — the signature must survive.
	p := &openaiProvider{model: "gemini-3-flash"}
	body := p.buildBody("gemini-3-flash", Request{
		Messages: []Message{
			{Role: "user", Content: "list files"},
			{Role: "assistant", ToolCalls: resp.ToolCalls},
			{Role: "tool", ToolCallID: "call_1", Name: "list_dir", Content: "README.md"},
		},
	})
	if !strings.Contains(string(body), "extra_content") || !strings.Contains(string(body), "SIG_ABC123") {
		t.Fatalf("replayed request dropped the thought_signature; body: %s", body)
	}

	// 3. A tool call WITHOUT extra_content (OpenAI/Mistral) must not serialize the field.
	plain := p.buildBody("gpt-4o", Request{
		Messages: []Message{
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Name: "f", Args: json.RawMessage(`{}`)}}},
		},
	})
	if strings.Contains(string(plain), "extra_content") {
		t.Errorf("extra_content leaked onto a tool call that had none: %s", plain)
	}
}

// TestOpenAIBuildBody_EmptyAssistantContentGetsPlaceholder guards G1: an assistant
// message with neither content nor tool calls must still serialize a non-empty content
// field, or a bare {"role":"assistant"} 400s on stricter endpoints. Happens on the
// verify-finish nudge branch when the model returns an empty final answer.
func TestOpenAIBuildBody_EmptyAssistantContentGetsPlaceholder(t *testing.T) {
	p := &openaiProvider{model: "mistral-small"}
	body := p.buildBody("mistral-small", Request{
		Messages: []Message{
			{Role: "user", Content: "do it"},
			{Role: "assistant", Content: ""}, // empty final answer, no tool calls
		},
	})
	var payload struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	var asst map[string]any
	for _, m := range payload.Messages {
		if m["role"] == "assistant" {
			asst = m
		}
	}
	if asst == nil {
		t.Fatal("no assistant message serialized")
	}
	if s, _ := asst["content"].(string); strings.TrimSpace(s) == "" {
		t.Errorf("empty assistant content must get a non-empty placeholder, got %q", s)
	}
}

// TestOpenAIBuildBody_EmptyAssistantWithToolCallsUnaffected: an assistant message with
// tool calls but empty content is VALID (content omitted) and must NOT get a placeholder
// that would sit alongside tool_calls.
func TestOpenAIBuildBody_EmptyAssistantWithToolCallsUnaffected(t *testing.T) {
	p := &openaiProvider{model: "m"}
	body := p.buildBody("m", Request{
		Messages: []Message{
			{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "c1", Name: "x", Args: json.RawMessage(`{}`)}}},
		},
	})
	if strings.Contains(string(body), "(no content)") {
		t.Errorf("assistant-with-tool-calls should not get a content placeholder: %s", body)
	}
}

// TestParseOpenAIResponse_SynthesizesEmptyToolCallID guards G2: a tool call returned with
// an empty id must be assigned a stable non-empty id so the echoed tool_call_id isn't dropped.
func TestParseOpenAIResponse_SynthesizesEmptyToolCallID(t *testing.T) {
	raw := `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"","type":"function","function":{"name":"run_script","arguments":"{}"}}]}}]}`
	resp, err := parseOpenAIResponse([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(resp.ToolCalls))
	}
	if strings.TrimSpace(resp.ToolCalls[0].ID) == "" {
		t.Errorf("empty tool_call id must be synthesized to a non-empty value")
	}
}
