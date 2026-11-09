package agentrunner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/rookery-ai/rookery/internal/budget"
	"github.com/rookery-ai/rookery/internal/coder"
	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/llm"
	"github.com/rookery-ai/rookery/internal/profile"
)

// ErrRunLimited is returned by Run when a spending limit ended the run. The owner
// has already been told (at most once a day), so callers must not report it again
// as a failure.
var ErrRunLimited = errors.New("agent run limited")

// limitCause is the cancel cause a spending limit ends a run with. Value receivers
// on purpose: the run is cancelled with a value and read back with errors.As into a
// value, and mixing the two forms would make As miss it silently.
type limitCause struct{ reason string }

func (c limitCause) Error() string { return "agent run limited: " + c.reason }
func (c limitCause) Unwrap() error { return ErrRunLimited }

// providerReportsCost is coder.ProviderReportsCost, held in a variable so tests can
// stand in a provider that reports cost without registering under a real name.
var providerReportsCost = coder.ProviderReportsCost

// InboxStatusLimited marks an inbox row a spending limit produced — a pause or a
// limited run. It is not "error": the agent was held to the owner's own cap, and
// the inbox rendering it as a failure sends the owner looking for a bug.
const InboxStatusLimited = "limited"

// pauseTimeLayout is how a pause's end is written to the owner.
const pauseTimeLayout = "Mon 2 Jan 15:04"

// BlockedError is why a run was refused before it started. No run row exists for
// it: a refused run did not run, and recording one per skipped cron slot would bury
// the agent's real history.
type BlockedError struct {
	// Busy: another run of this agent is in flight, and the agent has a daily or
	// monthly limit, so a second run could overshoot what the first was admitted under.
	Busy bool
	// Until and Reason describe a budget pause.
	Until  time.Time
	Reason string

	loc *time.Location
}

func (e *BlockedError) Error() string {
	if e.Busy {
		return "⏸ This agent is already running. It has a daily or monthly limit, so a second run cannot start until the first one finishes."
	}
	loc := e.loc
	if loc == nil {
		loc = time.Local
	}
	return fmt.Sprintf("⏸ Paused until %s — %s. Raise or remove the limit on the agent's page to resume now.",
		e.Until.In(loc).Format(pauseTimeLayout), e.Reason)
}

// shortReason is the clause a parent's [CALL] warning quotes.
func (e *BlockedError) shortReason() string {
	if e.Busy {
		return "already running"
	}
	if e.Reason == "" {
		return "paused"
	}
	return "paused — " + e.Reason
}

// coderForAgent is which coder an agent runs on. Today the coder is per workspace;
// it is its own function so the multi-coder project replaces this one seam and the
// budget enforcement that keys off it follows without further change.
func (r *Runner) coderForAgent(workspaceID, agentID string) *coder.Coder {
	_ = agentID
	return r.coderForWorkspace(workspaceID)
}

// Enforcement says what limits the coder lets the runner hold an agent to. A CLI
// coder reports no usage at all, so its agents' limits are kept but not enforced.
func (r *Runner) Enforcement(c *coder.Coder) budget.Enforcement {
	if c == nil || !c.IsAPI() {
		return budget.Enforcement{}
	}
	return budget.Enforcement{Enforced: true, DollarsSupported: providerReportsCost(c.APIProviderName())}
}

// admission is what the start gate decided for one run.
type admission struct {
	limits  db.AgentLimits
	ceiling budget.RunCeiling
	loc     *time.Location
	// periodLimited: the agent has a daily or monthly limit, so it must not run
	// twice at once and is re-checked for a pause after every run.
	periodLimited bool
}

func (r *Runner) budgetZone(workspaceID string) *time.Location {
	if r.db == nil {
		return time.Local
	}
	_, loc := profile.ScheduleZone(r.db, workspaceID)
	if loc == nil {
		return time.Local
	}
	return loc
}

