package agentdesigner

import (
	"context"
	"log/slog"
	"strings"

	"github.com/ilijad1/simple-agents/internal/coder"
	"github.com/ilijad1/simple-agents/internal/prompts"
)

// selectorRetries is how many times the selector call is attempted before giving up.
// One retry absorbs a transient provider hiccup; more would delay a save the user is
// waiting on for a fallback that is already best-effort.
const selectorRetries = 2

// SelectSkills picks the skills an agent needs by asking the model directly. It is the
// fallback for when the build coder omitted the `# Skills:` header entirely.
//
// Tier 1 (the header, requested in the implementation prompt) leans on a weak model
// emitting a specific line. That is exactly the unreliability this exists to cover: on
// the live install, no agent had a single skill attached. Selection now happens whether
// or not the build model cooperated.
//
// It fails CLOSED and loudly: any error, or a response with nothing recognisable in it,
// returns an empty slice and logs a warning. Attaching a guessed skill would be worse
// than attaching none. The return is always non-nil.
func SelectSkills(ctx context.Context, c *coder.Coder, workspaceID, agentMD string, pool []prompts.SkillRef) []string {
	if c == nil || len(pool) == 0 || strings.TrimSpace(agentMD) == "" {
		return []string{}
	}

	prompt := prompts.BuildSkillSelectionPrompt(agentMD, pool)
	var lastErr error
	for attempt := 0; attempt < selectorRetries; attempt++ {
		result, err := c.WithNoTools().Chat(ctx, workspaceID, nil, "", prompt)
		if err != nil {
			lastErr = err
			continue
		}
		names := parseSelectorResponse(result.Text, pool)
		if len(names) > 0 {
			slog.Info("agentdesigner: selector picked skills", "workspace_id", workspaceID, "skills", names)
			return names
		}
		// An explicit "none" and an unparseable answer are indistinguishable here, and
		// both mean "attach nothing" — so stop rather than burning a second call.
		return []string{}
	}

	slog.Warn("agentdesigner: skill selector call failed; attaching no skills",
		"workspace_id", workspaceID, "err", lastErr)
	return []string{}
}

// parseSelectorResponse extracts canonical skill names from the model's answer, reusing
// the same tolerant matcher parseSkillsLine uses so formatting drift (backticks, bullets,
// prose wrapping, alternate delimiters) is already handled and unknown names are dropped.
//
// Deliberate limit: a single-line answer that buries the name behind a colon
// ("This agent reads PDFs, so: pdf") is NOT recovered, and that is the safe choice. An
// earlier version split the last line on a widened separator set including ":" to catch
// it. That strategy could not tell an affirmative tail from a negated one, so
// "This agent explicitly does NOT use: pdf" returned ["pdf"] — the model said no and the
// parser heard yes, attaching a skill to a live agent against an explicit refusal.
//
// The colon split was its only marginal value: splitSkillCandidates already separates on
// newlines, so every other realistic shape is covered without it — a clean list
// ("pdf, csv"), a prose preamble with the list on the next line, and a lone name on its
// own line all parse correctly. Guarding the tail with a negation-word blocklist was
// rejected: natural-language negation is unbounded ("hardly", "instead of", "no need
// for", "rather than"), so that is a whack-a-mole race, and this function's contract is
// to fail closed. Losing one prose shape costs recall on a fallback-of-a-fallback;
// attaching a rejected skill costs correctness on a live agent.
func parseSelectorResponse(resp string, pool []prompts.SkillRef) []string {
	byLower := make(map[string]string, len(pool))
	for _, s := range pool {
		byLower[strings.ToLower(s.Name)] = s.Name
	}
	if len(byLower) == 0 {
		return []string{}
	}

	// A bullet list is the other common shape; try it first, then the inline split.
	lines := strings.Split(resp, "\n")
	if names := matchSkillNames(bulletCandidates(lines), byLower); len(names) > 0 {
		return names
	}
	return matchSkillNames(splitSkillCandidates(resp), byLower)
}
