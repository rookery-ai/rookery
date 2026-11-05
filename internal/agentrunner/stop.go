package agentrunner

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/rookery-ai/rookery/internal/db"
)

// ErrRunStopped is returned by Run when the owner stopped the run. It is not a
// failure, so callers must not report it as one.
var ErrRunStopped = errors.New("agent run stopped")

// runStoppedNotice closes a stopped run's live view and its transcript.
const runStoppedNotice = "⏹ Stopped by the owner. What the run had already done was left as it was."

// activeRun is one in-flight run the owner can stop. Runs register here rather than
// in the web layer's tracker because a cron or chat run has no tracker entry, and
// this is the one place every trigger passes through.
type activeRun struct {
	workspaceID string
	agentID     string
	agentName   string
	depth       int
	cancel      context.CancelCauseFunc
}

// trackRun makes a run stoppable. untrack removes it from the registry and is safe
// to call twice; release frees the context and must always run.
//
// The cancel cause, not the error the coder comes back with, is what later says the
// run was stopped: a cancelled API turn can report ErrTimeout and a killed CLI coder
// reports "signal: killed". Children inherit the cause through their context.
func (r *Runner) trackRun(ctx context.Context, agent *db.Agent, input RunInput) (runCtx context.Context, untrack, release func()) {
	runCtx, cancel := context.WithCancelCause(ctx)
	run := &activeRun{
		workspaceID: input.WorkspaceID,
		agentID:     input.AgentID,
		agentName:   agent.Name,
		depth:       input.depth,
		cancel:      cancel,
	}
	r.activeMu.Lock()
	if r.active == nil {
		r.active = make(map[*activeRun]struct{})
	}
	r.active[run] = struct{}{}
	r.activeMu.Unlock()

	untrack = func() {
		r.activeMu.Lock()
		delete(r.active, run)
		r.activeMu.Unlock()
	}
	release = func() { cancel(nil) }
	return runCtx, untrack, release
}

// stopMatching cancels every registered run the predicate selects, under the lock.
// Holding it is what makes a true result reliable: a run reads its cause only after
// untracking, so a cancel made here is always visible to it.
func (r *Runner) stopMatching(match func(*activeRun) bool) bool {
	r.activeMu.Lock()
	defer r.activeMu.Unlock()
	stopped := false
	for run := range r.active {
		if match(run) {
			run.cancel(ErrRunStopped)
			stopped = true
		}
	}
	return stopped
}

// StopAgent stops the agent's in-flight runs and reports whether there were any.
func (r *Runner) StopAgent(workspaceID, agentID string) bool {
	return r.stopMatching(func(run *activeRun) bool {
		return run.workspaceID == workspaceID && run.agentID == agentID
	})
}

// StopAgentByName is StopAgent for a name typed into a chat app, so case is ignored.
func (r *Runner) StopAgentByName(workspaceID, name string) bool {
	name = strings.TrimSpace(name)
	return r.stopMatching(func(run *activeRun) bool {
		return run.workspaceID == workspaceID && strings.EqualFold(run.agentName, name)
	})
}

// RunningAgentNames lists the agents the owner started or scheduled that are running
// now, sorted and without repeats. A child called by another agent is left out: it
// stops with its parent, and counting it would make a single running agent look like
// several when the owner sends a bare /agent stop.
func (r *Runner) RunningAgentNames(workspaceID string) []string {
	r.activeMu.Lock()
	defer r.activeMu.Unlock()
	seen := make(map[string]struct{})
	names := []string{}
	for run := range r.active {
		if run.workspaceID != workspaceID || run.depth > 0 {
			continue
		}
		if _, dup := seen[run.agentName]; dup {
			continue
		}
		seen[run.agentName] = struct{}{}
		names = append(names, run.agentName)
	}
	sort.Strings(names)
	return names
}
