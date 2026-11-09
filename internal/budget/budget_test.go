package budget

import (
	"testing"
	"time"

	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/llm"
)

var plus2 = time.FixedZone("UTC+2", 2*3600)

func TestPeriodStartUsesWorkspaceZone(t *testing.T) {
	now := time.Date(2026, 11, 30, 23, 30, 0, 0, time.UTC) // already Dec 1 in UTC+2
	if got, want := PeriodStart(PeriodMonth, now, plus2), time.Date(2026, 12, 1, 0, 0, 0, 0, plus2); !got.Equal(want) {
		t.Fatalf("month start %v want %v", got, want)
	}
	if got, want := PeriodEnd(PeriodMonth, now, plus2), time.Date(2027, 1, 1, 0, 0, 0, 0, plus2); !got.Equal(want) {
		t.Fatalf("month end %v want %v", got, want)
	}
	if got, want := PeriodStart(PeriodDay, now, plus2), time.Date(2026, 12, 1, 0, 0, 0, 0, plus2); !got.Equal(want) {
		t.Fatalf("day start %v want %v", got, want)
	}
	if got, want := PeriodEnd(PeriodDay, now, plus2), time.Date(2026, 12, 2, 0, 0, 0, 0, plus2); !got.Equal(want) {
		t.Fatalf("day end %v want %v", got, want)
	}
}

var enf = Enforcement{Enforced: true, DollarsSupported: true}

func TestDecideStartPausesOnMonthBeforeDay(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	l := db.AgentLimits{DayTokens: 100, MonthUSD: 30, DayUSD: 1}
	d := DecideStart(l, db.Spend{Tokens: 500, USD: 5}, db.Spend{USD: 30}, enf, now, time.UTC)
	if d.Allowed || d.Reason != "monthly limit of $30.00 reached" {
		t.Fatalf("%+v", d)
	}
	if !d.PauseUntil.Equal(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("until %v", d.PauseUntil)
	}
	d = DecideStart(l, db.Spend{Tokens: 500}, db.Spend{}, enf, now, time.UTC)
	if d.Allowed || d.Reason != "daily limit of 100 tokens reached" {
		t.Fatalf("%+v", d)
	}
}

func TestDecideStartRemainingIsMinimum(t *testing.T) {
	l := db.AgentLimits{RunTokens: 50000, DayTokens: 500000, RunTurns: 20}
	d := DecideStart(l, db.Spend{Tokens: 490000}, db.Spend{}, enf, time.Now(), time.UTC)
	if !d.Allowed || d.Remaining.Tokens != 10000 || d.Remaining.Turns != 20 {
		t.Fatalf("%+v", d)
	}
	ex, why := d.Remaining.Exceeded(llm.Usage{TotalTokens: 10000}, 1, false)
	// The configured limit, not the 10,000 left of it: that is the setting the
	// owner has to change.
	if !ex || why != "daily limit of 500,000 tokens" {
		t.Fatalf("%v %q", ex, why)
	}
	d = DecideStart(l, db.Spend{}, db.Spend{}, enf, time.Now(), time.UTC)
	_, why = d.Remaining.Exceeded(llm.Usage{TotalTokens: 50000}, 1, false)
	if why != "per-run limit of 50,000 tokens" {
		t.Fatalf("%q", why)
	}
}

func TestDecideIgnoresDollarLimitsWhenCostUnreported(t *testing.T) {
	l := db.AgentLimits{DayUSD: 1, RunUSD: 0.5}
	d := DecideStart(l, db.Spend{USD: 5, CostUnreported: true}, db.Spend{}, enf, time.Now(), time.UTC)
	if !d.Allowed || d.Remaining.USD != 0.5 {
		t.Fatalf("unreported day: %+v", d)
	}
	d = DecideStart(l, db.Spend{USD: 5}, db.Spend{}, Enforcement{Enforced: true}, time.Now(), time.UTC)
	if !d.Allowed || d.Remaining.USD != 0 {
		t.Fatalf("unsupported: %+v", d)
	}
}

func TestDecideNotEnforcedAllowsAll(t *testing.T) {
	d := DecideStart(db.AgentLimits{DayTokens: 1}, db.Spend{Tokens: 99}, db.Spend{}, Enforcement{}, time.Now(), time.UTC)
	if !d.Allowed || d.Remaining != (RunCeiling{}) {
		t.Fatalf("%+v", d)
	}
}

func TestCeilingExceededTurns(t *testing.T) {
	d := DecideStart(db.AgentLimits{RunTurns: 20, RunUSD: 0.5}, db.Spend{}, db.Spend{}, enf, time.Now(), time.UTC)
	if ex, why := d.Remaining.Exceeded(llm.Usage{}, 20, false); !ex || why != "per-run limit of 20 turns" {
		t.Fatalf("%v %q", ex, why)
	}
	if ex, _ := d.Remaining.Exceeded(llm.Usage{}, 19, false); ex {
		t.Fatal("19 turns should pass")
	}
	if ex, why := d.Remaining.Exceeded(llm.Usage{Cost: 0.6, CostReported: true}, 1, true); !ex || why != "per-run limit of $0.50" {
		t.Fatalf("%v %q", ex, why)
	}
	if ex, _ := d.Remaining.Exceeded(llm.Usage{Cost: 0.6}, 1, true); ex {
		t.Fatal("unreported cost must not trip")
	}
}

func TestFormatPauseReason(t *testing.T) {
	if got := FormatPauseReason(PeriodMonth, "$30.00"); got != "monthly limit of $30.00 reached" {
		t.Fatal(got)
	}
	if got := FormatPauseReason(PeriodDay, "500,000 tokens"); got != "daily limit of 500,000 tokens reached" {
		t.Fatal(got)
	}
}

func TestHasPeriodLimit(t *testing.T) {
	if HasPeriodLimit(db.AgentLimits{RunTokens: 1}) || !HasPeriodLimit(db.AgentLimits{MonthUSD: 1}) {
		t.Fatal("wrong")
	}
}

func TestFormatPauseReasonUnknownPeriod(t *testing.T) {
	if got := FormatPauseReason(Period("week"), "1 token"); got != "week limit of 1 token reached" {
		t.Fatal(got)
	}
}

func TestExceededNamesConfiguredUSDLimitNotRemainder(t *testing.T) {
	l := db.AgentLimits{MonthUSD: 30}
	d := DecideStart(l, db.Spend{}, db.Spend{USD: 29.5}, Enforcement{Enforced: true, DollarsSupported: true}, time.Now(), time.UTC)
	ex, why := d.Remaining.Exceeded(llm.Usage{Cost: 0.6, CostReported: true}, 1, true)
	if !ex || why != "monthly limit of $30.00" {
		t.Fatalf("%v %q", ex, why)
	}
}

func TestHasEnforceablePeriodLimit(t *testing.T) {
	usdOnly := db.AgentLimits{DayUSD: 1}
	if HasEnforceablePeriodLimit(usdOnly, Enforcement{Enforced: true}) {
		t.Error("a dollar limit the provider cannot report is not enforceable")
	}
	if !HasEnforceablePeriodLimit(usdOnly, Enforcement{Enforced: true, DollarsSupported: true}) {
		t.Error("a dollar limit with reported cost is enforceable")
	}
	if !HasEnforceablePeriodLimit(db.AgentLimits{MonthTokens: 5}, Enforcement{Enforced: true}) {
		t.Error("a token period limit is enforceable")
	}
	if HasEnforceablePeriodLimit(db.AgentLimits{MonthTokens: 5}, Enforcement{}) {
		t.Error("nothing is enforceable on a CLI coder")
	}
}
