package agentrunner

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rookery-ai/rookery/internal/coder"
	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/llm"
)

// budgetProvider answers each completion from a per-test script, so a test decides
// exactly what every completion costs.
type budgetProvider struct {
	mu     sync.Mutex
	calls  int
	script func(ctx context.Context, call int, req llm.Request) (*llm.Response, error)
}

func (p *budgetProvider) Name() string { return "budget-fake" }

func (p *budgetProvider) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	p.mu.Lock()
	c := p.calls
	p.calls++
	p.mu.Unlock()
	return p.script(ctx, c, req)
}

// useBudgetProvider points the harness's runner at an API coder backed by script.
// Registered under a name unique to the test, so no two tests share a script.
func useBudgetProvider(t *testing.T, h *stopHarness, script func(ctx context.Context, call int, req llm.Request) (*llm.Response, error)) *budgetProvider {
	t.Helper()
	p := &budgetProvider{script: script}
	name := "budget-fake-" + t.Name()
	llm.RegisterProvider(name, func(llm.Config) (llm.Provider, error) { return p, nil })
	api := coder.New("claude", time.Minute, filepath.Join(h.dir, "homes"), h.dir).
		WithSandbox(false).
		WithVault(h.v).
		WithAPIConfig(name, "fake-model", "fake://test", "KEY").
		WithSecretsLookup(func(context.Context, string, string) (string, error) { return "k", nil })
	h.runner.WithCoderFactory(func(string) *coder.Coder { return api })
	return p
}

// toolTurn spends tokens and asks for another turn, so a run keeps going until
// something ends it.
func toolTurn(tokens int) *llm.Response {
	return &llm.Response{
		Usage: llm.Usage{TotalTokens: tokens},
		ToolCalls: []llm.ToolCall{{
			ID: uuid.NewString(), Name: "list_dir", Args: json.RawMessage(`{"path":"."}`),
		}},
	}
}

func chatTurn(tokens int, text string) *llm.Response {
	return &llm.Response{Usage: llm.Usage{TotalTokens: tokens}, Content: "[CHAT] " + text}
}

func (h *stopHarness) setLimits(a *db.Agent, l db.AgentLimits) {
	h.t.Helper()
	l.AgentID = a.ID
	if err := h.db.SaveAgentLimits(l); err != nil {
		h.t.Fatalf("SaveAgentLimits: %v", err)
	}
}