// admit applies the paused and period checks. A pause is set (and announced once)
// when a period limit is found to be hit. A failed read lets the run start: the
// limits are a guard on spend, and a database hiccup should cost at most one
// unmetered run, not every agent's work.
func (r *Runner) admit(agent *db.Agent, e budget.Enforcement, input RunInput) (admission, error) {
	adm := admission{loc: r.budgetZone(input.WorkspaceID)}
	if !e.Enforced {
		return adm, nil
	}
	now := time.Now()
	if agent.BudgetPausedUntil != nil && now.Before(*agent.BudgetPausedUntil) {
		return adm, &BlockedError{Until: *agent.BudgetPausedUntil, Reason: agent.BudgetPauseReason, loc: adm.loc}
	}
	limits, err := r.db.GetAgentLimits(agent.ID)
	if err != nil {
		slog.Warn("agentrunner: could not read agent limits; running unmetered", "agent_id", agent.ID, "err", err)
		return adm, nil
	}
	adm.limits = limits
	d, ok := r.decide(agent.ID, limits, e, now, adm.loc)
	if !ok {
		return adm, nil
	}
	if !d.Allowed {
		r.pauseAgent(agent, input, d, now, adm.loc)
		return adm, &BlockedError{Until: d.PauseUntil, Reason: d.Reason, loc: adm.loc}
	}
	adm.ceiling = d.Remaining
	// Only limits that can be checked make the agent exclusive: a dollar limit on a
	// provider that reports no cost is kept but never evaluated, so it must not
	// refuse an overlapping run either.
	adm.periodLimited = budget.HasEnforceablePeriodLimit(limits, e)
	return adm, nil
}

// decide reads this day's and month's spend and applies the limits to it. ok is
// false when the spend could not be read.
func (r *Runner) decide(agentID string, limits db.AgentLimits, e budget.Enforcement, now time.Time, loc *time.Location) (budget.StartDecision, bool) {
	day, err := r.db.AgentSpendSince(agentID, budget.PeriodStart(budget.PeriodDay, now, loc))
	if err != nil {
		slog.Warn("agentrunner: could not read the agent's spend", "agent_id", agentID, "err", err)
		return budget.StartDecision{}, false
	}
	month, err := r.db.AgentSpendSince(agentID, budget.PeriodStart(budget.PeriodMonth, now, loc))
	if err != nil {
		slog.Warn("agentrunner: could not read the agent's spend", "agent_id", agentID, "err", err)
		return budget.StartDecision{}, false
	}
	return budget.DecideStart(limits, day, month, e, now, loc), true
}

// pauseAgent records the pause and tells the owner, once per pause: SetAgentBudgetPause
// reports no change when the same pause is already set, so the skipped runs that
// follow it stay quiet.
func (r *Runner) pauseAgent(agent *db.Agent, input RunInput, d budget.StartDecision, now time.Time, loc *time.Location) {
	changed, err := r.db.SetAgentBudgetPause(agent.ID, d.PauseUntil, d.Reason)
	if err != nil {
		slog.Warn("agentrunner: could not pause the agent", "agent_id", agent.ID, "err", err)
		return
	}
	slog.Info("agentrunner: agent paused by its spending limit",
		"agent_id", agent.ID, "agent", agent.Name, "until", d.PauseUntil, "reason", d.Reason, "new", changed)
	if !changed {
		return
	}
	msg := fmt.Sprintf("⏸ Paused until %s — %s. Scheduled and manual runs are blocked until then; raise or remove the limit on the agent's page to resume now.",
		d.PauseUntil.In(loc).Format(pauseTimeLayout), d.Reason)
	if input.SendOutput != nil {
		input.SendOutput(msg)
	}
	// No run id: a refused run has no row, and a pause found after a run belongs
	// to the agent rather than to that run.
	r.recordInbox(input, agent, "", msg, InboxStatusLimited)
}

