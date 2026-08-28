package coder

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rookery-ai/rookery/internal/browser"
)

// waitingBrowser implements both Renderer and the polling waiter, recording the
// order in which things happened — which is the property under test.
type waitingBrowser struct {
	fakeBrowser
	matched   bool
	waitCalls int
	events    *[]string
}

func (w *waitingBrowser) WaitFor(_ context.Context, _, _ string, _ time.Duration) (browser.Result, bool, error) {
	w.waitCalls++
	*w.events = append(*w.events, "waited")
	return browser.Result{Title: "Payment confirmed"}, w.matched, nil
}

func newWaitingSet(matched bool) (*hostToolSet, *[]string) {
	events := &[]string{}
	h := &hostToolSet{
		browser:          &waitingBrowser{fakeBrowser: fakeBrowser{available: true}, matched: matched, events: events},
		includeExecTools: true,
		notifyUser: func(msg string) {
			*events = append(*events, "notified: "+msg)
		},
	}
	return h, events
}

// The whole point of the notify parameter: the user is told BEFORE the wait
// starts. Told afterwards, the message arrives after the thing it was asking
// them to do — which is what [CHAT] already did, and why this exists.
func TestTheUserIsNotifiedBeforeTheWaitBegins(t *testing.T) {
	h, events := newWaitingSet(true)
	h.execBrowserWait(context.Background(), "s1", browserCallArgs{
		WaitFor:   "text:Payment confirmed",
		Notify:    "Approve the payment in your banking app",
		TimeoutMS: 600000,
	})
	if len(*events) != 2 {
		t.Fatalf("events = %v, want a notify then a wait", *events)
	}
	if !strings.HasPrefix((*events)[0], "notified:") {
		t.Errorf("the wait ran before the user was told: %v", *events)
	}
	if (*events)[1] != "waited" {
		t.Errorf("no wait happened: %v", *events)
	}
}

func TestNoNotificationIsSentWhenNoneWasAskedFor(t *testing.T) {
	h, events := newWaitingSet(true)
	h.execBrowserWait(context.Background(), "s1", browserCallArgs{WaitFor: "networkidle"})
	for _, e := range *events {
		if strings.HasPrefix(e, "notified:") {
			t.Fatalf("an unrequested message was sent to the user: %v", *events)
		}
	}
}

// A surface with no notifier must SAY so. A model told its message was delivered
// will not repeat it at the end of the run, and the user would then never learn
// why the agent sat waiting.
func TestAnUndeliverableNotificationIsReportedToTheModel(t *testing.T) {
	events := &[]string{}
	h := &hostToolSet{
		browser:          &waitingBrowser{fakeBrowser: fakeBrowser{available: true}, matched: true, events: events},
		includeExecTools: true,
		// notifyUser deliberately nil
	}
	out := h.execBrowserWait(context.Background(), "s1", browserCallArgs{
		WaitFor: "text:done", Notify: "tap approve",
	})
	if !strings.Contains(out, "could not notify") {
		t.Errorf("the model was not told its message went nowhere: %q", out)
	}
}

// An unmet wait must not be shaped as a failing call: the engine's oscillation
// guard counts an "error:" prefix as a failure worth short-circuiting, and the
// model needs to report the outcome rather than treat the tool as broken.
func TestAnUnmetWaitIsNotAFailingCall(t *testing.T) {
	h, _ := newWaitingSet(false)
	out := h.execBrowserWait(context.Background(), "s1", browserCallArgs{WaitFor: "text:Payment confirmed"})
	if strings.HasPrefix(out, "error:") {
		t.Fatalf("an unmet wait was shaped as a failing call: %q", out)
	}
	if !strings.Contains(out, "without") {
		t.Errorf("the result does not say the condition never appeared: %q", out)
	}
	// It must also steer away from simply waiting again, which is what a model
	// does by default and which would burn the run's turn budget.
	if !strings.Contains(strings.ToLower(out), "do not simply wait again") {
		t.Errorf("the result does not discourage a blind retry: %q", out)
	}
}
