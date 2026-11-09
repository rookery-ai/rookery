// Package budget is the pure model behind agent spending limits: periods, the
// decision whether a run may start, and the ceiling a started run must stay
// under. It touches no database and no clock, so the runner and the designer
// can both call it and tests can pin every edge.
package budget

import (
	"fmt"
	"strings"
	"time"

	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/llm"
)

// Period is a window a limit applies to.
type Period string

const (
	PeriodRun   Period = "run"
	PeriodDay   Period = "day"
	PeriodMonth Period = "month"
)

// PeriodStart is the start of the window containing now, in loc. Day and month
// windows follow the workspace's zone, not the host's: "daily" means the
// owner's day.
func PeriodStart(p Period, now time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	n := now.In(loc)
	switch p {
	case PeriodDay:
		return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, loc)
	case PeriodMonth:
		return time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, loc)
	}
	return now
}

// PeriodEnd is when the next window begins. A run has no calendar window, so
// its end is now.
func PeriodEnd(p Period, now time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	s := PeriodStart(p, now, loc)
	switch p {
	case PeriodDay:
		return time.Date(s.Year(), s.Month(), s.Day()+1, 0, 0, 0, 0, loc)
	case PeriodMonth:
		return time.Date(s.Year(), s.Month()+1, 1, 0, 0, 0, 0, loc)
	}
	return now
}

// Enforcement says what the active coder lets us hold an agent to.
// Enforced is false when we cannot meter usage at all (a CLI coder reports
// none); DollarsSupported is false when the provider does not report cost.
type Enforcement struct {
	Enforced         bool
	DollarsSupported bool
}

// RunCeiling is the most a single run may consume. 0 in a field means no
// ceiling on that unit. The unexported fields remember which limit produced
// each ceiling, so a stop names the limit actually hit.
type RunCeiling struct {
	Tokens int64
	USD    float64
	Turns  int

	tokSrc, usdSrc, turnSrc Period
}

// StartDecision is whether a run may begin now.
type StartDecision struct {
	Allowed    bool
	PauseUntil time.Time
	Reason     string
	Remaining  RunCeiling
}

// HasPeriodLimit reports whether any day or month limit is set.
func HasPeriodLimit(l db.AgentLimits) bool {
	return l.DayTokens != 0 || l.MonthTokens != 0 || l.DayUSD != 0 || l.MonthUSD != 0
}

// DecideStart applies the period limits to the spend so far. The month is
// checked before the day so the longer pause wins when both are hit.
//
// A dollar limit is skipped when the coder cannot report cost, or when any run
// in that window reported none: a total missing a run's cost would
// under-count, and enforcing it would let the agent overspend while the limit
// looked healthy.
func DecideStart(l db.AgentLimits, day, month db.Spend, e Enforcement, now time.Time, loc *time.Location) StartDecision {
	if !e.Enforced {
		return StartDecision{Allowed: true}
	}
	dayUSD := e.DollarsSupported && !day.CostUnreported
	monUSD := e.DollarsSupported && !month.CostUnreported

	type window struct {
		p         Period
		spend     db.Spend
		tokLimit  int64
		usdLimit  float64
		usdActive bool
	}
	ws := []window{
		{PeriodMonth, month, l.MonthTokens, l.MonthUSD, monUSD},
		{PeriodDay, day, l.DayTokens, l.DayUSD, dayUSD},
	}
	for _, w := range ws {
		if w.tokLimit > 0 && w.spend.Tokens >= w.tokLimit {
			return StartDecision{PauseUntil: PeriodEnd(w.p, now, loc), Reason: FormatPauseReason(w.p, fmtTokens(w.tokLimit))}
		}
		if w.usdActive && w.usdLimit > 0 && w.spend.USD >= w.usdLimit {
			return StartDecision{PauseUntil: PeriodEnd(w.p, now, loc), Reason: FormatPauseReason(w.p, fmtUSD(w.usdLimit))}
		}
	}

	var c RunCeiling
	tok := func(limit int64, p Period) {
		if limit > 0 && (c.Tokens == 0 || limit < c.Tokens) {
			c.Tokens, c.tokSrc = limit, p
		}
	}
	usd := func(limit float64, p Period) {
		if limit > 0 && (c.USD == 0 || limit < c.USD) {
			c.USD, c.usdSrc = limit, p
		}
	}
	tok(l.RunTokens, PeriodRun)
	if l.DayTokens > 0 {
		tok(l.DayTokens-day.Tokens, PeriodDay)
	}
	if l.MonthTokens > 0 {
		tok(l.MonthTokens-month.Tokens, PeriodMonth)
	}
	if e.DollarsSupported {
		usd(l.RunUSD, PeriodRun)
		if dayUSD && l.DayUSD > 0 {
			usd(l.DayUSD-day.USD, PeriodDay)
		}
		if monUSD && l.MonthUSD > 0 {
			usd(l.MonthUSD-month.USD, PeriodMonth)
		}
	}
	if l.RunTurns > 0 {
		c.Turns, c.turnSrc = int(l.RunTurns), PeriodRun
	}
	return StartDecision{Allowed: true, Remaining: c}
}

// Exceeded reports whether a run's usage has reached its ceiling, and which
// limit it was. USD is checked only when dollars apply and the provider
// reported a cost for this usage.
func (c RunCeiling) Exceeded(u llm.Usage, turns int, dollars bool) (bool, string) {
	if c.Tokens > 0 && int64(u.TotalTokens) >= c.Tokens {
		return true, limitName(c.tokSrc) + " of " + fmtTokens(c.Tokens)
	}
	if c.Turns > 0 && turns >= c.Turns {
		return true, limitName(c.turnSrc) + fmt.Sprintf(" of %s turns", fmtInt(int64(c.Turns)))
	}
	if dollars && u.CostReported && c.USD > 0 && u.Cost >= c.USD {
		return true, limitName(c.usdSrc) + " of " + fmtUSD(c.USD)
	}
	return false, ""
}

// FormatPauseReason is the sentence stored when an agent is paused, e.g.
// "monthly limit of $30.00 reached".
func FormatPauseReason(p Period, limit string) string {
	name := map[Period]string{PeriodDay: "daily", PeriodMonth: "monthly", PeriodRun: "per-run"}[p]
	return name + " limit of " + limit + " reached"
}

func limitName(p Period) string {
	switch p {
	case PeriodDay:
		return "daily limit"
	case PeriodMonth:
		return "monthly limit"
	}
	return "per-run limit"
}

func fmtInt(n int64) string {
	s := fmt.Sprintf("%d", n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

func fmtTokens(n int64) string { return fmtInt(n) + " tokens" }
func fmtUSD(v float64) string  { return fmt.Sprintf("$%.2f", v) }
