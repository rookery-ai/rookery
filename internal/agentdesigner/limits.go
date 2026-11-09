package agentdesigner

import (
	"log/slog"
	"strings"

	"github.com/rookery-ai/rookery/internal/budget"
	"github.com/rookery-ai/rookery/internal/db"
)

// latestSpecInHistory returns the most recent [TECHNICAL SPEC] block in the
// conversation. It scans backwards rather than reading the last turn because by
// save time the last assistant turn is the build review, which carries no spec;
// the plan the owner approved is an earlier turn.
func latestSpecInHistory(history []db.ChatMessage) string {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role != "assistant" {
			continue
		}
		if spec := extractTechnicalSpec(history[i].Content); spec != "" {
			return spec
		}
	}
	return ""
}

type limitsStore interface {
	SaveAgentLimits(db.AgentLimits) error
	ClearAgentBudgetPause(agentID string) error
}

// persistLimits saves the `Limits:` line of the approved plan. Only an
// explicit, parseable value changes anything: "unchanged", a missing line
// (limits unavailable, or a model that forgot it) and garbage all leave the
// agent's limits exactly as they were — a limit silently dropped or invented
// from a misparse is worse than one left alone.
func (f *Flow) persistLimits(agentID string, history []db.ChatMessage) {
	if f.db == nil || agentID == "" {
		return
	}
	// An optional capability rather than a dbDesignStore method, so the test
	// fakes standing in for the store need not grow one; the real *db.DB has both.
	store, ok := f.db.(limitsStore)
	if !ok {
		return
	}
	for _, line := range strings.Split(latestSpecInHistory(history), "\n") {
		label, value, found := strings.Cut(strings.TrimSpace(line), ":")
		if !found || !strings.EqualFold(strings.TrimSpace(label), "Limits") {
			continue
		}
		l, set, ok := budget.ParseSpecLine(value)
		if !set || !ok {
			return
		}
		l.AgentID = agentID
		if err := store.SaveAgentLimits(l); err != nil {
			slog.Warn("agentdesigner: save agent limits", "agent_id", agentID, "err", err)
			return
		}
		// The owner just approved a new budget; a pause from the old one
		// would otherwise outlive the decision that replaced it.
		if err := store.ClearAgentBudgetPause(agentID); err != nil {
			slog.Warn("agentdesigner: clear budget pause", "agent_id", agentID, "err", err)
		}
		return
	}
}
