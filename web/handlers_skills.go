package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/ilijad1/simple-agents/internal/prompts"
	"github.com/ilijad1/simple-agents/internal/skillstore"
)

// inferSkillMeta calls the coder to extract name and description from SKILL.md content.
// Returns empty strings on failure (caller should handle gracefully). Shared by the
// JSON skills API (api_skills.go) for ZIP/paste imports.
func (s *Server) inferSkillMeta(ctx context.Context, workspaceID, content string) (name, description string) {
	coder := s.coderForWorkspace(workspaceID)
	if coder == nil {
		return
	}

	prompt := prompts.BuildSkillMetaPrompt(content)

	result, err := coder.Generate(ctx, workspaceID, prompt)
	if err != nil {
		slog.Warn("skillstore: coder inference failed", "err", err)
		return
	}

	// Extract JSON from result (may have surrounding text).
	text := result.Text
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return
	}

	var meta struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), &meta); err != nil {
		slog.Warn("skillstore: could not parse coder JSON response", "err", err, "text", text)
		return
	}

	return skillstore.SanitizeName(meta.Name), meta.Description
}
