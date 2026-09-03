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
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
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
	// ErrEmptyResponse is a 2xx carrying no body at all, repeated until the
	// retry budget was spent. An upstream/transport failure at the provider,
	// NOT anything about the request: the run never reached a model, so it has
	// no tokens, no tool calls and no partial work.
	//
	// Typed because it is the one transient failure that reached the user as a
	// raw internal string — every other one (rate limit, quota, auth) had a
	// plain-English message and this fell through to err.Error(). "llm: empty
	// response body (status 200)" tells someone whose agent just burned ten
	// minutes nothing they can act on, when the true advice is simply to run it
	// again.
	ErrEmptyResponse = errors.New("llm: provider returned an empty response")
	// ErrUnreachable is a TERMINAL transport failure: the request never reached
	// a server that could answer it, and waiting will not change that. A refused
	// dial (nothing listening — a local Ollama that is not running), a hostname
	// that does not resolve, or a certificate that does not verify.
	//
	// Typed because it used to be the loudest silent failure in the product.
	// doJSON treated EVERY network error as transient, so a dead local provider
	// spent the whole seven-attempt ladder — about 68 seconds of backoff — and
	// then returned a raw *url.Error that no downstream classifier recognised,
	// which every surface rendered as its generic "see the server log" arm. From
	// the outside that is a chat window that hangs for over a minute and then
	// says nothing. Nothing about a refused dial improves after a 30-second
	// wait, so these three fail on the first attempt with a message that names
	// the endpoint.
	ErrUnreachable = errors.New("llm: provider unreachable")
)

// terminalTransportErr reports whether a transport failure is one that waiting
// cannot fix, so doJSON should give up immediately rather than spend the retry
// ladder on it.
//
// It deliberately fails OPEN: anything not recognised here keeps the existing
// retry behaviour. A false negative therefore costs latency (the pre-existing
// behaviour) while a false positive would cost a turn that might have succeeded,
// so the list stays narrow and only holds failures that are terminal by
// construction.
//
// The accepted cost, recorded rather than engineered around: a hosted provider
// behind a load balancer that refuses a single dial now fails that request
// instead of recovering on the next attempt. That is rare, and it fails in a
// second with an explanation rather than after a silent minute without one.
func terminalTransportErr(err error) bool {
	if err == nil {
		return false
	}
	// Nothing is listening on the port. The single most common cause on a
	// self-hosted install: a local model server that is not running. The check
	// is per-platform (connrefused_unix.go / connrefused_windows.go) because the
	// portable-looking spelling silently never matches on Windows.
	if isConnRefused(err) {
		return true
	}
	// The hostname does not exist. A typo in a base URL, not an outage —
	// IsNotFound is NXDOMAIN specifically, so a temporary resolver failure
	// (IsTemporary) still falls through to the retry ladder.
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return true
	}
	// The TLS handshake produced a certificate the client will not accept.
	// Retrying re-runs an identical handshake and gets an identical answer.
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameErr x509.HostnameError
	var certInvalid x509.CertificateInvalidError
	var verifyErr *tls.CertificateVerificationError
	return errors.As(err, &unknownAuthority) ||
		errors.As(err, &hostnameErr) ||
		errors.As(err, &certInvalid) ||
		errors.As(err, &verifyErr)
}

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
	// Extra is opaque provider-specific passthrough attached to a tool call that
	// MUST be echoed back verbatim when the call is replayed in later turns.
	// Gemini 3 puts a required `thought_signature` here (the OpenAI-compat
	// `tool_calls[N].extra_content` object); dropping it makes Gemini reject the
	// next turn with a 400. Empty for providers that don't use it (OpenAI, Mistral).
	Extra json.RawMessage `json:"-"`
}

// Response is one completion result.
type Response struct {
	Content   string // assistant text; may be empty when the model only emitted tool calls
	ToolCalls []ToolCall
	Usage     Usage
	// FinishReason is "stop", "tool_calls"/"tool_use", "length", … A "length"
	// carrying empty Content is a TRUNCATION, not an empty answer, and the two
	// need opposite handling — see the empty-completion branch in
	// internal/coder/api_engine.go.
	FinishReason string
	// Reasoning is a reasoning model's thinking, which providers return OUTSIDE
	// Content. It is captured for DIAGNOSIS ONLY and must never be delivered to a
	// user or fed back as an answer: it is mid-thought on a truncated turn, and
	// this repo has already shipped model internals to a real user twice (see
	// chat.CleanReply and coder.LooksLikeToolScaffolding).
	//
	// Without it, a run whose whole output budget went to reasoning was
	// indistinguishable from a model that returned nothing at all — which is
	// exactly how one agent was misdiagnosed as a large-file problem four times.
	Reasoning string
}

// Usage is a best-effort token accounting (zero values are fine).
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	// CachedTokens is the part of PromptTokens the provider served from its
	// prompt cache, and is the only way to tell a run that re-sent the same
	// bytes cheaply from one that paid for them in full.
	//
	// It matters here more than it would elsewhere: the tool loop is strictly
	// append-only with the system prompt set once, so every turn resends a
	// byte-identical prefix — measured at 33 KB of skills, memory and AGENT.md
	// before the static blocks. Whether that prefix is cached changes the cost
	// of a run by roughly an order of magnitude, and nothing recorded it.
	CachedTokens int
	// CacheReported says the provider actually told us. It is NOT redundant
	// with CachedTokens > 0: "the provider reports zero cache hits" and "this
	// provider does not report cache statistics" are opposite diagnoses — the
	// first says caching is broken and worth fixing, the second says the
	// measurement is unavailable and the question is still open. Collapsing
	// them into a bare zero would make an unanswerable case look like a
	// finding.
	CacheReported bool
	// Cost is what the provider says this call cost, in USD. OpenRouter reports
	// it on every response; providers that bill separately (Anthropic) do not,
	// and a CLI coder runs a subprocess with no usage accounting at all.
	//
	// Taken from the provider rather than computed from a price table, because a
	// table is a second copy of someone else's pricing and goes stale in
	// silence — yielding a number that looks authoritative and is wrong.
	Cost float64
	// CostReported distinguishes "this cost nothing" from "nobody told us what
	// it cost", for the same reason CacheReported exists: an unreported cost
	// rendered as $0.00 reads as free.
	CostReported bool
}

