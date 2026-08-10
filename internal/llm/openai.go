package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// openaiProvider speaks the OpenAI /v1/chat/completions schema with native
// tool-calling. One implementation covers OpenAI, OpenRouter, Groq, Together,
// Ollama, LM Studio, and any other OpenAI-compatible endpoint via base_url.
type openaiProvider struct {
	name    string
	apiKey  string
	baseURL string
	model   string
	client  *http.Client
}

// newOpenAIProvider builds the Factory registered for "openai"/"openrouter"/
// "generic". New() already resolves cfg.BaseURL to the registry default (or
// errors if there is none — "generic" has none, so it always requires an
// explicit base_url) before calling this, so the factory can assume
// cfg.BaseURL is set.
func newOpenAIProvider() Factory {
	return func(cfg Config) (Provider, error) {
		timeout := cfg.Timeout
		if timeout <= 0 {
			timeout = 60 * time.Second
		}
		return &openaiProvider{
			name:    cfg.Provider,
			apiKey:  cfg.APIKey,
			baseURL: strings.TrimRight(cfg.BaseURL, "/"),
			model:   cfg.Model,
			client:  &http.Client{Timeout: timeout},
		}, nil
	}
}

func (p *openaiProvider) Name() string { return p.name }

func (p *openaiProvider) Complete(ctx context.Context, req Request) (*Response, error) {
	model := req.Model
	if model == "" {
		model = p.model
	}
	if model == "" {
		return nil, fmt.Errorf("llm: no model configured")
	}

	body := p.buildBody(model, req)
	headers := map[string]string{
		"Authorization": "Bearer " + p.apiKey,
	}
	// OpenRouter accepts extra headers; harmless to others.
	if p.name == "openrouter" {
		headers["HTTP-Referer"] = "https://rookery.cloud"
		headers["X-Title"] = "Rookery"
	}

	url := p.baseURL + "/chat/completions"
	respBody, code, err := doJSON(ctx, p.client, http.MethodPost, url, headers, body)
	if err != nil {
		// A 400 mentioning "tool"/"function" usually means the model doesn't support
		// function-calling — degrade to a no-tool turn rather than failing hard.
		// Mirrors anthropicProvider.Complete's classification of the same condition.
		if code == 400 && len(req.Tools) > 0 {
			lower := strings.ToLower(string(respBody))
			if strings.Contains(lower, "tool") || strings.Contains(lower, "function") {
				return nil, ErrToolsUnsupported
			}
		}
		return nil, err
	}
	return parseOpenAIResponse(respBody)
}

