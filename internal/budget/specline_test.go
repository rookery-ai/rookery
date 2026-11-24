package budget

import (
	"testing"

	"github.com/rookery-ai/rookery/internal/db"
)

func TestParseSpecLineRoundTrip(t *testing.T) {
	full := db.AgentLimits{RunTokens: 50000, RunTurns: 20, RunUSD: 0.5, DayTokens: 500000, DayUSD: 2, MonthUSD: 30}
	cases := []struct {
		in      string
		want    db.AgentLimits
		set, ok bool
	}{
		{"none", db.AgentLimits{}, true, true},
		{"None", db.AgentLimits{}, true, true},
		{"unchanged", db.AgentLimits{}, false, true},
		{"", db.AgentLimits{}, false, true},
		{"per run: 50000 tokens, 20 turns, $0.50; per day: 500k tokens, $2; per month: $30", full, true, true},
		{"PER DAY: 500K tokens", db.AgentLimits{DayTokens: 500000}, true, true},
		{"per month: 1,000,000 tokens", db.AgentLimits{MonthTokens: 1000000}, true, true},
		{"per month: 2m tokens, $1,000", db.AgentLimits{MonthTokens: 2000000, MonthUSD: 1000}, true, true},
		{"banana", db.AgentLimits{}, false, false},
		{"per run: 50000", db.AgentLimits{}, false, false},
		{"per day: 20 turns", db.AgentLimits{}, false, false},
		{"per week: $5", db.AgentLimits{}, false, false},
		{"per run: 5 tokens; per run: 6 tokens", db.AgentLimits{}, false, false},
	}
	for _, c := range cases {
		got, set, ok := ParseSpecLine(c.in)
		if got != c.want || set != c.set || ok != c.ok {
			t.Errorf("%q: got %+v set=%v ok=%v", c.in, got, set, ok)
		}
	}
	for _, l := range []db.AgentLimits{full, {}, {MonthTokens: 1000000}} {
		s := FormatSpecLine(l)
		got, set, ok := ParseSpecLine(s)
		if got != l || !set || !ok {
			t.Errorf("round trip %q: %+v", s, got)
		}
	}
	if FormatSpecLine(db.AgentLimits{}) != "none" {
		t.Fatal("zero should be none")
	}
}

func TestParseSpecLineRejectsHostileNumbers(t *testing.T) {
	for _, in := range []string{
		"per run: nan tokens", "per run: inf tokens", "per run: 1e30 tokens", "per run: 1e3 tokens",
		"per month: $nan", "per month: $inf", "per month: $1e30", "per month: $2e9",
		"per run: 2000000000000 tokens", "per run: -5 tokens", "per run: 0 tokens", "per run: $0",
		"per day: 5 tokenz", "per run: 5 turnstile", "per run: 5 tokens, 6 tokens", "per run: $1, $2",
	} {
		if _, _, ok := ParseSpecLine(in); ok {
			t.Errorf("%q should be rejected", in)
		}
	}
}

func TestParseSpecLineToleratesScopeVariation(t *testing.T) {
	cases := []struct {
		in   string
		want db.AgentLimits
	}{
		{"per-run: 5 tokens", db.AgentLimits{RunTokens: 5}},
		{"Per_Run: 5 tokens", db.AgentLimits{RunTokens: 5}},
		{"each run: 3 turns", db.AgentLimits{RunTurns: 3}},
		{"run: 5 token", db.AgentLimits{RunTokens: 5}},
		{"daily: $2", db.AgentLimits{DayUSD: 2}},
		{"each day: 5k tokens", db.AgentLimits{DayTokens: 5000}},
		{"monthly: $30.", db.AgentLimits{MonthUSD: 30}},
		{"per-month: $30", db.AgentLimits{MonthUSD: 30}},
		{"per run: 5 tokens; daily: $1.", db.AgentLimits{RunTokens: 5, DayUSD: 1}},
	}
	for _, c := range cases {
		got, set, ok := ParseSpecLine(c.in)
		if got != c.want || !set || !ok {
			t.Errorf("%q: %+v set=%v ok=%v", c.in, got, set, ok)
		}
	}
	if _, set, ok := ParseSpecLine("none."); !set || !ok {
		t.Error("none. should parse")
	}
	if _, _, ok := ParseSpecLine("daily: 5 tokens; per day: 6 tokens"); ok {
		t.Error("aliased duplicate scope should be rejected")
	}
}

