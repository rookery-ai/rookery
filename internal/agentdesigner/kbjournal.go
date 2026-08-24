package agentdesigner

import (
	"log/slog"

	"github.com/rookery-ai/rookery/internal/vault"
)

// vaultRootFor returns the workspace's vault root, or "" when this Flow has no
// vault. The empty string is meaningful to BuildCoderPrompt, which gates its
// whole <agent_workspace> block on it — so a Flow without a vault produces
// exactly the prompt it did before, rather than one naming a directory that
// does not exist.
func (f *Flow) vaultRootFor(workspaceID string) string {
	if f.vlt == nil {
		return ""
	}
	return f.vlt.Root(workspaceID)
}

// beginKBRehearsal opens a write journal for one rehearsal PHASE and returns it
// with the function that undoes it.
//
// A build and a dry run are both rehearsals of an agent the user has not
// approved, and both deliberately run for real against live services — that is
// the only way the review step can show what an agent does rather than what the
// model claims it will do. The cost is real writes into the user's knowledge
// base. This is what takes them back out, so the agent's first genuine run
// starts from the knowledge base the user actually has.
//
// One journal PER PHASE, never one shared between them: the build's writes are
// reverted before the dry run begins, so the rehearsal is not handed a
// knowledge base its own build already altered.
//
// A nil vault yields a nil journal and a no-op revert. Every method on
// vault.WriteJournal tolerates a nil receiver, so callers need no branch.
func (f *Flow) beginKBRehearsal(workspaceID, phase, buildID string) (*vault.WriteJournal, func()) {
	if f.vlt == nil {
		return nil, func() {}
	}
	j := f.vlt.NewWriteJournal(workspaceID)
	return j, func() {
		reverted, err := j.Revert()
		if err != nil {
			// Loud, and NOT fatal. By the time this runs the build is already on
			// disk and past its guardrails, so failing it here would turn a
			// cleanup problem into a lost build. The operator needs to know the
			// user's knowledge base may still hold rehearsal output.
			slog.Error("agentdesigner: could not fully undo a rehearsal's knowledge-base writes",
				"workspace_id", workspaceID, "phase", phase, "build_id", buildID,
				"reverted", len(reverted), "err", err)
			return
		}
		if len(reverted) == 0 {
			return // the rehearsal stayed in its own directory; nothing to say
		}
		// Worth a line even on success: a rehearsal that had to be undone is
		// evidence about the agent being built, and without this the only trace
		// that the user's notes were briefly overwritten is the absence of one.
		slog.Info("agentdesigner: undid a rehearsal's knowledge-base writes",
			"workspace_id", workspaceID, "phase", phase, "build_id", buildID,
			"paths", reverted)
	}
}
