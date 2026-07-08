package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// anthropicProvider speaks the Anthropic Messages API (/v1/messages) with native
// tool_use / tool_result blocks. The message-shape translation (tool result as a
// user role block, tool_use on assistant) is encapsulated here so the engine
// deals only with the generic []Message model.
type anthropicProvider struct {
	name    string
	apiKey  string
	baseURL string
	model   string
	client  *http.Client
}

// newAnthropicProvider builds the Factory registered for "anthropic". New()
// already resolves cfg.BaseURL to the registry default (or errors if there is
// none) before calling this, so the factory can assume cfg.BaseURL is set.
func newAnthropicProvider() Factory {
	return func(cfg Config) (Provider, error) {
		timeout := cfg.Timeout
		if timeout <= 0 {
			timeout = 60 * time.Second
		}
		return &anthropicProvider{
			name:    cfg.Provider,
			apiKey:  cfg.APIKey,
			baseURL: strings.TrimRight(cfg.BaseURL, "/"),
			model:   cfg.Model,
			client:  &http.Client{Timeout: timeout},
		}, nil
	}
}

func (p *anthropicProvider) Name() string { return p.name }

// anthropicContent is a polymorphic content block: text, tool_use, or tool_result.
type anthropicContent struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"` // tool_result content (string or block array)
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// anthropicMessage is one wire-format turn (either role, one or more content blocks).
type anthropicMessage struct {
	Role    string             `json:"role"`
	Content []anthropicContent `json:"content"`
}

func (p *anthropicProvider) Complete(ctx context.Context, req Request) (*Response, error) {
	model := req.Model
	if model == "" {
		model = p.model
	}
	if model == "" {
		return nil, fmt.Errorf("llm: no model configured")
	}

	body := p.buildBody(model, req)
	headers := map[string]string{
		"x-api-key":         p.apiKey,
		"anthropic-version": "2023-06-01",
	}
	url := p.baseURL + "/v1/messages"
	respBody, code, err := doJSON(ctx, p.client, http.MethodPost, url, headers, body)
	if err != nil {
		// A 400 mentioning "tool" usually means the model doesn't support
		// function-calling — degrade to a no-tool turn rather than failing hard.
		if code == 400 && strings.Contains(strings.ToLower(string(respBody)), "tool") {
			return nil, ErrToolsUnsupported
		}
		return nil, err
	}
	return parseAnthropicResponse(respBody)
}

func (p *anthropicProvider) buildBody(model string, req Request) []byte {
	var msgs []anthropicMessage
	for _, m := range req.Messages {
		switch m.Role {
		case "user":
			block := anthropicContent{Type: "text", Text: m.Content}
			// Anthropic rejects two consecutive user-role messages. Coalesce a user turn
			// that follows another user turn into that message as an extra block. The
			// concrete case: the turn-budget grace nudge is a plain user message appended
			// right after tool_result blocks (which serialize as a user message) — without
			// this it would be a second consecutive user message and 400 the fallback call.
			if n := len(msgs); n > 0 && msgs[n-1].Role == "user" {
				msgs[n-1].Content = append(msgs[n-1].Content, block)
			} else {
				msgs = append(msgs, anthropicMessage{Role: "user", Content: []anthropicContent{block}})
			}
		case "assistant":
			blocks := []anthropicContent{}
			if m.Content != "" {
				blocks = append(blocks, anthropicContent{Type: "text", Text: m.Content})
			}
			for _, c := range m.ToolCalls {
				args := c.Args
				if len(args) == 0 {
					args = json.RawMessage("{}")
				}
				blocks = append(blocks, anthropicContent{Type: "tool_use", ID: c.ID, Name: c.Name, Input: args})
			}
			// An assistant turn with no blocks at all (empty content, no tool calls) makes an
			// empty content array, which Anthropic rejects. Emit a minimal text block so an
			// empty final answer (e.g. on the verify-finish nudge branch) can't 400 the call.
			if len(blocks) == 0 {
				blocks = append(blocks, anthropicContent{Type: "text", Text: "(no content)"})
			}
			msgs = append(msgs, anthropicMessage{Role: "assistant", Content: blocks})
		case "tool":
			// Anthropic requires strict user/assistant role alternation and rejects
			// consecutive user-role messages. The engine emits one Message per tool
			// call executed in a turn, so a turn with multiple tool calls produces
			// multiple consecutive "tool" entries here — coalesce them into a single
			// user-role message with one tool_result block each, rather than one user
			// message per result (which would violate the alternation requirement).
			block := anthropicContent{Type: "tool_result", ToolUseID: m.ToolCallID, Content: jsonString(m.Content)}
			if n := len(msgs); n > 0 && isToolResultMessage(msgs[n-1]) {
				msgs[n-1].Content = append(msgs[n-1].Content, block)
			} else {
				msgs = append(msgs, anthropicMessage{Role: "user", Content: []anthropicContent{block}})
			}
		}
	}

	payload := map[string]any{
		"model":      model,
		"max_tokens": orDefault(req.MaxTokens, 4096),
		"messages":   msgs,
	}
	if req.System != "" {
		payload["system"] = req.System
	}
	if len(req.Tools) > 0 {
		tools := make([]anthropicTool, 0, len(req.Tools))
		for _, t := range req.Tools {
			schema := t.Parameters
			if len(schema) == 0 {
				schema = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			tools = append(tools, anthropicTool{Name: t.Name, Description: t.Description, InputSchema: schema})
		}
		payload["tools"] = tools
	}
	b, _ := json.Marshal(payload)
	return b
}

// jsonString wraps a plain string as a JSON string literal (for tool_result.content).
func jsonString(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return json.RawMessage(b)
}

// isToolResultMessage reports whether msg is a user-role message made up
// entirely of tool_result blocks, i.e. it's safe to append another tool_result
// block to it rather than starting a new message (which would produce two
// consecutive user-role messages and violate Anthropic's strict alternation).
func isToolResultMessage(msg anthropicMessage) bool {
	if msg.Role != "user" || len(msg.Content) == 0 {
		return false
	}
	for _, b := range msg.Content {
		if b.Type != "tool_result" {
			return false
		}
	}
	return true
}

type anthropicResponse struct {
	Content    []anthropicContent `json:"content"`
	StopReason string             `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func parseAnthropicResponse(data []byte) (*Response, error) {
	var r anthropicResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("llm: parse anthropic response: %w (body: %s)", err, snippet(data))
	}
	resp := &Response{
		FinishReason: r.StopReason,
		Usage:        Usage{PromptTokens: r.Usage.InputTokens, CompletionTokens: r.Usage.OutputTokens, TotalTokens: r.Usage.InputTokens + r.Usage.OutputTokens},
	}
	for _, b := range r.Content {
		switch b.Type {
		case "text":
			resp.Content += b.Text
		case "tool_use":
			args := b.Input
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			// Defensive: Anthropic normally always returns a tool_use id, but a missing one
			// would be echoed back as an empty tool_result.tool_use_id and rejected. Synthesize
			// a stable id so the assistant tool_use and its result always carry the same value.
			id := b.ID
			if strings.TrimSpace(id) == "" {
				id = fmt.Sprintf("call_%d", len(resp.ToolCalls))
			}
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{ID: id, Name: b.Name, Args: args})
		}
	}
	return resp, nil
}

func init() {
	RegisterProvider("anthropic", newAnthropicProvider())
}
