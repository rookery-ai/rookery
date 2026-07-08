package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

// wireBody is the shape buildBody produces, enough to assert on role sequencing.
type wireBody struct {
	Messages []struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"messages"`
}

func TestAnthropicBuildBody_CoalescesMultipleToolResultsIntoOneUserMessage(t *testing.T) {
	p := &anthropicProvider{model: "claude-x"}
	req := Request{
		Model: "claude-x",
		Messages: []Message{
			{Role: "assistant", ToolCalls: []ToolCall{
				{ID: "call-1", Name: "read_file", Args: json.RawMessage(`{}`)},
				{ID: "call-2", Name: "write_file", Args: json.RawMessage(`{}`)},
			}},
			// Two tool results from the SAME turn — the engine emits one Message per
			// tool call. Anthropic requires these to land in a single user-role
			// message, not two consecutive user messages.
			{Role: "tool", ToolCallID: "call-1", Content: "file contents"},
			{Role: "tool", ToolCallID: "call-2", Content: "ok: wrote"},
		},
	}

	body := p.buildBody("claude-x", req)
	var parsed wireBody
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal wire body: %v", err)
	}

	// assistant, then exactly one user message (not two).
	if len(parsed.Messages) != 2 {
		t.Fatalf("got %d messages, want 2 (assistant + one coalesced user); roles: %+v", len(parsed.Messages), parsed.Messages)
	}
	if parsed.Messages[0].Role != "assistant" {
		t.Fatalf("messages[0].Role = %q, want assistant", parsed.Messages[0].Role)
	}
	if parsed.Messages[1].Role != "user" {
		t.Fatalf("messages[1].Role = %q, want user", parsed.Messages[1].Role)
	}
	if len(parsed.Messages[1].Content) != 2 {
		t.Fatalf("coalesced user message has %d content blocks, want 2 (one tool_result per call)", len(parsed.Messages[1].Content))
	}
	for _, b := range parsed.Messages[1].Content {
		if b.Type != "tool_result" {
			t.Fatalf("content block type = %q, want tool_result", b.Type)
		}
	}
}

func TestAnthropicBuildBody_SingleToolResultUnaffected(t *testing.T) {
	p := &anthropicProvider{model: "claude-x"}
	req := Request{
		Model: "claude-x",
		Messages: []Message{
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call-1", Name: "read_file", Args: json.RawMessage(`{}`)}}},
			{Role: "tool", ToolCallID: "call-1", Content: "file contents"},
			{Role: "assistant", Content: "[CHAT] done"},
		},
	}

	body := p.buildBody("claude-x", req)
	var parsed wireBody
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal wire body: %v", err)
	}
	if len(parsed.Messages) != 3 {
		t.Fatalf("got %d messages, want 3 (assistant, user, assistant); roles: %+v", len(parsed.Messages), parsed.Messages)
	}
	roles := []string{parsed.Messages[0].Role, parsed.Messages[1].Role, parsed.Messages[2].Role}
	want := []string{"assistant", "user", "assistant"}
	for i := range want {
		if roles[i] != want[i] {
			t.Fatalf("roles = %v, want %v", roles, want)
		}
	}
}

func TestAnthropicBuildBody_ConsecutiveToolTurnsDoNotMerge(t *testing.T) {
	// A "user"-role plain-text message followed by tool results must NOT be
	// merged into the preceding user message if that message wasn't itself made
	// of tool_result blocks (isToolResultMessage guards on block type).
	p := &anthropicProvider{model: "claude-x"}
	req := Request{
		Model: "claude-x",
		Messages: []Message{
			{Role: "user", Content: "please proceed"},
			{Role: "tool", ToolCallID: "call-1", Content: "result"},
		},
	}
	body := p.buildBody("claude-x", req)
	var parsed wireBody
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal wire body: %v", err)
	}
	// This is a pre-existing edge case (a bare "tool" message with no preceding
	// assistant tool_use) — the two must still not collapse into a single
	// mixed-content-type user message that hides the distinct blocks incorrectly.
	if len(parsed.Messages) != 2 {
		t.Fatalf("got %d messages, want 2 (plain user text kept separate from tool_result)", len(parsed.Messages))
	}
}

// TestAnthropicBuildBody_GraceNudgeAfterToolResultsCoalesces guards G3: the turn-budget
// grace nudge is a plain user message appended right after tool_result blocks. Without
// coalescing, that is a second consecutive user message and Anthropic 400s the fallback
// call it was built to make graceful. It must merge into the tool_result user turn as an
// extra text block (a user turn may hold tool_result + text).
func TestAnthropicBuildBody_GraceNudgeAfterToolResultsCoalesces(t *testing.T) {
	p := &anthropicProvider{model: "claude-x"}
	req := Request{
		Model: "claude-x",
		Messages: []Message{
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Name: "run_script", Args: json.RawMessage(`{}`)}}},
			{Role: "tool", ToolCallID: "c1", Content: "result"},
			{Role: "user", Content: "you are out of turns, wrap up"}, // the grace nudge
		},
	}
	body := p.buildBody("claude-x", req)
	var parsed wireBody
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(parsed.Messages) != 2 {
		t.Fatalf("got %d messages, want 2 (assistant + one user); no two consecutive user messages allowed", len(parsed.Messages))
	}
	if parsed.Messages[1].Role != "user" {
		t.Fatalf("messages[1].Role = %q, want user", parsed.Messages[1].Role)
	}
	// The user turn must carry the tool_result AND the coalesced nudge text.
	types := map[string]bool{}
	for _, b := range parsed.Messages[1].Content {
		types[b.Type] = true
	}
	if !types["tool_result"] || !types["text"] {
		t.Fatalf("coalesced user turn must contain both tool_result and text blocks, got %+v", parsed.Messages[1].Content)
	}
}

// TestAnthropicBuildBody_EmptyAssistantGetsTextBlock guards G1: an assistant turn with no
// content and no tool calls must still emit a non-empty content array (Anthropic rejects
// an empty one). Happens on the verify-finish nudge branch with an empty final answer.
func TestAnthropicBuildBody_EmptyAssistantGetsTextBlock(t *testing.T) {
	p := &anthropicProvider{model: "claude-x"}
	body := p.buildBody("claude-x", Request{
		Messages: []Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: ""}, // empty final answer, no tool calls
		},
	})
	var parsed wireBody
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	last := parsed.Messages[len(parsed.Messages)-1]
	if last.Role != "assistant" {
		t.Fatalf("last message role = %q, want assistant", last.Role)
	}
	if len(last.Content) == 0 {
		t.Fatalf("empty assistant turn must still have at least one content block")
	}
}

// TestParseAnthropicResponse_SynthesizesEmptyToolUseID guards G2 on the Anthropic path.
func TestParseAnthropicResponse_SynthesizesEmptyToolUseID(t *testing.T) {
	raw := `{"content":[{"type":"tool_use","id":"","name":"run_script","input":{}}],"stop_reason":"tool_use"}`
	resp, err := parseAnthropicResponse([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(resp.ToolCalls))
	}
	if strings.TrimSpace(resp.ToolCalls[0].ID) == "" {
		t.Errorf("empty tool_use id must be synthesized")
	}
}