func (p *openaiProvider) buildBody(model string, req Request) []byte {
	type fnTool struct {
		Type     string `json:"type"`
		Function struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"function"`
	}
	type msg struct {
		Role       string           `json:"role"`
		Content    string           `json:"content,omitempty"`
		ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
		ToolCallID string           `json:"tool_call_id,omitempty"`
		Name       string           `json:"name,omitempty"`
	}
	var msgs []msg
	if req.System != "" {
		msgs = append(msgs, msg{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		var tc []openAIToolCall
		for _, c := range m.ToolCalls {
			tc = append(tc, openAIToolCall{ID: c.ID, Type: "function", Function: openAIToolFn{Name: c.Name, Arguments: string(c.Args)}, ExtraContent: c.Extra})
		}
		content := m.Content
		// A tool-result message MUST carry a content field. `content,omitempty` drops an
		// empty string, and stricter OpenAI-compatible APIs (Mistral) then reject the tool
		// message with HTTP 422 ("content: Field required"). Force a non-empty placeholder
		// so the field is always serialized. (The engine already normalizes empty tool
		// results; this is defense-in-depth for any other call path — e.g. replayed history.)
		if m.Role == "tool" && strings.TrimSpace(content) == "" {
			content = "(no output)"
		}
		// An assistant message with NEITHER content NOR tool calls serializes as a bare
		// {"role":"assistant"} (content dropped by omitempty) — which some OpenAI-compatible
		// endpoints reject with a 400. This happens when the model returns an empty final
		// answer (e.g. on the verify-finish nudge branch). Force a placeholder so the message
		// is always well-formed. (Assistant + tool_calls with empty content is valid — leave it.)
		if m.Role == "assistant" && strings.TrimSpace(content) == "" && len(tc) == 0 {
			content = "(no content)"
		}
		msgs = append(msgs, msg{Role: m.Role, Content: content, ToolCalls: tc, ToolCallID: m.ToolCallID, Name: m.Name})
	}

	payload := map[string]any{
		"model":       model,
		"messages":    msgs,
		"max_tokens":  orDefault(req.MaxTokens, 4096),
		"temperature": 0,
	}
	if len(req.Tools) > 0 {
		tools := make([]fnTool, 0, len(req.Tools))
		for _, t := range req.Tools {
			ft := fnTool{Type: "function"}
			ft.Function.Name = t.Name
			ft.Function.Description = t.Description
			ft.Function.Parameters = t.Parameters
			tools = append(tools, ft)
		}
		payload["tools"] = tools
		payload["tool_choice"] = "auto"
	}
	b, _ := json.Marshal(payload)
	return b
}

type openAIToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function openAIToolFn `json:"function"`
	// ExtraContent is a non-standard passthrough field: Gemini 3 returns a required
	// `thought_signature` here and demands it back verbatim on the next turn. Carried
	// on both the response (parse) and the request (replay). `omitempty` keeps it off
	// providers that never send it.
	ExtraContent json.RawMessage `json:"extra_content,omitempty"`
}
type openAIToolFn struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // OpenAI sends arguments as a JSON string
}

type openAIResponse struct {
	Choices []struct {
		Message      openAIChoiceMessage `json:"message"`
		FinishReason string              `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type openAIChoiceMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	ToolCalls []openAIToolCall `json:"tool_calls"`
}

func parseOpenAIResponse(data []byte) (*Response, error) {
	var r openAIResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("llm: parse openai response: %w (body: %s)", err, snippet(data))
	}
	if len(r.Choices) == 0 {
		return &Response{Usage: Usage{PromptTokens: r.Usage.PromptTokens, CompletionTokens: r.Usage.CompletionTokens, TotalTokens: r.Usage.TotalTokens}}, nil
	}
	ch := r.Choices[0]
	resp := &Response{
		Content:      ch.Message.Content,
		FinishReason: ch.FinishReason,
		Usage:        Usage{PromptTokens: r.Usage.PromptTokens, CompletionTokens: r.Usage.CompletionTokens, TotalTokens: r.Usage.TotalTokens},
	}
	for i, tc := range ch.Message.ToolCalls {
		args := json.RawMessage(tc.Function.Arguments)
		if len(args) == 0 {
			args = json.RawMessage("{}")
		}
		// Some OpenAI-compatible providers (seen on Mistral) occasionally return a tool call
		// with an empty id. The engine echoes that id back as the tool-result's
		// `tool_call_id`, where `omitempty` would then drop it — and OpenAI/Mistral REQUIRE
		// `tool_call_id` on role:"tool" messages, so the follow-up request 400/422s. Synthesize
		// a stable id so both the assistant tool_call and its result carry the same non-empty id.
		id := tc.ID
		if strings.TrimSpace(id) == "" {
			id = fmt.Sprintf("call_%d", i)
		}
		resp.ToolCalls = append(resp.ToolCalls, ToolCall{ID: id, Name: tc.Function.Name, Args: args, Extra: tc.ExtraContent})
	}
	return resp, nil
}

func orDefault(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}

func init() {
	factory := newOpenAIProvider()
	for _, name := range []string{
		"openai", "openrouter", "generic", // no registry default for generic — New() requires an explicit base_url
		"zai", "ollama", "ollama_local", "deepseek", "groq", "xai",
		"mistral", "gemini", "opencode_zen", "opencode_go", "perplexity", "moonshot",
		// Wave 1 (2026-08) — hosted tier.
		"bedrock", "alibaba", "together", "fireworks", "cerebras", "sambanova",
		"nebius", "deepinfra", "huggingface", "github_models",
		// Wave 1 (2026-08) — local tier (self-hosted OpenAI-compatible servers).
		"lmstudio", "llamacpp", "vllm", "localai", "jan",
	} {
		RegisterProvider(name, factory)
	}
}
