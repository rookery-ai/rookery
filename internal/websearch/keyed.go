package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ilijad1/rookery/internal/nethttp"
)

// ErrInvalidKey means the provider rejected the credential itself (401/403).
// It is separated from every other failure because it is the one the user can
// actually fix, and because retrying it is pointless: a wrong key stays wrong.
// At search time it still falls through to the next provider (search should
// degrade, not fail); at key-save time it is the signal to refuse the key.
var ErrInvalidKey = errors.New("invalid api key")

// verifyQuery is the throwaway query Verify sends. Its content is irrelevant —
// Verify is testing whether the credential is accepted, not what it returns, so
// a 200 carrying zero results still means the key works.
const verifyQuery = "example"

// Verify checks one provider's credential with a single request. It returns
// nil when the credential is accepted, ErrInvalidKey when the provider rejects
// it, and the underlying error otherwise ("could not check right now").
//
// It deliberately does NOT retry. The caller needs to distinguish "rejected"
// from "could not check", and a retry loop only makes an interactive settings
// save slower without changing which of those two answers it gets.
func Verify(ctx context.Context, hc *http.Client, p Provider) error {
	if p == nil {
		return fmt.Errorf("no provider configured")
	}
	if hc == nil {
		hc = nethttp.GuardedClient(10 * time.Second)
	}
	_, err := p.Search(ctx, hc, verifyQuery)
	return err
}

// Production endpoints for the keyed providers.
const (
	braveEndpoint  = "https://api.search.brave.com/res/v1/web/search"
	tavilyEndpoint = "https://api.tavily.com/search"
)

// KeySecretNames are the secret names a workspace can set to upgrade search
// from scraping to a real API. They are stored as ordinary encrypted secrets,
// mirroring how coder provider keys use the CODER_KEY_<PROVIDER> convention.
func KeySecretNames() []string { return []string{"SEARCH_KEY_BRAVE", "SEARCH_KEY_TAVILY"} }

// SecretLookup resolves a single named secret for a workspace. Both chat
// surfaces (web's s.secretsLookup, the gateway's textHandler secretsLookup)
// already implement this exact signature for the API coder's provider key —
// ResolveKeyEnv reuses whichever one the caller has in hand.
type SecretLookup func(ctx context.Context, workspaceID, name string) (string, error)

// ResolveKeyEnv resolves the workspace's configured search API keys (if any)
// into an env map suitable for coder.WithExtraEnv, so a chat coder's
// searchProviders() picks up the same keyed provider agent runs already get.
// Only keys that are actually set are included — an unset key or a lookup
// error is treated as "not configured" (never a hard failure): search keys
// are a reliability upgrade for chat, not a dependency it should block on.
// This never exposes the key value to the model — the host reads it here to
// build the provider; the model calling web_search only sees results.
func ResolveKeyEnv(ctx context.Context, workspaceID string, lookup SecretLookup) map[string]string {
	env := map[string]string{}
	if lookup == nil {
		return env
	}
	for _, name := range KeySecretNames() {
		if v, err := lookup(ctx, workspaceID, name); err == nil && v != "" {
			env[name] = v
		}
	}
	return env
}

// KeyedProvider returns a provider for a supported keyed engine, or nil when no
// key is configured or the engine is unknown. Returning nil (rather than an
// erroring provider) means an unconfigured key simply leaves the keyless
// cascade in place.
func KeyedProvider(engine, apiKey, baseOverride string) Provider {
	if apiKey == "" {
		return nil
	}
	switch engine {
	case "brave":
		return &braveProvider{key: apiKey, base: orDefault(baseOverride, braveEndpoint)}
	case "tavily":
		return &tavilyProvider{key: apiKey, base: orDefault(baseOverride, tavilyEndpoint)}
	}
	return nil
}

func orDefault(override, prod string) string {
	if override != "" {
		return override
	}
	return prod
}

type braveProvider struct {
	key  string
	base string
}

func (p *braveProvider) Name() string { return "brave" }

func (p *braveProvider) Search(ctx context.Context, hc *http.Client, query string) ([]Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+"?q="+url.QueryEscape(query), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", p.key)
	data, err := doJSON(hc, req, p.key)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("brave: decode: %w", err)
	}
	out := make([]Result, 0, len(payload.Web.Results))
	for _, r := range payload.Web.Results {
		out = append(out, Result{Title: r.Title, URL: r.URL, Snippet: stripTags(r.Description)})
	}
	return out, nil
}

type tavilyProvider struct {
	key  string
	base string
}

func (p *tavilyProvider) Name() string { return "tavily" }

func (p *tavilyProvider) Search(ctx context.Context, hc *http.Client, query string) ([]Result, error) {
	body, _ := json.Marshal(map[string]any{"query": query, "max_results": 6})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.base, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.key)
	data, err := doJSON(hc, req, p.key)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("tavily: decode: %w", err)
	}
	out := make([]Result, 0, len(payload.Results))
	for _, r := range payload.Results {
		out = append(out, Result{Title: r.Title, URL: r.URL, Snippet: stripTags(r.Content)})
	}
	return out, nil
}

// doJSON performs the request and applies the same transient/definitive split
// the scraping providers use, so keyed engines participate in the retry loop
// identically. apiKey is redacted out of any error-body excerpt before it
// reaches the caller — providers must never be trusted not to echo it back
// (e.g. a validation error that mirrors the request body/headers).
func doJSON(hc *http.Client, req *http.Request, apiKey string) ([]byte, error) {
	resp, err := hc.Do(req)
	if err != nil {
		return nil, Transient(fmt.Errorf("request failed: %w", err))
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, Transient(fmt.Errorf("HTTP %d", resp.StatusCode))
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	// 401/403 is the credential being refused, not a request the provider
	// disliked — the one failure the user can fix, so it gets its own type.
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("%w: HTTP %d: %s", ErrInvalidKey, resp.StatusCode, errSnippet(data, apiKey))
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, errSnippet(data, apiKey))
	}
	return data, nil
}

// errSnippet returns a short excerpt of an error body for the message, with
// any occurrence of apiKey scrubbed out. It is NOT a general-purpose secret
// scanner — it only guarantees that this specific credential cannot be
// reconstructed from the returned string.
func errSnippet(data []byte, apiKey string) string {
	const limit = 200
	s := string(data)
	if apiKey != "" {
		s = strings.ReplaceAll(s, apiKey, "[REDACTED]")
	}
	if len(s) > limit {
		return s[:limit] + "…"
	}
	return s
}
