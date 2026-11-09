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
