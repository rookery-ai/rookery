package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ilijad1/rookery/internal/coder"
	"github.com/ilijad1/rookery/internal/llm"
)

// TestFriendlyDesignErrorAlwaysSaysTheSessionSurvived is the property that
// matters most, and the one the reported transcript actually lacked.
//
// The raw error ("coder: coder api error: context deadline exceeded") said
// nothing about whether the designer was still there. The four messages the user
// sent afterwards were answered as ordinary chat while they believed they were
// still talking to the designer — that ambiguity did more damage than the
// timeout itself.
func TestFriendlyDesignErrorAlwaysSaysTheSessionSurvived(t *testing.T) {
	recoverable := []error{
		coder.ErrUsageLimit,
		coder.ErrRateLimited,
		coder.ErrAPIAuth,
		coder.ErrMaxTurns,
		llm.ErrQuotaExhausted,
		context.DeadlineExceeded,
		errors.New("something entirely unexpected"),
	}
	for _, err := range recoverable {
		msg := friendlyDesignError("agent", err)
		if !strings.Contains(msg, "still open") {
			t.Errorf("%v → %q, want it to say the session is still open", err, msg)
		}
		if !strings.Contains(msg, "/agent cancel") {
			t.Errorf("%v → %q, want it to name the way out", err, msg)
		}
	}
}

// TestFriendlyDesignErrorExplainsTheCause: a user cannot act on a Go error
// string. Each sentinel must produce guidance naming what to do next.
func TestFriendlyDesignErrorExplainsTheCause(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{coder.ErrUsageLimit, "out of credit"},
		{llm.ErrQuotaExhausted, "out of credit"},
		{coder.ErrRateLimited, "rate-limiting"},
		{coder.ErrAPIAuth, "API key"},
		{coder.ErrMaxTurns, "ran out of turns"},
		{context.DeadlineExceeded, "timed out"},
	} {
		got := friendlyDesignError("agent", tc.err)
		if !strings.Contains(got, tc.want) {
			t.Errorf("friendlyDesignError(%v) = %q, want it to mention %q", tc.err, got, tc.want)
		}
		// The raw Go error must not leak into a message we have a translation for.
		if strings.Contains(got, "coder:") || strings.Contains(got, "llm:") {
			t.Errorf("friendlyDesignError(%v) leaked the raw error: %q", tc.err, got)
		}
	}
}

// A wrapped sentinel must still be recognised — the coder wraps these on the way
// out, which is exactly how the transcript's message ended up as a raw string.
func TestFriendlyDesignErrorUnwraps(t *testing.T) {
	wrapped := fmt.Errorf("coder: coder api error: %w", context.DeadlineExceeded)
	if got := friendlyDesignError("agent", wrapped); !strings.Contains(got, "timed out") {
		t.Errorf("wrapped deadline error = %q, want the timeout translation", got)
	}
}

func TestFriendlyDesignErrorHandlesNilAndCancel(t *testing.T) {
	if got := friendlyDesignError("agent", nil); got != "" {
		t.Errorf("nil error = %q, want empty", got)
	}
	// A cancel is the user's own action, so it must NOT claim the session lives.
	got := friendlyDesignError("skill", context.Canceled)
	if !strings.Contains(got, "cancelled") || strings.Contains(got, "still open") {
		t.Errorf("cancel = %q, want a cancellation notice", got)
	}
}

// The kind is woven into the guidance, so a skill session must not tell the user
// to send /agent cancel.
func TestFriendlyDesignErrorNamesTheRightCommand(t *testing.T) {
	got := friendlyDesignError("skill", coder.ErrRateLimited)
	if !strings.Contains(got, "/skill cancel") {
		t.Errorf("skill error = %q, want it to name /skill cancel", got)
	}
	if strings.Contains(got, "/agent") {
		t.Errorf("skill error = %q, must not name /agent", got)
	}
}

// FriendlyDesignError is the exported entry point the build-completion hook uses,
// so a detached failure is phrased exactly like an inline one.
func TestExportedFriendlyDesignErrorMatches(t *testing.T) {
	if FriendlyDesignError("agent", coder.ErrMaxTurns) != friendlyDesignError("agent", coder.ErrMaxTurns) {
		t.Error("exported and unexported forms must agree")
	}
}