// seedSpend records a finished run that spent tokens, as if the agent had run earlier today.
func (h *stopHarness) seedSpend(a *db.Agent, tokens int) {
	h.t.Helper()
	id := uuid.NewString()
	if err := h.db.CreateAgentRun(&db.AgentRun{ID: id, AgentID: a.ID, WorkspaceID: h.workspaceID, Trigger: "cron"}); err != nil {
		h.t.Fatal(err)
	}
	if err := h.db.FinishAgentRun(id, db.RunOutcome{TotalTokens: tokens}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *stopHarness) runOnce(a *db.Agent, sent, progress *collector) error {
	h.t.Helper()
	return waitRun(h.t, h.start(context.Background(), a, sent, progress))
}

func (h *stopHarness) inbox() []*db.InboxMessage {
	h.t.Helper()
	msgs, err := h.db.ListInboxMessages(h.workspaceID, 100, 0)
	if err != nil {
		h.t.Fatal(err)
	}
	return msgs
}

func (h *stopHarness) inboxWith(prefix string) int {
	n := 0
	for _, m := range h.inbox() {
		if strings.HasPrefix(m.Body, prefix) {
			n++
		}
	}
	return n
}

func (h *stopHarness) runCount(a *db.Agent) int {
	h.t.Helper()
	runs, err := h.db.ListAgentRuns(a.ID, 100)
	if err != nil {
		h.t.Fatal(err)
	}
	return len(runs)
}

func TestRunnerEndsRunAtPerRunTokenLimit(t *testing.T) {
	h := newStopHarness(t)
	p := useBudgetProvider(t, h, func(_ context.Context, _ int, _ llm.Request) (*llm.Response, error) {
		return toolTurn(600), nil
	})
	a := h.agent("Spender", "Look around forever.")
	h.setLimits(a, db.AgentLimits{RunTokens: 1000})

	sent, progress := &collector{}, &collector{}
	err := h.runOnce(a, sent, progress)
	if !errors.Is(err, ErrRunLimited) {
		t.Fatalf("Run returned %v, want ErrRunLimited", err)
	}
	if p.calls != 2 {
		t.Errorf("the run should end at the completion that crossed the limit, made %d calls", p.calls)
	}
	run := h.latest(a)
	if run.Limited == "" || !strings.Contains(run.Limited, "1,000 tokens") {
		t.Errorf("run.Limited = %q", run.Limited)
	}
	if run.ExitCode == nil || *run.ExitCode != -1 {
		t.Errorf("exit = %v, want -1", run.ExitCode)
	}
	if run.Stopped {
		t.Error("a limited run is not a stopped one")
	}
	if !strings.Contains(run.Transcript, "⛔ Ended: reached its per-run limit") {
		t.Errorf("transcript lacks the closing line: %s", run.Transcript)
	}
	if strings.Contains(run.Stderr, "time limit") {
		t.Errorf("a limited run must not read as a timeout: %s", run.Stderr)
	}
	if got := h.inboxWith("⛔"); got != 1 || len(h.inbox()) != 1 {
		t.Errorf("want exactly one inbox row (the notice), got %d of %d", got, len(h.inbox()))
	}
	if s := sent.all(); len(s) != 1 || !strings.HasPrefix(s[0], "⛔ per-run limit of 1,000 tokens reached") {
		t.Errorf("sent = %q", s)
	}
	if pr := progress.all(); len(pr) == 0 || !strings.HasPrefix(pr[len(pr)-1], "⛔ Ended:") {
		t.Errorf("the live view should end with the limit line: %q", pr)
	}
}

func TestLimitNoticeOncePerDay(t *testing.T) {
	h := newStopHarness(t)
	useBudgetProvider(t, h, func(context.Context, int, llm.Request) (*llm.Response, error) {
		return toolTurn(600), nil
	})
	a := h.agent("Spender", "Look around forever.")
	h.setLimits(a, db.AgentLimits{RunTokens: 1000})

	for i := 0; i < 2; i++ {
		if err := h.runOnce(a, &collector{}, &collector{}); !errors.Is(err, ErrRunLimited) {
			t.Fatalf("run %d returned %v", i, err)
		}
	}
	runs, _ := h.db.ListAgentRuns(a.ID, 10)
	for _, r := range runs {
		if r.Limited == "" {
			t.Errorf("every limited run keeps its own record: %+v", r)
		}
	}
	if got := h.inboxWith("⛔"); got != 1 {
		t.Errorf("limit notices = %d, want 1 per day", got)
	}
}

func TestRunnerPausesAfterMonthlySpend(t *testing.T) {
	h := newStopHarness(t)
	useBudgetProvider(t, h, func(context.Context, int, llm.Request) (*llm.Response, error) {
		return toolTurn(100), nil
	})
	a := h.agent("Monthly", "Look around.")
	h.setLimits(a, db.AgentLimits{MonthTokens: 1000})
	h.seedSpend(a, 950)

	if err := h.runOnce(a, &collector{}, &collector{}); !errors.Is(err, ErrRunLimited) {
		t.Fatalf("Run returned %v, want ErrRunLimited (only 50 tokens were left)", err)
	}
	got, err := h.db.GetAgent(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.BudgetPausedUntil == nil || !got.BudgetPausedUntil.After(time.Now()) {
		t.Fatalf("the agent should be paused after crossing its monthly limit: %+v", got.BudgetPausedUntil)
	}
	if !strings.Contains(got.BudgetPauseReason, "monthly limit") {
		t.Errorf("reason = %q", got.BudgetPauseReason)
	}
	if n := h.inboxWith("⏸ Paused until"); n != 1 {
		t.Fatalf("pause notices = %d, want 1", n)
	}
	// Both notices are a limit doing its job, not a failure: the inbox must not
	// show them as one.
	for _, m := range h.inbox() {
		if m.Status != InboxStatusLimited {
			t.Errorf("inbox %q status = %q, want %q", m.Body, m.Status, InboxStatusLimited)
		}
	}
	// The notices name the limit the owner set, not the 50 tokens that were left.
	if n := h.inboxWith("⛔ monthly limit of 1,000 tokens reached"); n != 1 {
		t.Errorf("limit notice should name the configured limit: %q", inboxBodies(h))
	}
	if n := h.inboxWith("⏸ Paused until"); n == 1 && !strings.Contains(strings.Join(inboxBodies(h), "\n"), "monthly limit of 1,000 tokens reached. Scheduled") {
		t.Errorf("pause notice should name the configured limit: %q", inboxBodies(h))
	}
	// Not h.latest: the seeded run and this one start in the same second.
	runs, _ := h.db.ListAgentRuns(a.ID, 10)
	var limited []string
	for _, r := range runs {
		if r.Limited != "" {
			limited = append(limited, r.Limited)
		}
	}
	if len(limited) != 1 || limited[0] != "monthly limit of 1,000 tokens" {
		t.Errorf("limited runs = %q", limited)
	}

	// The next attempt is refused, and says nothing new.
	runsBefore := h.runCount(a)
	sent := &collector{}
	err = h.runOnce(a, sent, &collector{})
	var blocked *BlockedError
	if !errors.As(err, &blocked) || blocked.Busy {
		t.Fatalf("Run returned %v, want a pause", err)
	}
	if h.runCount(a) != runsBefore {
		t.Error("a refused run must not create a run row")
	}
	if n := h.inboxWith("⏸ Paused until"); n != 1 || len(sent.all()) != 0 {
		t.Errorf("the pause is announced once: notices=%d sent=%q", n, sent.all())
	}
}

func TestPausedAgentRefusedWithoutRunRow(t *testing.T) {
	h := newStopHarness(t)
	p := useBudgetProvider(t, h, func(context.Context, int, llm.Request) (*llm.Response, error) {
		return chatTurn(10, "hi"), nil
	})
	a := h.agent("Paused", "Say hi.")
	h.setLimits(a, db.AgentLimits{DayTokens: 1000})
	until := time.Now().Add(3 * time.Hour)
	if _, err := h.db.SetAgentBudgetPause(a.ID, until, "daily limit of 1,000 tokens reached"); err != nil {
		t.Fatal(err)
	}

	err := h.runOnce(a, &collector{}, &collector{})
	var blocked *BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("Run returned %v, want *BlockedError", err)
	}
	if !strings.Contains(blocked.Error(), "Paused until") || !strings.Contains(blocked.Error(), "daily limit") {
		t.Errorf("sentence = %q", blocked.Error())
	}
	if h.runCount(a) != 0 || p.calls != 0 {
		t.Errorf("a paused agent must not run: rows=%d calls=%d", h.runCount(a), p.calls)
	}
	if err := h.runner.CheckRunnable(mustAgent(t, h, a)); !errors.As(err, &blocked) {
		t.Errorf("CheckRunnable = %v, want the pause", err)
	}
}

func TestBusyBudgetedAgentRefused(t *testing.T) {
	h := newStopHarness(t)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	useBudgetProvider(t, h, func(_ context.Context, call int, _ llm.Request) (*llm.Response, error) {
		if call == 0 {
			entered <- struct{}{}
			<-release
		}
		return chatTurn(10, "done"), nil
	})
	a := h.agent("Slow", "Say done.")
	h.setLimits(a, db.AgentLimits{DayTokens: 100000})

	first := h.start(context.Background(), a, &collector{}, &collector{})
	<-entered
	defer func() {
		close(release)
		if err := waitRun(t, first); err != nil {
			t.Errorf("first run: %v", err)
		}
	}()

	err := h.runOnce(a, &collector{}, &collector{})
	var blocked *BlockedError
	if !errors.As(err, &blocked) || !blocked.Busy {
		t.Fatalf("second run returned %v, want busy", err)
	}
	if err := h.runner.CheckRunnable(mustAgent(t, h, a)); !errors.As(err, &blocked) || !blocked.Busy {
		t.Errorf("CheckRunnable = %v, want busy", err)
	}
	if h.runCount(a) != 1 {
		t.Errorf("only the first run has a row, got %d", h.runCount(a))
	}
}

func TestCLICoderIgnoresLimits(t *testing.T) {
	h := newStopHarness(t) // the harness's default coder is a CLI script
	a := h.agent("Cli", "say hello")
	h.setLimits(a, db.AgentLimits{RunTokens: 1, DayTokens: 10})
	h.seedSpend(a, 500)

	if err := h.runOnce(a, &collector{}, &collector{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	run := h.latest(a)
	if run.Limited != "" || run.ExitCode == nil || *run.ExitCode != 0 {
		t.Errorf("a CLI run is not metered: %+v", run)
	}
	if got := mustAgent(t, h, a); got.BudgetPausedUntil != nil {
		t.Errorf("a CLI coder must not pause the agent: %v", got.BudgetPausedUntil)
	}
	// The limits are kept, not deleted.
	if l, _ := h.db.GetAgentLimits(a.ID); l.DayTokens != 10 {
		t.Errorf("limits were changed: %+v", l)
	}
}

func TestOwnerStopWinsOverLimit(t *testing.T) {
	h := newStopHarness(t)
	var a *db.Agent
	useBudgetProvider(t, h, func(context.Context, int, llm.Request) (*llm.Response, error) {
		// The owner stops first; the same completion then crosses the limit.
		h.runner.StopAgent(h.workspaceID, a.ID)
		return toolTurn(5000), nil
	})
	a = h.agent("Both", "Look around.")
	h.setLimits(a, db.AgentLimits{RunTokens: 1000})

	sent := &collector{}
	if err := h.runOnce(a, sent, &collector{}); !errors.Is(err, ErrRunStopped) {
		t.Fatalf("Run returned %v, want ErrRunStopped", err)
	}
	run := h.latest(a)
	if !run.Stopped || run.Limited != "" {
		t.Errorf("stopped=%v limited=%q, want the stop", run.Stopped, run.Limited)
	}
	if len(sent.all()) != 0 || len(h.inbox()) != 0 {
		t.Errorf("a stop sends nothing: sent=%q inbox=%d", sent.all(), len(h.inbox()))
	}
}

func TestRunnerDollarOnlyLimitWithUnreportedCost(t *testing.T) {
	h := newStopHarness(t)
	old := providerReportsCost
	providerReportsCost = func(string) bool { return true }
	t.Cleanup(func() { providerReportsCost = old })
	useBudgetProvider(t, h, func(_ context.Context, call int, _ llm.Request) (*llm.Response, error) {
		if call < 2 {
			return toolTurn(5000), nil // tokens, and no cost reported
		}
		return chatTurn(5000, "done"), nil
	})
	a := h.agent("Dollars", "Look around.")
	h.setLimits(a, db.AgentLimits{RunUSD: 0.01})

	if err := h.runOnce(a, &collector{}, &collector{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	run := h.latest(a)
	if run.Limited != "" {
		t.Errorf("unreported cost must not be read as a limit hit, nor as zero: %q", run.Limited)
	}
	if !strings.Contains(run.Stderr, "dollar limit not checked") {
		t.Errorf("the run should record that the dollar limit went unchecked: %q", run.Stderr)
	}
}

func TestRunnerDollarLimitEndsRunWhenCostReported(t *testing.T) {
	h := newStopHarness(t)
	old := providerReportsCost
	providerReportsCost = func(string) bool { return true }
	t.Cleanup(func() { providerReportsCost = old })
	useBudgetProvider(t, h, func(context.Context, int, llm.Request) (*llm.Response, error) {
		r := toolTurn(10)
		r.Usage.Cost, r.Usage.CostReported = 0.006, true
		return r, nil
	})
	a := h.agent("Dollars", "Look around.")
	h.setLimits(a, db.AgentLimits{RunUSD: 0.01})

	if err := h.runOnce(a, &collector{}, &collector{}); !errors.Is(err, ErrRunLimited) {
		t.Fatalf("Run returned %v, want ErrRunLimited", err)
	}
	if run := h.latest(a); !strings.Contains(run.Limited, "$0.01") {
		t.Errorf("Limited = %q", run.Limited)
	}
}

func TestPauseExpiryDoesNotReactivateInactiveAgent(t *testing.T) {
	h := newStopHarness(t)
	a := h.agent("Off", "say hello")
	if _, err := h.db.SetAgentBudgetPause(a.ID, time.Now().Add(-time.Hour), "daily limit of 10 tokens reached"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Exec(`UPDATE agents SET active=0 WHERE id=?`, a.ID); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Minute)
	if err := h.db.UpsertAgentSchedule(&db.AgentSchedule{
		ID: uuid.NewString(), AgentID: a.ID, WorkspaceID: h.workspaceID,
		CronExpr: "* * * * *", NextRunAt: &past, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	due, err := h.db.ListDueSchedules(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range due {
		if s.AgentID == a.ID {
			t.Fatal("an expired budget pause must not bring back an agent the owner turned off")
		}
	}
}

func TestCallChildPausedIsSkippedWithWarning(t *testing.T) {
	h := newStopHarness(t)
	useBudgetProvider(t, h, func(_ context.Context, call int, _ llm.Request) (*llm.Response, error) {
		if call == 0 {
			return &llm.Response{Usage: llm.Usage{TotalTokens: 10}, Content: "[CALL: Child]"}, nil
		}
		return chatTurn(10, "parent done"), nil
	})
	parent := h.agent("Parent", "Call the child.")
	child := h.agent("Child", "Say hi.")
	h.setLimits(child, db.AgentLimits{DayTokens: 100})
	if _, err := h.db.SetAgentBudgetPause(child.ID, time.Now().Add(time.Hour), "daily limit of 100 tokens reached"); err != nil {
		t.Fatal(err)
	}

	if err := h.runOnce(parent, &collector{}, &collector{}); err != nil {
		t.Fatalf("the parent should finish: %v", err)
	}
	run := h.latest(parent)
	if !strings.Contains(run.Stderr, "skipping [CALL: Child]: paused — daily limit of 100 tokens reached") {
		t.Errorf("parent warnings = %q", run.Stderr)
	}
	if h.runCount(child) != 0 {
		t.Error("a paused child must not create a run row")
	}
}

func mustAgent(t *testing.T, h *stopHarness, a *db.Agent) *db.Agent {
	t.Helper()
	got, err := h.db.GetAgent(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func inboxBodies(h *stopHarness) []string {
	var out []string
	for _, m := range h.inbox() {
		out = append(out, m.Body)
	}
	return out
}

// The engine's wrap-up turn returns its answer without an error even when its
// completion crosses the ceiling. That run finished: it delivers, and is not
// recorded as cut short.
func TestRunFinishedOnFinalCompletionIsNotLimited(t *testing.T) {
	h := newStopHarness(t)
	useBudgetProvider(t, h, func(_ context.Context, _ int, req llm.Request) (*llm.Response, error) {
		if len(req.Tools) == 0 {
			// The grace turn: tools withdrawn, answer now. This completion crosses the limit.
			return chatTurn(5000, "wrapped up"), nil
		}
		// Failing calls, so the unproductive-streak guard ends the loop.
		return &llm.Response{Usage: llm.Usage{TotalTokens: 10}, ToolCalls: []llm.ToolCall{{
			ID: uuid.NewString(), Name: "no_such_tool", Args: json.RawMessage(`{"n":"` + uuid.NewString() + `"}`),
		}}}, nil
	})
	a := h.agent("Finisher", "Try things.")
	h.setLimits(a, db.AgentLimits{RunTokens: 1000})

	sent := &collector{}
	if err := h.runOnce(a, sent, &collector{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	run := h.latest(a)
	if run.Limited != "" || run.ExitCode == nil || *run.ExitCode != 0 {
		t.Errorf("a finished run must not be recorded as limited: limited=%q exit=%v", run.Limited, run.ExitCode)
	}
	if s := strings.Join(sent.all(), "\n"); !strings.Contains(s, "wrapped up") {
		t.Errorf("the answer should be delivered: %q", s)
	}
	if h.inboxWith("⛔") != 0 {
		t.Error("no limit notice for a finished run")
	}
}

// The same in the ordinary loop, not just the wrap-up turn: a final answer whose
// completion crosses the ceiling is delivered and the run is not limited.
func TestFinalAnswerCrossingTheLimitIsDelivered(t *testing.T) {
	h := newStopHarness(t)
	useBudgetProvider(t, h, func(_ context.Context, call int, _ llm.Request) (*llm.Response, error) {
		if call == 0 {
			return toolTurn(600), nil
		}
		return chatTurn(600, "final answer"), nil
	})
	a := h.agent("Closer", "Look once, then answer.")
	h.setLimits(a, db.AgentLimits{RunTokens: 1000})

	sent := &collector{}
	if err := h.runOnce(a, sent, &collector{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	run := h.latest(a)
	if run.Limited != "" || run.ExitCode == nil || *run.ExitCode != 0 {
		t.Errorf("limited=%q exit=%v, want a finished run", run.Limited, run.ExitCode)
	}
	if s := strings.Join(sent.all(), "\n"); !strings.Contains(s, "final answer") {
		t.Errorf("the answer should be delivered: %q", s)
	}
	if h.inboxWith("⛔") != 0 {
		t.Error("no limit notice for a finished run")
	}
}

// A turn that crosses the ceiling and asks for another agent has not finished its
// work: the child is not started (it would inherit this run's limit) and the run
// is recorded as limited.
func TestCallAfterLimitIsNotDispatched(t *testing.T) {
	h := newStopHarness(t)
	useBudgetProvider(t, h, func(_ context.Context, call int, _ llm.Request) (*llm.Response, error) {
		if call == 0 {
			return &llm.Response{Usage: llm.Usage{TotalTokens: 5000}, Content: "[CALL: Helper]"}, nil
		}
		return chatTurn(10, "should not run"), nil
	})
	parent := h.agent("Caller", "Ask the helper.")
	child := h.agent("Helper", "Say hi.")
	h.setLimits(parent, db.AgentLimits{RunTokens: 1000})

	err := h.runOnce(parent, &collector{}, &collector{})
	if !errors.Is(err, ErrRunLimited) {
		t.Fatalf("Run returned %v, want ErrRunLimited", err)
	}
	if h.runCount(child) != 0 {
		t.Error("a child must not be started after the parent's limit was reached")
	}
	if run := h.latest(parent); run.Limited == "" {
		t.Errorf("the parent run should be recorded as limited: %+v", run)
	}
}

// A dollar day limit on a provider that reports no cost is kept but never checked,
// so it must not make the agent refuse an overlapping run.
func TestUnenforceableDollarPeriodLimitDoesNotMakeAgentBusy(t *testing.T) {
	h := newStopHarness(t)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	useBudgetProvider(t, h, func(_ context.Context, call int, _ llm.Request) (*llm.Response, error) {
		if call == 0 {
			entered <- struct{}{}
			<-release
		}
		return chatTurn(10, "done"), nil
	})
	a := h.agent("SlowDollars", "Say done.")
	h.setLimits(a, db.AgentLimits{DayUSD: 5})

	first := h.start(context.Background(), a, &collector{}, &collector{})
	<-entered
	if err := h.runner.CheckRunnable(mustAgent(t, h, a)); err != nil {
		t.Errorf("CheckRunnable = %v, want nil", err)
	}
	second := h.runOnce(a, &collector{}, &collector{})
	close(release)
	if err := waitRun(t, first); err != nil {
		t.Errorf("first run: %v", err)
	}
	if second != nil {
		t.Fatalf("second run returned %v, want it to run", second)
	}
	if h.runCount(a) != 2 {
		t.Errorf("both runs should have rows, got %d", h.runCount(a))
	}
}

func TestAgentEnforcementFollowsTheCoder(t *testing.T) {
	r := &Runner{}
	if e := r.AgentEnforcement("ws", "a"); e.Enforced {
		t.Errorf("a runner with no API coder must not enforce: %+v", e)
	}
}
