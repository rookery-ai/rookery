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
	GetAgentLimits(agentID string) (db.AgentLimits, error)
	SaveAgentLimits(db.AgentLimits) error
	ClearAgentBudgetPause(agentID string) error
}

// limitsDB is the optional limits capability of the design store. An optional
// capability rather than a dbDesignStore method, so the test fakes standing in
// for the store need not grow one; the real *db.DB has it.
func (f *Flow) limitsDB() limitsStore {
	if f.db == nil {
		return nil
	}
	store, _ := f.db.(limitsStore)
	return store
}

// currentLimitsLine is the edited agent's limits as a Limits: value, for the edit
// design prompt. "" (read as none) when they cannot be read.
func (f *Flow) currentLimitsLine(agentID string) string {
	store := f.limitsDB()
	if store == nil || agentID == "" {
		return ""
	}
	l, err := store.GetAgentLimits(agentID)
	if err != nil {
		return ""
	}
	return budget.FormatSpecLine(l)
}

// editCurrentLimits is CurrentLimits for the design prompt: the agent's limits on
// an edit, "" on a create (which has no agent yet).
func (f *Flow) editCurrentLimits(sess *DesignSession) string {
	if sess == nil || !sess.IsEdit {
		return ""
	}
	return f.currentLimitsLine(sess.AgentID)
}

// specLimitsValue finds the value of the `Limits:` line in a spec. A model
// decorates labels it was told to write plainly — "- Limits:", "**Limits:**",
// "**Limits**:", or the whole line in bold — so markdown emphasis and a list
// marker are stripped from the label and from both ends of the value before
// either is judged. Anything else about the line is left to the parser.
func specLimitsValue(spec string) (string, bool) {
	for _, line := range strings.Split(spec, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "-*+ \t")
		label, value, found := strings.Cut(line, ":")
		if !found || !strings.EqualFold(strings.Trim(label, "*_ \t"), "Limits") {
			continue
		}
		return strings.Trim(value, "*_ \t"), true
	}
	return "", false
}

// persistLimits saves the `Limits:` line of the approved plan. Only an
// explicit, parseable value changes anything: "unchanged", a missing line
// (limits unavailable, or a model that forgot it) and garbage all leave the
// agent's limits exactly as they were — a limit silently dropped or invented
// from a misparse is worse than one left alone.
//
// A create takes the line as the whole set. An edit merges per period: each
// period the line names replaces that period's limits and every other period
// keeps what it has, because a designer restating only the daily limit would
// otherwise erase the monthly one the owner never mentioned. "none" alone still
// clears everything.
func (f *Flow) persistLimits(agentID string, history []db.ChatMessage, isEdit bool) {
	store := f.limitsDB()
	if store == nil || agentID == "" {
		return
	}
	value, found := specLimitsValue(latestSpecInHistory(history))
	if !found {
		return
	}
	l, named, set, ok := budget.ParseSpecLineScopes(value)
	if !set || !ok {
		return
	}
	if isEdit {
		cur, err := store.GetAgentLimits(agentID)
		if err != nil {
			// Merging into a zero value would wipe the periods meant to be kept.
			slog.Warn("agentdesigner: read agent limits for merge", "agent_id", agentID, "err", err)
			return
		}
		l = budget.MergeLimitScopes(cur, l, named)
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
}
