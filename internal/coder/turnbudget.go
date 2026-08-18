package coder

// turnBudget decides when a tool-calling loop has gone on long enough.
//
// A fixed turn cap cannot tell a runaway loop from legitimately long work — they
// look identical by turn count. It also caused a real incident: an agent that
// genuinely needed more than 25 turns hit the cap, the grace turn stripped its
// tools, and the model expressed a still-pending tool call as raw text which was
// then delivered to the user. Budgeting on PROGRESS separates the two cases.
//
// Three limits, in the order they usually bite:
//
//   - unproductive streak — a model going nowhere stops in 6 turns, far sooner than
//     any base budget would allow.
//   - base budget — spent only by turns that achieved nothing, so honest work does
//     not consume it.
//   - hard ceiling — pure runaway protection, never extended by anything.
type turnBudget struct {
	base        int
	spent       int
	streak      int
	turns       int
	hardCeiling int
}

func newTurnBudget(isBuild bool) *turnBudget {
	base := maxAPITurns
	if isBuild {
		base = maxBuildAPITurns
	}
	return &turnBudget{base: base, hardCeiling: maxHardTurns}
}

// iterate is called at the TOP of every loop iteration and counts it, whatever the
// iteration goes on to do.
//
// It is separate from next() so the loop is bounded BY CONSTRUCTION. Two paths in
// runToolLoop `continue` without ever executing a tool call — the tools-unsupported
// degrade and the verify-finish nudge — and so never reach next(). Both are bounded
// by their own counters today, but relying on that would make an unbounded `for`
// safe only by coincidence, and a future third path would spin forever.
func (b *turnBudget) iterate() (stop bool, reason string) {
	b.turns++
	if b.turns > b.hardCeiling {
		return true, "hard-ceiling"
	}
	return false, ""
}

// next records the OUTCOME of a turn that actually ran tool calls, and reports
// whether the loop must stop. productive means the turn executed at least one tool
// call that succeeded and was not a short-circuited repeat.
func (b *turnBudget) next(productive bool) (stop bool, reason string) {
	if productive {
		b.streak = 0
	} else {
		b.streak++
		b.spent++
	}

	switch {
	case b.streak >= maxUnproductiveStreak:
		return true, "unproductive"
	case b.spent >= b.base:
		return true, "budget"
	}
	return false, ""
}
