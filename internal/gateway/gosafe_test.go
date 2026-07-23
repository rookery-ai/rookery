package gateway

import (
	"testing"
	"time"
)

// TestGoSafeRecoversFromPanic is the regression guard for the finding that a
// background goroutine spawned off the message path (e.g. sendAutoDelete's
// 30s deletion timer) is NOT covered by dispatchFunc's recover — recover()
// only unwinds the panicking goroutine's own stack, and by the time a
// detached goroutine runs, dispatch has already returned. goSafe must catch
// the panic itself so it never reaches the runtime and kills the process.
//
// The test proves this behaviorally: it spawns a goSafe goroutine that
// signals it started (so the test knows the goroutine is really running,
// not skipped), then panics. If goSafe did not recover, the panic would
// propagate out of the goroutine and crash the entire test binary — so the
// mere fact that this test (and any test after it) completes normally is
// itself the assertion. We also confirm the goroutine actually ran to avoid
// a false-pass from a fn that silently never got called.
func TestGoSafeRecoversFromPanic(t *testing.T) {
	m := &GatewayManager{}

	started := make(chan struct{})
	done := make(chan struct{})

	m.goSafe("t", func() {
		close(started)
		defer close(done)
		panic("boom")
	})

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("goSafe goroutine never started")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("goSafe goroutine's fn did not run to completion (panic before its defer?)")
	}

	// If the panic had escaped goSafe's recover, the process would already be
	// dead by now. Reaching here — and being able to run more code, including
	// another goroutine spawn — is the actual proof the guard held.
	proof := make(chan struct{})
	m.goSafe("t2", func() { close(proof) })
	select {
	case <-proof:
	case <-time.After(2 * time.Second):
		t.Fatal("process/manager not usable after a recovered goSafe panic")
	}
}

// TestGoSafeRunsFnNormally is a sanity check that goSafe doesn't interfere
// with a non-panicking fn's execution or its context.
func TestGoSafeRunsFnNormally(t *testing.T) {
	m := &GatewayManager{}
	done := make(chan struct{})
	var ran bool
	m.goSafe("t", func() {
		ran = true
		close(done)
	})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("goSafe did not run fn")
	}
	if !ran {
		t.Fatal("fn did not run")
	}
}
