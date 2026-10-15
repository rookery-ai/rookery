package agentdesigner

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/rookery-ai/rookery/internal/db"
)

type descriptionDB interface {
	GetAgent(id string) (*db.Agent, error)
	UpdateAgentDescription(id, description string) error
}

// BackfillAgentDescriptions recomputes the stored description of every agent
// whose description is exactly what legacyExtractDescription derived from its
// current AGENT.md — i.e. one the old byte-cut produced, often ending mid-word.
// A description that matches anything else is left alone. Idempotent: once
// rewritten, a description no longer equals the legacy output.
func BackfillAgentDescriptions(database descriptionDB, vaultsBase string) int {
	matches, _ := filepath.Glob(filepath.Join(vaultsBase, "*", "agents", "*", "AGENT.md"))
	n := 0
	for _, path := range matches {
		agentID := filepath.Base(filepath.Dir(path))
		a, err := database.GetAgent(agentID)
		if err != nil || a == nil {
			continue // draft dir, or no row
		}
		md, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if a.Description != legacyExtractDescription(string(md), a.Name) {
			continue
		}
		next := extractDescription(string(md), a.Name)
		if next == a.Description {
			continue
		}
		if err := database.UpdateAgentDescription(a.ID, next); err != nil {
			slog.Warn("backfill agent description", "agent", a.ID, "err", err)
			continue
		}
		n++
	}
	return n
}