// recheckPeriod pauses the agent straight after a run that took it over a daily or
// monthly limit, so the owner hears about it now rather than at the next skipped slot.
func (r *Runner) recheckPeriod(agent *db.Agent, input RunInput, e budget.Enforcement, limits db.AgentLimits, loc *time.Location) {
	now := time.Now()
	d, ok := r.decide(agent.ID, limits, e, now, loc)
	if ok && !d.Allowed {
		r.pauseAgent(agent, input, d, now, loc)
	}
}

// CheckRunnable reports whether the agent may start a run now, as a *BlockedError,
// without starting one. For the web layer to refuse a Run click with the reason
// instead of opening a live view that ends at once. Its only side effect is the one
// the run itself would have: a period limit newly found to be hit sets the pause and
// sends its notice.
func (r *Runner) CheckRunnable(agent *db.Agent) error {
	e := r.Enforcement(r.coderForAgent(agent.WorkspaceID, agent.ID))
	input := RunInput{AgentID: agent.ID, WorkspaceID: agent.WorkspaceID, Trigger: "manual"}
	adm, err := r.admit(agent, e, input)
	if err != nil {
		return err
	}
	if adm.periodLimited && r.runningCount(agent.ID) > 0 {
		return &BlockedError{Busy: true}
	}
	return nil
}

// runGuard meters one run's completions against its ceiling.
type runGuard struct {
	mu      sync.Mutex
	spent   llm.Usage
	turns   int
	costGap bool // a completion used tokens and reported no cost
}

// withRunGuard ends the run with a limit cause the moment its usage reaches the
// ceiling. It keeps its own total rather than reading the run's: the guard fires per
// completion, while the run only learns its usage when a Generate call returns.
//
// costGap is tracked here because llm.Usage.Add ORs CostReported: one completion that
// reported a cost would make a total missing another's read as measured, and a dollar
// ceiling checked against it would let the run overspend.
func withRunGuard(c *coder.Coder, ceiling budget.RunCeiling, dollars bool, cancel context.CancelCauseFunc) (*coder.Coder, *runGuard) {
	g := &runGuard{}
	return c.WithUsageGuard(func(delta llm.Usage) {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.spent = g.spent.Add(delta)
		g.turns++
		if delta.TotalTokens > 0 && !delta.CostReported {
			g.costGap = true
		}
		if hit, reason := ceiling.Exceeded(g.spent, g.turns, dollars && !g.costGap); hit {
			cancel(limitCause{reason: reason})
		}
	}), g
}

func (g *runGuard) hadCostGap() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.costGap
}

// limitedLine closes a limited run's live view and its transcript.
func limitedLine(reason string) string { return "⛔ Ended: reached its " + reason + "." }

// limitNotice tells the owner a limit ended a run, at most once a day per agent so an
// agent hitting its limit every few seconds cannot flood the inbox. Every limited run
// still carries its own record.
func (r *Runner) limitNotice(agent *db.Agent, input RunInput, runID, reason string, loc *time.Location) {
	first, err := r.db.MarkLimitNoticeSent(agent.ID, time.Now().In(loc).Format("2006-01-02"))
	if err != nil {
		slog.Warn("agentrunner: could not record the limit notice", "agent_id", agent.ID, "err", err)
		return
	}
	if !first {
		return
	}
	msg := "⛔ " + reason + " reached — this run was ended. Raise or remove the limit on the agent's page if it needed more."
	if input.SendOutput != nil {
		input.SendOutput(msg)
	}
	r.recordInbox(input, agent, runID, msg, InboxStatusLimited)
}

// hasDollarLimit reports whether any limit is set in dollars.
func hasDollarLimit(l db.AgentLimits) bool { return l.RunUSD > 0 || l.DayUSD > 0 || l.MonthUSD > 0 }

// AgentEnforcement is Enforcement for the coder this agent would run on, for
// callers (the web API) that hold ids rather than a coder.
func (r *Runner) AgentEnforcement(workspaceID, agentID string) budget.Enforcement {
	return r.Enforcement(r.coderForAgent(workspaceID, agentID))
}
