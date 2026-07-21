package agentdesigner

import (
	"context"
	"log/slog"
	"regexp"
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
// A model that ignores the "answer with only a list" instruction sometimes wraps the
// answer in a sentence ("This agent reads PDFs, so: pdf") instead of returning a clean
// list. Neither the bullet nor the full-string split recognises that shape (the
// candidate token is "so: pdf", not "pdf"), so as a last resort — only when both
// stricter strategies find nothing — lastLineCandidate takes just the final token of
// the last non-empty line, which is where a model states its actual answer.
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
	if names := matchSkillNames(splitSkillCandidates(resp), byLower); len(names) > 0 {
		return names
	}
	return matchSkillNames(lastLineCandidate(resp), byLower)
}

// lastLineSepRe widens splitSkillCandidates' separator set with a colon — deliberately
// excluded there because skill names are matched whole first, but needed here to split
// a trailing clause like "...so: pdf".
var lastLineSepRe = regexp.MustCompile(`(?i)\s*,\s*|\s*;\s*|\s*\|\s*|\s*\+\s*|\s*&\s*|\s*/\s*|\s+and\s+|\s+or\s+|\s*:\s*`)

// lastLineCandidate returns a single best-guess candidate: the last token of the last
// non-empty line, split on the widened separator set above. It exists only as the final
// fallback in parseSelectorResponse, after the stricter strategies have already found
// nothing — tokenising an entire prose sentence as a list of candidates would produce
// garbage like "This agent reads PDFs" as a candidate name, so only the tail is tried.
func lastLineCandidate(resp string) []string {
	lines := strings.Split(resp, "\n")
	var last string
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			last = t
			break
		}
	}
	if last == "" {
		return nil
	}
	tokens := lastLineSepRe.Split(last, -1)
	if len(tokens) == 0 {
		return nil
	}
	return []string{tokens[len(tokens)-1]}
}
