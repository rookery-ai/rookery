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
	f.persistLimits(id, hist)
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
		f.persistLimits(id, []db.ChatMessage{specTurn(line)})
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
	f.persistLimits(id, []db.ChatMessage{specTurn("Limits: none")})
	l, _ := database.GetAgentLimits(id)
	if l.DayTokens != 0 {
		t.Fatalf("none did not clear: %+v", l)
	}
}
