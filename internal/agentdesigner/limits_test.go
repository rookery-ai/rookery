package agentdesigner

import (
	"testing"

	"github.com/rookery-ai/rookery/internal/db"
)

func specTurn(limitsLine string) db.ChatMessage {
	body := "Tier: 1\nSchedule: none\n"
	if limitsLine != "" {
		body += limitsLine + "\n"
	}
	return db.ChatMessage{Role: "assistant", Content: "Plan.\n\n" + specOpen + "\n" + body + specClose}
}

func limitsFlow(t *testing.T) (*Flow, *db.DB, string) {
	t.Helper()
	database, ws := testDB(t)
	agentID := seedLimitsAgent(t, database, ws)
	return &Flow{db: database}, database, agentID
}

func seedLimitsAgent(t *testing.T, database *db.DB, ws string) string {
	t.Helper()
	id := "agent-limits-1"
	seedAgent(t, database, t.TempDir(), ws, id, "# a", nil)
	return id
}

func TestPersistLimitsFromLatestSpec(t *testing.T) {
	f, database, id := limitsFlow(t)
	hist := []db.ChatMessage{
		{Role: "user", Content: "hi"},
		specTurn("Limits: per run: 50000 tokens, 20 turns; per day: 500000 tokens"),
		{Role: "user", Content: "approve"},
		{Role: "assistant", Content: "Here is the review, no spec."},
	}
	f.persistLimits(id, hist, false)
	l, err := database.GetAgentLimits(id)
	if err != nil {
		t.Fatal(err)
	}
	if l.RunTokens != 50000 || l.RunTurns != 20 || l.DayTokens != 500000 {
		t.Fatalf("limits = %+v", l)
	}
}

func TestPersistLimitsIgnoresUnchangedAndGarbage(t *testing.T) {
	f, database, id := limitsFlow(t)
	if err := database.SaveAgentLimits(db.AgentLimits{AgentID: id, DayTokens: 7}); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"Limits: unchanged", "Limits: lots of money please", ""} {
		f.persistLimits(id, []db.ChatMessage{specTurn(line)}, false)
		l, _ := database.GetAgentLimits(id)
		if l.DayTokens != 7 {
			t.Fatalf("%q changed limits: %+v", line, l)
		}
	}
}

func TestPersistLimitsNoneClears(t *testing.T) {
	f, database, id := limitsFlow(t)
	if err := database.SaveAgentLimits(db.AgentLimits{AgentID: id, DayTokens: 7}); err != nil {
		t.Fatal(err)
	}
	f.persistLimits(id, []db.ChatMessage{specTurn("Limits: none")}, false)
	l, _ := database.GetAgentLimits(id)
	if l.DayTokens != 0 {
		t.Fatalf("none did not clear: %+v", l)
	}
}

// A create takes the line as the whole set: a period it does not name has no limit.
func TestPersistLimitsCreateReplacesEverything(t *testing.T) {
	f, database, id := limitsFlow(t)
	if err := database.SaveAgentLimits(db.AgentLimits{AgentID: id, MonthUSD: 30}); err != nil {
		t.Fatal(err)
	}
	f.persistLimits(id, []db.ChatMessage{specTurn("Limits: per day: 9 tokens")}, false)
	if l, _ := database.GetAgentLimits(id); l != (db.AgentLimits{AgentID: id, DayTokens: 9}) {
		t.Fatalf("create should replace the whole set: %+v", l)
	}
}

// An edit that restates one period must leave the others as they were — the
// designer changing the daily limit used to erase a monthly one nobody mentioned.
func TestPersistLimitsEditMergesPerPeriod(t *testing.T) {
	f, database, id := limitsFlow(t)
	start := db.AgentLimits{AgentID: id, RunTokens: 50000, RunTurns: 20, DayTokens: 500000, MonthUSD: 30}
	if err := database.SaveAgentLimits(start); err != nil {
		t.Fatal(err)
	}
	f.persistLimits(id, []db.ChatMessage{specTurn("Limits: per day: 9 tokens")}, true)
	want := db.AgentLimits{AgentID: id, RunTokens: 50000, RunTurns: 20, DayTokens: 9, MonthUSD: 30}
	if l, _ := database.GetAgentLimits(id); l != want {
		t.Fatalf("edit merge = %+v, want %+v", l, want)
	}

	f.persistLimits(id, []db.ChatMessage{specTurn("Limits: per run: none")}, true)
	want = db.AgentLimits{AgentID: id, DayTokens: 9, MonthUSD: 30}
	if l, _ := database.GetAgentLimits(id); l != want {
		t.Fatalf("a period-level none should clear only that period: %+v", l)
	}

	f.persistLimits(id, []db.ChatMessage{specTurn("Limits: none")}, true)
	if l, _ := database.GetAgentLimits(id); l != (db.AgentLimits{AgentID: id}) {
		t.Fatalf("none on an edit must still clear everything: %+v", l)
	}
}

func TestPersistLimitsEditUnchangedKeepsEverything(t *testing.T) {
	f, database, id := limitsFlow(t)
	start := db.AgentLimits{AgentID: id, DayTokens: 7, MonthUSD: 3}
	if err := database.SaveAgentLimits(start); err != nil {
		t.Fatal(err)
	}
	f.persistLimits(id, []db.ChatMessage{specTurn("Limits: unchanged")}, true)
	if l, _ := database.GetAgentLimits(id); l != start {
		t.Fatalf("unchanged on an edit changed limits: %+v", l)
	}
}

func TestPersistLimitsToleratesDecoratedLabel(t *testing.T) {
	for _, line := range []string{
		"- Limits: per day: 9 tokens",
		"* Limits: per day: 9 tokens",
		"**Limits:** per day: 9 tokens",
		"**Limits**: per day: 9 tokens",
		"**Limits: per day: 9 tokens**",
		"- **Limits:** per day: 9 tokens.",
	} {
		f, database, id := limitsFlow(t)
		f.persistLimits(id, []db.ChatMessage{specTurn(line)}, false)
		if l, _ := database.GetAgentLimits(id); l.DayTokens != 9 {
			t.Errorf("%q not read: %+v", line, l)
		}
	}
}

// The edit design prompt states the agent's current limits in the same grammar
// the line is written in, so the designer can restate what it is keeping.
func TestEditCurrentLimitsFormatsTheAgentsLimits(t *testing.T) {
	f, database, id := limitsFlow(t)
	if err := database.SaveAgentLimits(db.AgentLimits{AgentID: id, DayTokens: 500000, MonthUSD: 30}); err != nil {
		t.Fatal(err)
	}
	got := f.editCurrentLimits(&DesignSession{AgentID: id, IsEdit: true})
	if got != "per day: 500000 tokens; per month: $30.00" {
		t.Fatalf("current limits = %q", got)
	}
	if f.editCurrentLimits(&DesignSession{AgentID: id}) != "" {
		t.Fatal("a create session has no current limits")
	}
}
