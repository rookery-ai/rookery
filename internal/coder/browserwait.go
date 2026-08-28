package coder

import (
	"context"
	"strings"
	"time"

	"github.com/rookery-ai/rookery/internal/browser"
)

// execBrowserWait runs a long, poll-backed wait, announcing it first when the
// wait depends on the user doing something.
//
// The announcement is the part that needed new plumbing, and the reason is worth
// keeping. [CHAT] is only delivered durably when a run ENDS, and the scheduler
// wires no live-progress sink at all — so an agent that stopped at a payment step
// to wait for a bank push could not tell anyone until after the wait it was
// asking them to act on. On a 03:00 run that message reached nobody, ten minutes
// late.
func (h *hostToolSet) execBrowserWait(ctx context.Context, session string, a browserCallArgs) string {
	if h.browser == nil {
		return "error: the browser is not available on this server"
	}

	notified := ""
	if msg := strings.TrimSpace(a.Notify); msg != "" {
		if h.notifyUser == nil {
			// Say so rather than swallowing it. A model told its message was sent
			// will not repeat it at the end of the run, and the user would then
			// never learn why the agent was waiting.
			notified = "\n(could not notify the user on this surface — say what you were waiting for in your final message instead)"
		} else {
			h.notifyUser(msg)
			notified = "\n(the user has been told: " + msg + ")"
		}
	}

	waiter, ok := h.browser.(browserWaiter)
	if !ok {
		// A renderer without the polling loop (a test fake) keeps the old
		// single-shot behaviour rather than failing.
		return h.execBrowserAct(ctx, browser.ActRequest{
			Action: browser.ActionWait, WaitFor: a.WaitFor, TimeoutMS: a.TimeoutMS,
		}, "", browser.PageContext{}) + notified
	}

	res, matched, err := waiter.WaitFor(ctx, session, a.WaitFor,
		time.Duration(a.TimeoutMS)*time.Millisecond)
	if err != nil {
		return browserErrorResult(err)
	}
	if !matched {
		// NOT an "error:" — a wait that ends without the thing happening is a
		// finding the model must act on, and the oscillation guard counts that
		// prefix as a failing call worth short-circuiting.
		return "the wait ended without \"" + a.WaitFor + "\" appearing." + notified +
			"\nThe page has not changed. Do not simply wait again — tell the user what did not happen, " +
			"or read the page to see where it actually got to."
	}
	return renderBrowserResult(res, true) + notified
}

// browserWaiter is the polling wait, kept as its own interface so the narrow
// Renderer contract every other consumer depends on does not grow a method only
// this caller uses.
type browserWaiter interface {
	WaitFor(ctx context.Context, session, condition string, total time.Duration) (browser.Result, bool, error)
}
