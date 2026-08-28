package browser

import "fmt"

// Policy is the permission context one browser call runs under, enforced in
// exactly one place: CheckAct.
//
// One choke point matters more here than elsewhere. Three paths reach a browser
// — the API engine's native tools, a CLI coder over the loopback bridge, and the
// designer's feasibility probe — and if they enforced permissions independently,
// changing coder kind would silently change what an agent is allowed to do.
//
// There used to be a second, lower tier: a grant for clicking and typing AT ALL.
// It was removed after testing showed it gated nothing real. An agent asked to
// log into a site simply did it with `bash` and `curl` — eleven calls, no browser
// tool touched — so the switch withheld one route to an action the agent could
// perform anyway by another. Asking an owner to approve "clicking" for a task
// they had just described in words was friction that bought no safety, while
// leaving the impression that something was being guarded.
//
// What remains is the decision that is genuinely the owner's: whether this agent
// may do something that cannot be undone.
type Policy struct {
	// BuildPhase is set during an agent build and during a create-build dry run.
	BuildPhase bool
	// AllowIrreversible permits actions judged irreversible — paying, ordering,
	// transferring, deleting. Default false. This is now the ONLY browser
	// permission, which changes what the judgement below has to carry.
	AllowIrreversible bool
}

// PageContext is what the action is about to happen ON.
//
// It exists because the control's own name is not always available or
// meaningful: browser_press has no control at all, and plenty of real buttons
// carry no accessible name. Judging the page as well as the control is what
// stops "press Enter on a focused payment form" from walking past a check aimed
// at a button labelled "Pay".
type PageContext struct {
	Title string
	URL   string
	// NameKnown reports whether the caller could identify the control being
	// acted on. False for a keypress, or for a ref whose element has no
	// accessible name.
	NameKnown bool
}

// CheckAct decides whether one acting call may proceed.
//
// Refusals are worded for the MODEL, because the model is who reads them: each
// says what happened and what to do instead, so it reports the limitation to the
// user rather than retrying into the oscillation guard.
func CheckAct(pol Policy, action Action, elementName string, page PageContext) error {
	if !IsMutating(action) {
		return nil
	}

	risky, why := judgeIrreversible(action, elementName, page)

	// During a build or a create-build dry run, ordinary interaction is
	// PERMITTED — a rehearsal that cannot log in cannot rehearse a
	// login-and-read agent, which is most of them. Only the irreversible step is
	// held back, and the model is told to describe it rather than perform it, so
	// the review still shows what the agent would do.
	//
	// This is a deliberate loosening of the earlier rule, which refused every
	// mutating call at build time. The cost is real and worth stating: a
	// rehearsal now genuinely fills forms and clicks through pages. The
	// alternative was a rehearsal that proved nothing about the one part of the
	// agent anybody doubts.
	if pol.BuildPhase {
		if !risky {
			return nil
		}
		return fmt.Errorf("%w: this is a test run, so the irreversible step must not actually happen (%s). "+
			"Do NOT try another way to do it. Instead, finish by telling the user exactly what you would have done "+
			"at this point in a real run — which button, on which page, and what it would cause",
			ErrActingDisabled, why)
	}

	if risky && !pol.AllowIrreversible {
		return fmt.Errorf("%w: this looks like an action that cannot be undone (%s), and this agent has not been "+
			"given permission for those. Stop here and tell the user what you were about to do and that they can "+
			"allow it on this agent's page. Do not look for another way around it",
			ErrActingDisabled, why)
	}
	return nil
}

// judgeIrreversible decides whether an action needs the owner's permission, and
// says why in words the refusal can quote back.
//
// It is the WHOLE guard now that the lower tier is gone, which changes the
// standard it has to meet. As a second layer, a miss cost nothing that the first
// tier was not already gating; as the only layer, a miss is a real click on a
// real payment button. So it errs toward asking:
//
//   - a control whose name reads as irreversible → ask
//   - ANY mutating action on a page that reads as a checkout, payment, order or
//     account-deletion page → ask, whatever the control is called
//   - a submit-shaped keypress, or a click on a control that could not be
//     identified, while on such a page → ask
//
// The page test is what closes the two holes a name-only test leaves: an unnamed
// button, and browser_press("Enter") on a focused form, which has no control
// name to judge at all.
func judgeIrreversible(action Action, elementName string, page PageContext) (bool, string) {
	if LooksIrreversible(elementName) {
		return true, "the control is called " + quoteName(elementName)
	}
	if pageLooksIrreversible(page) {
		switch {
		case action == ActionPress:
			return true, "a keypress can submit the form on this page, and the page looks like a payment or order page"
		case !page.NameKnown:
			return true, "this control could not be identified, and the page looks like a payment or order page"
		default:
			return true, "the page looks like a payment or order page"
		}
	}
	return false, ""
}

func quoteName(s string) string {
	if s == "" {
		return "unnamed"
	}
	return "\"" + s + "\""
}