func TestFormatSpecLineKeepsSubCentDollars(t *testing.T) {
	for _, l := range []db.AgentLimits{{RunUSD: 0.005}, {DayUSD: 0.0125}, {MonthUSD: 30}, {RunUSD: 0.5}} {
		s := FormatSpecLine(l)
		if got, _, ok := ParseSpecLine(s); !ok || got != l {
			t.Errorf("%q -> %+v ok=%v", s, got, ok)
		}
	}
	if got := FormatSpecLine(db.AgentLimits{MonthUSD: 30}); got != "per month: $30.00" {
		t.Error(got)
	}
}

// The API refuses a turns limit of 150 or more (the engine's hard ceiling), so the
// designer's line must too: otherwise a plan could save a limit the page then
// rejects on its next save.
func TestParseSpecLineRejectsTurnsAboveTheEngineCeiling(t *testing.T) {
	if l, _, ok := ParseSpecLine("per run: 149 turns"); !ok || l.RunTurns != MaxRunTurns {
		t.Errorf("149 turns should parse, got %+v ok=%v", l, ok)
	}
	for _, in := range []string{"per run: 150 turns", "per run: 1k turns"} {
		if _, _, ok := ParseSpecLine(in); ok {
			t.Errorf("%q should be rejected", in)
		}
	}
}

func TestParseSpecLineScopesNamesOnlyTheScopesWritten(t *testing.T) {
	cases := []struct {
		in    string
		want  db.AgentLimits
		named []Period
	}{
		{"per day: 500k tokens", db.AgentLimits{DayTokens: 500000}, []Period{PeriodDay}},
		{"per run: 5 turns; monthly: $3", db.AgentLimits{RunTurns: 5, MonthUSD: 3}, []Period{PeriodRun, PeriodMonth}},
		// A scope-level "none" names the scope and leaves its fields zero, so an
		// edit can remove one period without restating the others.
		{"per day: none", db.AgentLimits{}, []Period{PeriodDay}},
		{"per day: none; per run: 9 turns", db.AgentLimits{RunTurns: 9}, []Period{PeriodDay, PeriodRun}},
		{"none", db.AgentLimits{}, []Period{PeriodRun, PeriodDay, PeriodMonth}},
	}
	for _, c := range cases {
		got, named, set, ok := ParseSpecLineScopes(c.in)
		if !set || !ok || got != c.want || len(named) != len(c.named) {
			t.Errorf("%q: %+v named=%v set=%v ok=%v", c.in, got, named, set, ok)
			continue
		}
		for i := range named {
			if named[i] != c.named[i] {
				t.Errorf("%q: named=%v want %v", c.in, named, c.named)
			}
		}
	}
	if _, _, _, ok := ParseSpecLineScopes("per day: none, 5 tokens"); ok {
		t.Error("none mixed with a value is ambiguous and must be rejected")
	}
}

func TestMergeLimitScopesKeepsUnnamedScopes(t *testing.T) {
	cur := db.AgentLimits{AgentID: "a", RunTokens: 50000, RunTurns: 20, DayTokens: 500000, DayUSD: 2, MonthUSD: 30}
	got := MergeLimitScopes(cur, db.AgentLimits{DayTokens: 9}, []Period{PeriodDay})
	want := db.AgentLimits{AgentID: "a", RunTokens: 50000, RunTurns: 20, DayTokens: 9, MonthUSD: 30}
	if got != want {
		t.Errorf("merge = %+v, want %+v", got, want)
	}
	if got := MergeLimitScopes(cur, db.AgentLimits{}, []Period{PeriodRun, PeriodDay, PeriodMonth}); got != (db.AgentLimits{AgentID: "a"}) {
		t.Errorf("naming every scope must replace everything: %+v", got)
	}
}
