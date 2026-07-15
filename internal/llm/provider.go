// Package llm provides direct LLM provider HTTP transport — a thin, reusable
// abstraction over chat-completion / messages APIs with native function-calling
// (tool use). It is used by the coder package's "api" engine to drive a workspace
// whose coder_kind is "api" (OpenAI, OpenRouter, Anthropic, any OpenAI-compatible
// endpoint) instead of a host CLI binary.
//
// The package deliberately knows nothing about vaults, sandboxes, or the agent
// output protocol — it only turns a []Message + []Tool into a Response. The
// agentic tool-calling loop that consumes this lives in internal/coder.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Sentinel errors. Providers map transport-level failures onto these so the
// coder engine can surface them uniformly (rate limits retry / defer, auth is a
// config error, unsupported-tools degrades to a no-tool turn).
var (
	// ErrRateLimit is a TRANSIENT 429: the provider throttled the request
	// (per-second/per-minute RPM or TPM window). It clears on its own after a
	// short wait, so callers retry with backoff — it is NOT a quota exhaustion.
	ErrRateLimit = errors.New("llm: transient rate limit reached")
	// ErrQuotaExhausted is a 402 / payment-required: the account is out of
	// credits or has exhausted its period quota. Not transient — do not retry;
	// surface as a usage-limit condition to the user.
	ErrQuotaExhausted   = errors.New("llm: quota/credits exhausted")
	ErrAuth             = errors.New("llm: authentication failed")
	ErrToolsUnsupported = errors.New("llm: model does not support tools")
)

// Config is the per-coder provider configuration resolved at run time.
type Config struct {
	Provider string        // registry name: "openai", "openrouter", "anthropic", "generic"
	APIKey   string        // resolved provider API key (from the secrets store)
	BaseURL  string        // optional override; provider default when empty
	Model    string        // model id, e.g. "gpt-4o", "anthropic/claude-3.5-sonnet"
	Timeout  time.Duration // per-request timeout
}

// Provider abstracts a single LLM completion round-trip with native tool-calling.
// Implementations are stateless and safe for concurrent use.
type Provider interface {
	// Name is the registry name ("openai", "anthropic", …).
	Name() string
	// Complete performs one completion round-trip. A response with ToolCalls
	// non-empty means the model wants tools executed; the caller appends tool
	// results and calls Complete again. A response with no ToolCalls is the
	// model's final answer (Response.Content).
	Complete(ctx context.Context, req Request) (*Response, error)
}

// Request is a single completion request.
type Request struct {
	Model     string
	System    string    // system prompt (top-level for Anthropic, first message for OpenAI)
	Messages  []Message // conversation turns, excluding the system prompt
	Tools     []Tool    // empty/nil = no tools offered (single reasoning turn)
	MaxTokens int       // 0 = provider default
}

// Message is one conversational turn in a provider-agnostic shape.
type Message struct {
	Role       string     // "user", "assistant", "tool"
	Content    string     // text content (for role "tool": the tool result text)
	ToolCalls  []ToolCall // set on assistant messages that requested tool execution
	ToolCallID string     // for role "tool": the id of the ToolCall this answers
	Name       string     // for role "tool": the tool name that produced this result
}

// Tool describes one host-executed function the model may call.
type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage // JSON Schema for the tool's arguments
}

// ToolCall is a model-requested tool invocation.
type ToolCall struct {
	ID   string
	Name string
	Args json.RawMessage // raw JSON object (the model's arguments)
}

// Response is one completion result.
type Response struct {
	Content      string // assistant text; may be empty when the model only emitted tool calls
	ToolCalls    []ToolCall
	Usage        Usage
	FinishReason string // "stop", "tool_calls"/"tool_use", "length", …
}

// Usage is a best-effort token accounting (zero values are fine).
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// ─── Registry ────────────────────────────────────────────────────────────────

// A Factory builds a Provider from a resolved Config. Providers register one
// factory per name in their init(); the coder engine picks one by Config.Provider.
type Factory func(cfg Config) (Provider, error)

var (
	providersMu  = make(map[string]Factory)
	defaultBases = map[string]string{
		"openai":       "https://api.openai.com/v1",
		"openrouter":   "https://openrouter.ai/api/v1",
		"anthropic":    "https://api.anthropic.com",
		"zai":          "https://api.z.ai/api/openai/v1",
		"ollama":       "https://ollama.com/v1",
		"ollama_local": "http://localhost:11434/v1",
		"deepseek":     "https://api.deepseek.com",
		"groq":         "https://api.groq.com/openai/v1",
		"xai":          "https://api.x.ai/v1",
		"mistral":      "https://api.mistral.ai/v1",
		"gemini":       "https://generativelanguage.googleapis.com/v1beta/openai/",
		"opencode_zen": "https://opencode.ai/zen/v1",
		"opencode_go":  "https://opencode.ai/zen/go/v1",
		"perplexity":   "https://api.perplexity.ai",
		"moonshot":     "https://api.moonshot.ai/v1",
	}
)

// RegisterProvider registers a provider factory under name. Called from init()
// in each provider file. Adding a new provider is one file + one registration.
func RegisterProvider(name string, f Factory) {
	providersMu[name] = f
}

// DefaultBaseURL returns the registered default endpoint for a provider name,
// or "" if the provider has no default (e.g. "generic", which requires an
// explicit base URL). The web layer uses this to prefill the base-URL field.
func DefaultBaseURL(name string) string {
	return defaultBases[name]
}

