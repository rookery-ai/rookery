package agentrunner

import (
	"fmt"

	"github.com/rookery-ai/rookery/internal/llm"
)

// cachedTokensField renders the run's prompt-cache accounting for the log.
//
// The tool loop is strictly append-only with the system prompt set once, so
// every turn resends a byte-identical prefix — 33 KB of skills, memory and
// AGENT.md before the static blocks. Whether a provider serves that from cache
// changes a run's cost by roughly an order of magnitude, and nothing recorded
// it, so "is this run expensive because it paid for the same bytes on every
// one of 38 turns?" could not be answered from the logs at all.
//
// Returns "n/a" when the provider said nothing, because a bare 0 would claim a
// measurement that was never taken. Those are opposite conclusions: zero means
// caching is not working and is worth fixing, absent means the question is
// still open and the next step is finding out why the provider is silent.
func cachedTokensField(u llm.Usage) string {
	if !u.CacheReported {
		return "n/a"
	}
	if u.PromptTokens <= 0 {
		return fmt.Sprintf("%d", u.CachedTokens)
	}
	pct := float64(u.CachedTokens) / float64(u.PromptTokens) * 100
	return fmt.Sprintf("%d (%.0f%% of prompt)", u.CachedTokens, pct)
}
