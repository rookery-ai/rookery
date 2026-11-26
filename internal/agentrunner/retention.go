package agentrunner

import (
	"log/slog"

	"github.com/rookery-ai/rookery/internal/prompts"
)

// RunHistoryKeep is how many of an agent's runs keep their full record: the run
// note in the knowledge base, its sidecar, and the transcript/output/errors on the
// database row. Older runs keep only their numbers (time, status, usage), which the
// spending limits read. A hard cap, not a setting — nobody, an agent edit included,
// needs more than the last 15 runs to understand what an agent is doing. Defined
// in prompts because the run prompt tells the agent the same number.
const RunHistoryKeep = prompts.RunHistoryKeep

// pruneRunHistory applies RunHistoryKeep to one agent. Best-effort: a failure is
// logged and never affects the run that triggered it.
//
// The database goes first and decides which runs are kept; the knowledge base
// then deletes every sidecar NOT in that set. So when the database step fails the
// knowledge-base step is skipped outright — an empty keep set would read as "keep
// nothing" and delete every sidecar this agent has.
func (r *Runner) pruneRunHistory(workspaceID, agentID string) {
	kept, err := r.db.PruneAgentRuns(agentID, RunHistoryKeep)
	if err != nil {
		slog.Warn("agentrunner: prune run history", "agent_id", agentID, "err", err)
		return
	}
	if r.reflector == nil {
		return
	}
	if _, err := r.reflector.PruneAgentRuns(workspaceID, agentID, RunHistoryKeep, kept); err != nil {
		slog.Warn("agentrunner: prune run notes", "agent_id", agentID, "err", err)
	}
}

// PruneAllRunHistory applies RunHistoryKeep to every agent on the install. It runs
// once at server startup so that an agent which rarely runs — or has not run since
// the cap was introduced — is brought under it too; afterwards every run prunes its
// own agent. Returns the number of agents visited.
func (r *Runner) PruneAllRunHistory() int {
	refs, err := r.db.ListAllAgentRefs()
	if err != nil {
		slog.Warn("agentrunner: list agents for run-history prune", "err", err)
		return 0
	}
	for _, ref := range refs {
		r.pruneRunHistory(ref.WorkspaceID, ref.ID)
	}
	return len(refs)
}
