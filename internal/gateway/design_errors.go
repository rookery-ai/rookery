package gateway

import (
	"context"
	"errors"

	"github.com/rookery-ai/rookery/internal/coder"
	"github.com/rookery-ai/rookery/internal/llm"
)

// friendlyDesignError turns a design-session failure into something a user can
// act on, and — critically — states whether the session survived.
//
// It used to send the raw Go error: the reported transcript shows
// "Design session error: coder: coder api error: context deadline exceeded",
// which says nothing about what to do and nothing about whether the designer is
// still there. The four messages that followed were answered as ordinary chat
// while the user believed they were still talking to the designer. That
// ambiguity did more damage than the underlying timeout.
//
// Every branch below therefore ends by saying the session is still open, because
// it is: none of these errors clears it.
// FriendlyDesignError is the exported form, used by the build-completion hook in
// main.go to phrase a detached build's failure the same way an inline one is.
func FriendlyDesignError(kind string, err error) string { return friendlyDesignError(kind, err) }

func friendlyDesignError(kind string, err error) string {
	if err == nil {
		return ""
	}

	tail := func() string {
		return " Your " + kind + " design session is still open — reply to carry on, or send /" + kind + " cancel to stop."
	}

	switch {
	case errors.Is(err, coder.ErrUsageLimit), errors.Is(err, llm.ErrQuotaExhausted):
		return "⚠️ Your AI provider is out of credit, so I couldn't finish that step." + tail()
	case errors.Is(err, coder.ErrRateLimited):
		return "⚠️ Your AI provider is rate-limiting us right now. Try again in a moment." + tail()
	case errors.Is(err, coder.ErrAPIAuth):
		return "⚠️ Your AI provider rejected the API key. Check the coder settings in the web dashboard." + tail()
	case errors.Is(err, coder.ErrMaxTurns):
		return "⚠️ That step ran out of turns before finishing. Asking for something simpler usually gets past it." + tail()
	case errors.Is(err, context.DeadlineExceeded):
		// The one from the reported transcript. A build that outlives the coder
		// timeout is no longer lost — the detached build keeps running and reports
		// through the completion hook — but a design CONVERSATION turn that times
		// out still lands here.
		return "⚠️ That step took too long and timed out. Sending it again usually works; if it keeps happening, raise the coder timeout in the web dashboard." + tail()
	case errors.Is(err, context.Canceled):
		return "The " + kind + " session was cancelled."
	}
	return "⚠️ Something went wrong on that step: " + err.Error() + tail()
}