// New builds the named provider from cfg. Returns a helpful error if the name
// is unknown or the API key is missing.
func New(cfg Config) (Provider, error) {
	if cfg.Provider == "" {
		return nil, fmt.Errorf("llm: empty provider name")
	}
	f, ok := providersMu[cfg.Provider]
	if !ok {
		return nil, fmt.Errorf("llm: unknown provider %q (registered: %s)", cfg.Provider, registeredNames())
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("llm: %s provider requires an API key (coder_api_key_secret not set or empty)", cfg.Provider)
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBases[cfg.Provider]
	}
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("llm: %s provider requires a base_url", cfg.Provider)
	}
	return f(cfg)
}

func registeredNames() string {
	names := make([]string, 0, len(providersMu))
	for k := range providersMu {
		names = append(names, k)
	}
	return strings.Join(names, ", ")
}

// ─── Shared HTTP plumbing ─────────────────────────────────────────────────────

// doJSON sends a JSON body to url with the given headers and returns the response.
// It retries transient failures (429 rate limits, 5xx, network) with backoff
// that respects Retry-After and grows long enough to clear a per-minute rate
// window — a 429 from a provider like Mistral's free tier is a transient RPM/TPM
// throttle, NOT quota exhaustion, so giving up after a couple of seconds (as the
// old 3-attempt loop did) fails runs that would succeed after a short wait.
// Quota exhaustion (402) and auth errors (401/403) are NOT retried. The response
// body + status are returned alongside the error so callers can classify
// ambiguous 400s (e.g. Anthropic → ErrToolsUnsupported).
func doJSON(ctx context.Context, client *http.Client, method, url string, headers map[string]string, body []byte) ([]byte, int, error) {
	const maxAttempts = 7
	var lastErr error
	var lastCode int
	var lastBody []byte
	var retryAfter time.Duration
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			backoff := retryAfter
			if backoff <= 0 {
				backoff = rateLimitBackoff(attempt)
			}
			if !sleep(ctx, backoff) {
				return nil, 0, ctx.Err()
			}
			retryAfter = 0
		}
		code, respBody, retryAfterHdr, err := doOnce(ctx, client, method, url, headers, body)
		if err != nil {
			lastErr = err
			continue // network error → retry
		}
		lastCode, lastBody = code, respBody
		switch {
		case code >= 200 && code < 300:
			// A chat-completion 2xx MUST carry a JSON body. Some OpenAI-compatible
			// providers (seen on OpenRouter) occasionally return 200 OK with an EMPTY
			// body — an upstream/transport hiccup, not a real completion. Treat that as
			// transient and retry within this loop, instead of handing an empty body to
			// the parser (which fails with an opaque "unexpected end of JSON input"). If
			// it persists across the attempt budget, surface an explicit "empty response"
			// error below rather than a parse error.
			if len(strings.TrimSpace(string(respBody))) == 0 {
				lastErr = fmt.Errorf("llm: empty response body (status %d)", code)
				continue
			}
			return respBody, code, nil
		case code == 429:
			// Transient throttle (RPM/TPM window). Retry with backoff; only
			// surface ErrRateLimit if we exhaust the attempt budget.
			retryAfter = parseRetryAfter(retryAfterHdr)
			lastErr = ErrRateLimit
			continue
		case code == 402:
			// Payment required / credits exhausted — not transient. Don't retry.
			return respBody, code, fmt.Errorf("%w: 402 %s", ErrQuotaExhausted, snippet(respBody))
		case code == 401 || code == 403:
			return respBody, code, fmt.Errorf("%w: %d %s", ErrAuth, code, snippet(respBody))
		case code == 400:
			// Ambiguous: may be "tools not supported", bad model, etc. Return the
			// body so the caller can classify (e.g. Anthropic → ErrToolsUnsupported).
			return respBody, code, fmt.Errorf("llm: bad request %d: %s", code, snippet(respBody))
		case code >= 500:
			lastErr = fmt.Errorf("llm: server error %d: %s", code, snippet(respBody))
			continue
		default:
			return respBody, code, fmt.Errorf("llm: unexpected status %d: %s", code, snippet(respBody))
		}
	}
	if errors.Is(lastErr, ErrRateLimit) {
		slog.Error("llm rate-limited after retries", "url", url, "req_size", len(body), "status", lastCode, "body", snippet(lastBody))
		return lastBody, lastCode, ErrRateLimit
	}
	if lastErr == nil {
		lastErr = errors.New("llm: request failed")
	}
	return lastBody, lastCode, lastErr
}

func doOnce(ctx context.Context, client *http.Client, method, url string, headers map[string]string, body []byte) (int, []byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(string(body)))
	if err != nil {
		return 0, nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB cap
	return resp.StatusCode, data, resp.Header.Get("Retry-After"), nil
}

// sleep returns false if the context was cancelled while waiting.
func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

// parseRetryAfter parses a Retry-After header (seconds or HTTP-date). Returns 0
// if it cannot be parsed.
func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		return time.Duration(secs) * time.Second
	}
	return 0
}

// rateLimitBackoff is the default backoff for a transient 429 when the provider
// sends no Retry-After. The schedule (1s, 2s, 5s, 10s, 20s, 30s) is deliberately
// long enough to cross a per-minute rate window: providers like Mistral's free
// tier throttle on a rolling 1-minute RPM/TPM budget, so a sub-second burst of
// retries (the old behaviour) always fails, while waiting out the window almost
// always recovers. attempt is 1-based (the first retry is attempt 1).
func rateLimitBackoff(attempt int) time.Duration {
	switch attempt {
	case 1:
		return 1 * time.Second
	case 2:
		return 2 * time.Second
	case 3:
		return 5 * time.Second
	case 4:
		return 10 * time.Second
	case 5:
		return 20 * time.Second
	default:
		return 30 * time.Second
	}
}
