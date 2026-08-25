package browser

import "fmt"

// Policy is the permission context one browser call runs under. It is
// assembled by the caller that knows the situation (a run, a build, a chat
// turn) and enforced in exactly one place: CheckAct.
//
// One choke point matters more here than elsewhere. This platform has three
// paths that reach a browser — the API engine's native tools, a CLI coder over
// the loopback bridge, and the designer's feasibility probe — and if they
// enforced permissions independently, changing coder kind would silently change
// what an agent is allowed to do. That is the failure ChatAllowedTools' doc
// comment records, and connectors/MCP both solved it the same way.
type Policy struct {
	// BuildPhase is set during an agent build and during a create-build dry
	// run.
	BuildPhase bool
	// AllowActing is the owner's standing grant for this agent on this session.
	// Default false: an agent can look at a page without being able to touch it.
	AllowActing bool
	// AllowIrreversible additionally permits actions whose control name suggests
	// they cannot be undone. A separate grant, because "let this agent log in
	// and read my bill" and "let this agent pay it" are different decisions.
	AllowIrreversible bool
}

// CheckAct decides whether one acting call may proceed.
//
// The refusals are worded for the MODEL, because the model is who reads them:
// each says what happened and what to do instead, so a weak model reports the
// limitation to the user rather than retrying into the oscillation guard. They
// are returned as errors and surface as ordinary tool results.
func CheckAct(pol Policy, action Action, elementName string) error {
	if !IsMutating(action) {
		return nil
	}

	// The build-phase refusal is a REAL BOUNDARY, and that is the point of it
	// living here. CLAUDE.md records that dryRunSendProhibition is "a PROMPT,
	// not a boundary" — acceptable for a script that might send an email, not
	// for a tool whose entire purpose is clicking buttons. This is the third
	// instance of the pattern buildphase already gates in connectors.Execute
	// and mcp.Execute, and it deliberately still permits reading: a rehearsal
	// that cannot look at the page cannot rehearse.
	if pol.BuildPhase {
		return fmt.Errorf("%w: this is a build/test run, so the browser may read pages but must not click, type or submit. "+
			"Verify what you can by reading, then tell the user that the acting steps will run for real on the first scheduled run",
			ErrActingDisabled)
	}

	if !pol.AllowActing {
		return fmt.Errorf("%w: this agent may read pages in the browser but has not been granted permission to act on them. "+
			"Report this to the user — they can enable acting for this agent on the agent's page. Do not retry",
			ErrActingDisabled)
	}

	if LooksIrreversible(elementName) && !pol.AllowIrreversible {
		return fmt.Errorf("%w: %q looks like an irreversible action (a payment, an order or a deletion) and this agent has not been "+
			"granted permission for those. Stop here and report to the user what you were about to do and why it needs their approval. Do not look for another way to do it",
			ErrActingDisabled, elementName)
	}
	return nil
}
