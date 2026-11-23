package db

import (
	"database/sql"
	"errors"
	"time"
)

// budgetTimeFmt is the format every timestamp in this database uses
// (datetime('now') in UTC), so budget_paused_until compares and parses like the
// rest.
const budgetTimeFmt = "2006-01-02 15:04:05"

// AgentLimits are an agent's spending limits. 0 in any field means that limit is
// unset; a row that is entirely zero does not exist.
type AgentLimits struct {
	AgentID                  string
	RunTokens, RunTurns      int64
	DayTokens, MonthTokens   int64
	RunUSD, DayUSD, MonthUSD float64
}

func (l AgentLimits) isZero() bool {
	return l.RunTokens == 0 && l.RunTurns == 0 && l.DayTokens == 0 && l.MonthTokens == 0 &&
		l.RunUSD == 0 && l.DayUSD == 0 && l.MonthUSD == 0
}

// GetAgentLimits returns the agent's limits, or the zero value (AgentID set) when
// it has none.
func (d *DB) GetAgentLimits(agentID string) (AgentLimits, error) {
	l := AgentLimits{AgentID: agentID}
	err := d.QueryRow(`SELECT run_tokens,run_turns,run_usd,day_tokens,day_usd,month_tokens,month_usd
		FROM agent_limits WHERE agent_id=?`, agentID).
		Scan(&l.RunTokens, &l.RunTurns, &l.RunUSD, &l.DayTokens, &l.DayUSD, &l.MonthTokens, &l.MonthUSD)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentLimits{AgentID: agentID}, nil
	}
	return l, err
}

// SaveAgentLimits upserts the limits. An all-zero value deletes the row, so
// "no limits" has exactly one representation.
func (d *DB) SaveAgentLimits(l AgentLimits) error {
	if l.isZero() {
		_, err := d.Exec(`DELETE FROM agent_limits WHERE agent_id=?`, l.AgentID)
		return err
	}
	_, err := d.Exec(`INSERT INTO agent_limits(agent_id,run_tokens,run_turns,run_usd,day_tokens,day_usd,month_tokens,month_usd,updated_at)
		VALUES(?,?,?,?,?,?,?,?,datetime('now'))
		ON CONFLICT(agent_id) DO UPDATE SET run_tokens=excluded.run_tokens, run_turns=excluded.run_turns,
			run_usd=excluded.run_usd, day_tokens=excluded.day_tokens, day_usd=excluded.day_usd,
			month_tokens=excluded.month_tokens, month_usd=excluded.month_usd, updated_at=datetime('now')`,
		l.AgentID, l.RunTokens, l.RunTurns, l.RunUSD, l.DayTokens, l.DayUSD, l.MonthTokens, l.MonthUSD)
	return err
}

// Spend is what an agent's runs consumed over a window.
type Spend struct {
	Tokens int64
	USD    float64
	// CostUnreported is true when any summed run used tokens but reported no cost
	// (a CLI coder, or a provider that does not say). USD is then a lower bound,
	// and a dollar limit cannot be trusted to have been checked.
	CostUnreported bool
}

// AgentSpendSince sums the agent's runs started at or after since.
func (d *DB) AgentSpendSince(agentID string, since time.Time) (Spend, error) {
	var s Spend
	var unreported int
	err := d.QueryRow(`SELECT COALESCE(SUM(total_tokens),0), COALESCE(SUM(cost_usd),0),
			COALESCE(MAX(CASE WHEN cost_reported=0 AND total_tokens>0 THEN 1 ELSE 0 END),0)
		FROM agent_runs WHERE agent_id=? AND started_at >= ?`,
		agentID, since.UTC().Format(budgetTimeFmt)).Scan(&s.Tokens, &s.USD, &unreported)
	s.CostUnreported = unreported == 1
	return s, err
}

// SetAgentBudgetPause pauses the agent until the given time. changed is false
// when the same until was already set, so the caller notifies once per pause
// rather than once per skipped run.
func (d *DB) SetAgentBudgetPause(agentID string, until time.Time, reason string) (bool, error) {
	u := until.UTC().Format(budgetTimeFmt)
	res, err := d.Exec(`UPDATE agents SET budget_paused_until=?, budget_pause_reason=?, updated_at=datetime('now')
		WHERE id=? AND budget_paused_until <> ?`, u, reason, agentID, u)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ClearAgentBudgetPause lifts the pause.
func (d *DB) ClearAgentBudgetPause(agentID string) error {
	_, err := d.Exec(`UPDATE agents SET budget_paused_until='', budget_pause_reason='' WHERE id=?`, agentID)
	return err
}

// MarkLimitNoticeSent records that the limit notice went out for day (any stable
// per-day string). first is true only for the call that recorded it, so
// concurrent skipped runs send one notice between them.
func (d *DB) MarkLimitNoticeSent(agentID, day string) (bool, error) {
	res, err := d.Exec(`UPDATE agents SET limit_notice_day=? WHERE id=? AND limit_notice_day <> ?`, day, agentID, day)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
