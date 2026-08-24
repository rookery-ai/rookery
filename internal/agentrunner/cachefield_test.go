package agentrunner

import (
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/llm"
)

// "The provider reports no cache hits" and "the provider reports nothing" are
// opposite findings — the first says caching is broken and worth fixing, the
// second says the measurement is unavailable. Rendering both as 0 would make an
// unanswerable case look like a finding, which is the mistake that turned one
// bug into four wrong diagnoses.
func TestCachedTokensFieldDistinguishesAbsentFromZero(t *testing.T) {
	absent := cachedTokensField(llm.Usage{PromptTokens: 100_000})
	if absent != "n/a" {
		t.Errorf("an unreported cache rendered as %q, want n/a", absent)
	}

	zero := cachedTokensField(llm.Usage{PromptTokens: 100_000, CacheReported: true})
	if zero == "n/a" {
		t.Errorf("a reported zero rendered as n/a, hiding a real finding: %q", zero)
	}
	if !strings.HasPrefix(zero, "0") {
		t.Errorf("a reported zero rendered as %q, want it to start with 0", zero)
	}
}

// The absolute number alone does not say whether caching is working — 50k
// cached is excellent against a 55k prompt and negligible against 1.3M.
func TestCachedTokensFieldShowsTheShareOfThePrompt(t *testing.T) {
	got := cachedTokensField(llm.Usage{
		PromptTokens: 200_000, CachedTokens: 150_000, CacheReported: true,
	})
	if !strings.Contains(got, "75%") {
		t.Errorf("got %q, want it to report 75%% of the prompt", got)
	}
}

// Guard the division: a failed run reports usage of zero.
func TestCachedTokensFieldSurvivesAZeroPrompt(t *testing.T) {
	if got := cachedTokensField(llm.Usage{CacheReported: true}); got != "0" {
		t.Errorf("got %q, want a bare 0 with no percentage", got)
	}
}
