package browser

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeWaitMgr stands in for the helper so the polling loop can be driven without
// a browser: it reports "not yet" a few times, then matches.
type fakeWaitMgr struct {
	callsUntilMatch int
	calls           int
	segments        []int // TimeoutMS seen per call
	fatalAfter      int   // >0: return a non-timeout error on that call
}

func (f *fakeWaitMgr) act(req ActRequest) (Result, error) {
	f.calls++
	f.segments = append(f.segments, req.TimeoutMS)
	if f.fatalAfter > 0 && f.calls >= f.fatalAfter {
		return Result{}, errors.New("browser helper unreachable: connection refused")
	}
	if f.calls >= f.callsUntilMatch {
		return Result{Title: "Payment confirmed"}, nil
	}
	return Result{}, errors.New("wait failed: playwright: timeout: Timeout 20000ms exceeded")
}

// waitLoop mirrors Manager.WaitFor over the fake, so the loop's decisions are
// tested without spawning a helper. Kept deliberately close to the real one; the
// properties asserted are the ones that made a single long wait dangerous.
func waitLoop(t *testing.T, f *fakeWaitMgr, total time.Duration) (bool, error) {
	t.Helper()
	if total <= 0 || total > MaxWaitFor {
		total = MaxWaitFor
	}
	deadline := time.Now().Add(total)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false, nil
		}
		seg := waitSegment
		if remaining < seg {
			seg = remaining
		}
		_, err := f.act(ActRequest{Action: ActionWait, TimeoutMS: int(seg / time.Millisecond)})
		if err == nil {
			return true, nil
		}
		if isFatalWaitErr(err) {
			return false, err
		}
	}
}

// The property that makes long waits safe: no SINGLE call may approach the
// manager's 3-minute transport timeout, because a call that exceeds it fails at
// the transport and the error path stops the helper — destroying the page and
// any login on it.
func TestEachWaitSegmentStaysWellUnderTheTransportTimeout(t *testing.T) {
	f := &fakeWaitMgr{callsUntilMatch: 4}
	if _, err := waitLoop(t, f, 5*time.Minute); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if f.calls != 4 {
		t.Fatalf("polled %d times, want 4", f.calls)
	}
	for i, ms := range f.segments {
		if ms > 60_000 {
			t.Errorf("segment %d asked for %dms — too close to the 3-minute transport ceiling", i, ms)
		}
	}
}

// A timeout inside one segment means "not yet", not "broken". Treating it as
// fatal would end the wait on the first poll, which is every poll but the last.
func TestATimedOutSegmentKeepsWaiting(t *testing.T) {
	if isFatalWaitErr(errors.New("wait failed: playwright: timeout: Timeout 20000ms exceeded")) {
		t.Error("a segment timeout was treated as fatal, so the loop would stop on the first poll")
	}
}

// A dead helper must stop the loop immediately. Retrying against a helper that
// has gone would burn the entire deadline achieving nothing.
func TestADeadHelperStopsTheWait(t *testing.T) {
	if !isFatalWaitErr(errors.New("browser helper unreachable: connection refused")) {
		t.Error("a dead helper was treated as a retryable timeout")
	}
	f := &fakeWaitMgr{callsUntilMatch: 99, fatalAfter: 2}
	matched, err := waitLoop(t, f, time.Minute)
	if matched || err == nil {
		t.Fatalf("matched=%v err=%v, want an error and no match", matched, err)
	}
	if f.calls != 2 {
		t.Errorf("kept polling a dead helper: %d calls", f.calls)
	}
}

// Reaching the deadline without the condition is an ANSWER, not an error. The
// engine's oscillation guard counts an error as a failing call, and the model
// needs to report "it did not arrive" rather than treat the tool as broken.
func TestAnUnmetConditionIsNotAnError(t *testing.T) {
	f := &fakeWaitMgr{callsUntilMatch: 1 << 30}
	matched, err := waitLoop(t, f, 40*time.Millisecond)
	if err != nil {
		t.Fatalf("an unmet wait returned an error: %v", err)
	}
	if matched {
		t.Fatal("reported a match that never happened")
	}
}

// A caller asking for longer than the cap gets the cap, not an unbounded wait
// that would outlive the run and hold a scheduler slot.
func TestWaitIsBoundedByTheMaximum(t *testing.T) {
	if MaxWaitFor > 15*time.Minute {
		t.Errorf("MaxWaitFor is %s — long enough to hold a scheduler slot for most of an hour", MaxWaitFor)
	}
	if waitSegment >= 3*time.Minute {
		t.Errorf("waitSegment %s meets or exceeds the transport timeout that kills the helper", waitSegment)
	}
}

var _ = context.Background
