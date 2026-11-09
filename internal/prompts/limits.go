package prompts

// Spending limits in the design conversation. Everything here is emitted only
// when DesignSystemParams.LimitsAvailable: limits are enforced from the API
// engine's per-call usage, so on a CLI coder the question would promise a
// guarantee nothing can keep.

// limitsGrammar is the exact shape agentdesigner/budget.ParseSpecLine reads.
// Stated once so the create and edit prompts cannot drift from the parser; a
// value it rejects is silently not saved.
func limitsGrammar(p DesignSystemParams) string {
	if p.LimitsDollars {
		return "per run: 50000 tokens, 20 turns, $0.50; per day: 500000 tokens, $2; per month: $30"
	}
	return "per run: 50000 tokens, 20 turns; per day: 500000 tokens"
}

func limitsQuestion(p DesignSystemParams) string {
	if !p.LimitsAvailable {
		return ""
	}
	s := `6. Whether the user wants spending limits for this assistant. ASK once, near the end:
   "Do you want a cap on how much this assistant can use?" Then SUGGEST concrete values
   that fit what it does and how often it runs (a small daily digest needs far less than
   a research task run every hour), for each of: one run, one day, one month. Explain in
   plain words that usage is counted in "tokens" — roughly three-quarters of a word each,
   covering everything the assistant reads and writes — and say what a typical run of
   THIS assistant is likely to use. A run may also be capped in "turns" (steps it takes).
`
	if p.LimitsDollars {
		s += `   Dollar limits are available too; offer them alongside token limits.
`
	} else {
		s += `   Offer token limits only — this provider does not report prices, so never offer or
   promise a dollar limit.
`
	}
	s += `   "No limits" is a valid answer; never press for one.
`
	return s
}

func limitsPlanBullet(p DesignSystemParams) string {
	if !p.LimitsAvailable {
		return ""
	}
	return "- Any spending limits agreed (per run / per day / per month), or that there are none\n"
}

func createLimitsSpecLine(p DesignSystemParams) string {
	if !p.LimitsAvailable {
		return ""
	}
	return "Limits: none | " + limitsGrammar(p) + " — only the limits the user agreed to, written in exactly this form (turns apply to a run only); \"none\" when they want no limits.\n"
}

// editLimitsSpecLine matches how an edit saves the line: each period written
// (per run / per day / per month) replaces that period's limits, and a period
// left out keeps what it has. That is why the agent's current limits are stated —
// so the designer can see what survives — and why removing ONE period needs its
// own "per day: none" rather than an omission.
func editLimitsSpecLine(p DesignSystemParams) string {
	if !p.LimitsAvailable {
		return ""
	}
	current := p.CurrentLimits
	if current == "" {
		current = "none"
	}
	return "Limits: unchanged | none | " + limitsGrammar(p) + " — the assistant's limits are currently: " + current +
		". Write \"unchanged\" unless the user asked to change the spending limits in this conversation. Otherwise write only the periods that change: each period you write replaces that period's limits, a period you leave out keeps its current limits, \"per day: none\" removes just that period's limits, and \"none\" alone removes them all.\n"
}