// Add sums two accountings.
//
// This is the ONE place usage is summed, and it exists because there were two.
// internal/coder summed the provider's per-call usage; internal/agentrunner
// summed the per-turn results — and the second enumerated three fields, so it
// silently discarded everything the first learned about. CachedTokens and
// CacheReported were parsed correctly, carried correctly out of the engine, and
// dropped one layer up, so the run log said "n/a" on a provider that reports
// cache statistics on every single response. Adding Cost the same way would
// have reproduced it exactly.
//
// Counts add; the reported-flags OR, because one call reporting is enough to
// make a run's number meaningful, and requiring every call to report would
// erase a real measurement whenever a single response omitted the field.
func (u Usage) Add(b Usage) Usage {
	return Usage{
		PromptTokens:     u.PromptTokens + b.PromptTokens,
		CompletionTokens: u.CompletionTokens + b.CompletionTokens,
		TotalTokens:      u.TotalTokens + b.TotalTokens,
		CachedTokens:     u.CachedTokens + b.CachedTokens,
		CacheReported:    u.CacheReported || b.CacheReported,
		Cost:             u.Cost + b.Cost,
		CostReported:     u.CostReported || b.CostReported,
	}
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

		// ── Wave 1 (2026-08) hosted tier ──
		// Bedrock: AWS documents three path shapes; this is the `bedrock-mantle`
		// endpoint AWS explicitly recommends, and the only one that accepts a
		// Bedrock API key as a plain bearer token with no SigV4 signing — which
		// is the whole reason Bedrock is a drop-in here. The region is baked to
		// us-east-1; another region goes in the per-workspace base-URL override.
		"bedrock":       "https://bedrock-mantle.us-east-1.api.aws/v1",
		"alibaba":       "https://dashscope-intl.aliyuncs.com/compatible-mode/v1",
		"together":      "https://api.together.xyz/v1",
		"fireworks":     "https://api.fireworks.ai/inference/v1",
		"cerebras":      "https://api.cerebras.ai/v1",
		"sambanova":     "https://api.sambanova.ai/v1",
		"nebius":        "https://api.studio.nebius.com/v1",
		"deepinfra":     "https://api.deepinfra.com/v1/openai",
		"huggingface":   "https://router.huggingface.co/v1",
		"github_models": "https://models.github.ai/inference",

		// ── Wave 1 (2026-08) local tier ──
		// Each is the default its own docs publish. llamacpp and localai share
		// 8080: they are alternative servers, not concurrent ones, and a user
		// running both overrides one — the workflow the base-URL prefill makes
		// discoverable.
		"lmstudio": "http://localhost:1234/v1",
		"llamacpp": "http://localhost:8080/v1",
		"vllm":     "http://localhost:8000/v1",
		"localai":  "http://localhost:8080/v1",
		"jan":      "http://localhost:1337/v1",

		// ── Wave 2 (2026-08) hosted tier ──
		// Each confirmed against the provider's own current documentation, not
		// carried forward from memory: a wrong base URL yields a provider that
		// appears in the picker, accepts a key, and cannot answer.
		// Cohere serves its OpenAI-shaped API on a /compatibility prefix rather
		// than /v1, and MiniMax splits by region — api.minimax.io is the
		// INTERNATIONAL host; mainland China accounts use api.minimaxi.com and
		// go through the per-workspace base-URL override.
		"cohere":     "https://api.cohere.ai/compatibility/v1",
		"nvidia":     "https://integrate.api.nvidia.com/v1",
		"vercel_ai":  "https://ai-gateway.vercel.sh/v1",
		"minimax":    "https://api.minimax.io/v1",
		"baseten":    "https://inference.baseten.co/v1",
		"novita":     "https://api.novita.ai/openai/v1",
		"hyperbolic": "https://api.hyperbolic.xyz/v1",
		"venice":     "https://api.venice.ai/api/v1",
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
			// A terminal transport failure returns NOW. The ladder below exists
			// to wait out a throttle or an upstream blip; there is no wait that
			// makes a refused dial or an unresolvable host succeed, and spending
			// it is what turned "the model server is off" into a minute of
			// silence. Anything unrecognised still falls through and retries.
			if terminalTransportErr(err) {
				return nil, 0, fmt.Errorf("%w: %v", ErrUnreachable, err)
			}
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
				lastErr = fmt.Errorf("%w (status %d)", ErrEmptyResponse, code)
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
	if errors.Is(lastErr, ErrEmptyResponse) {
		// Logged at the same level as the rate-limit exhaustion: seven empty
		// bodies in a row is a provider outage window, and the run it killed
		// leaves no other trace — no tokens, no tool calls, nothing to read back.
		slog.Error("llm returned an empty body on every attempt",
			"url", url, "req_size", len(body), "status", lastCode, "attempts", maxAttempts)
		return lastBody, lastCode, ErrEmptyResponse
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
