package coder

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// The two fallbacks must agree. They are written in different packages —
// config.defaults() and coder.New — and a caller reaches one or the other
// depending on how it constructs the coder, so a mismatch would make the
// effective timeout depend on the construction path rather than on
// configuration.
func TestDefaultTimeoutIsThirtyMinutes(t *testing.T) {
	if DefaultTimeout != 30*time.Minute {
		t.Errorf("DefaultTimeout = %s, want 30m", DefaultTimeout)
	}
	c := New("claude", 0, t.TempDir(), t.TempDir())
	if c.Timeout() != DefaultTimeout {
		t.Errorf("New with a zero timeout = %s, want %s", c.Timeout(), DefaultTimeout)
	}
}

func TestWorkspaceTimeoutOverridesTheDefault(t *testing.T) {
	c := New("claude", 90*time.Second, t.TempDir(), t.TempDir())
	if c.Timeout() != 90*time.Second {
		t.Errorf("Timeout() = %s, want 90s", c.Timeout())
	}
}

// The retry ceiling must sit above the 120s the settings form used to write —
// that is the population it exists for — and below the default, or every
// ordinary timeout would be retried at full cost.
func TestRetryCeilingCoversTheOldCapAndNotTheDefault(t *testing.T) {
	if RetryTimeoutBelow <= 120*time.Second {
		t.Errorf("RetryTimeoutBelow = %s, must exceed the 120s cap it exists to rescue",
			RetryTimeoutBelow)
	}
	if RetryTimeoutBelow >= DefaultTimeout {
		t.Errorf("RetryTimeoutBelow = %s must be under DefaultTimeout %s, or a build on the "+
			"default would be retried for another full timeout", RetryTimeoutBelow, DefaultTimeout)
	}
}

// The sentinel is what the designer's retry branches on. The message must stay
// byte-identical to the old fmt.Errorf text, because other call sites still
// match "timed out" as a substring.
func TestErrTimeoutWrapsAndKeepsItsMessage(t *testing.T) {
	err := fmt.Errorf("%w after %s", ErrTimeout, 20*time.Minute)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("errors.Is(err, ErrTimeout) = false for %v", err)
	}
	if got, want := err.Error(), "coder timed out after 20m0s"; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}
