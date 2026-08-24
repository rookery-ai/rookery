package web

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// drain reads everything currently available, starting from `next`.
func drain(rs *agentRunState, next int) ([]string, int, bool) {
	batch, advanced, done, _ := rs.readFrom(next)
	return batch, advanced, done
}

// The reported bug: leaving the agent page during a run and coming back showed
// an empty activity card. Progress was a consume-once channel, so everything
// already delivered was gone. A reader arriving late must see the whole run.
func TestRunProgressReplaysFromTheStart(t *testing.T) {
	rs := newAgentRunState()
	rs.appendLine("one")
	rs.appendLine("two")
	rs.appendLine("three")

	batch, next, done := drain(rs, 0)
	if done {
		t.Error("run is not finished yet")
	}
	if want := []string{"one", "two", "three"}; !equalStrings(batch, want) {
		t.Errorf("replay = %v, want %v", batch, want)
	}
	if next != 3 {
		t.Errorf("next = %d, want 3", next)
	}

	// Following on from there yields only what is new.
	rs.appendLine("four")
	batch, next, _ = drain(rs, next)
	if want := []string{"four"}; !equalStrings(batch, want) {
		t.Errorf("follow-up = %v, want %v", batch, want)
	}
	if next != 4 {
		t.Errorf("next = %d, want 4", next)
	}
}

// Two tabs on one run used to steal each other's lines: a channel delivers each
// message to exactly one reader. Both must now see everything.
func TestRunProgressFansOutToEveryReader(t *testing.T) {
	rs := newAgentRunState()
	rs.appendLine("a")
	rs.appendLine("b")

	first, _, _ := drain(rs, 0)
	second, _, _ := drain(rs, 0)

	want := []string{"a", "b"}
	if !equalStrings(first, want) {
		t.Errorf("first reader = %v, want %v", first, want)
	}
	if !equalStrings(second, want) {
		t.Errorf("second reader = %v, want %v", second, want)
	}
}

// A viewer attaching after the run finished still gets the transcript, then the
// terminal signal — the 90s eviction window exists precisely for this.
func TestRunProgressReplaysAfterFinish(t *testing.T) {
	rs := newAgentRunState()
	rs.appendLine("worked")
	rs.finish(nil)

	batch, _, done := drain(rs, 0)
	if !done {
		t.Error("done must be true after finish")
	}
	if want := []string{"worked"}; !equalStrings(batch, want) {
		t.Errorf("batch = %v, want %v", batch, want)
	}
}

// The failure line is appended BEFORE finish, so a reader that stops at `done`
// cannot miss the one line explaining what went wrong.
func TestRunProgressDeliversTheFailureLineBeforeDone(t *testing.T) {
	rs := newAgentRunState()
	rs.appendLine("⚠️ boom")
	rs.finish(errors.New("boom"))

	batch, _, done := drain(rs, 0)
	if !done {
		t.Fatal("expected done")
	}
	if len(batch) != 1 || batch[0] != "⚠️ boom" {
		t.Errorf("batch = %v, want the failure line", batch)
	}
}

// Over the cap the OLDEST lines go, so a live view keeps showing the newest
// work rather than freezing on the first 2000 lines.
func TestRunProgressCapsRetentionAndKeepsTheTail(t *testing.T) {
	rs := newAgentRunState()
	total := maxRetainedLines + 50
	for i := 0; i < total; i++ {
		rs.appendLine(fmt.Sprintf("line-%d", i))
	}

	batch, next, _ := drain(rs, 0)
	if len(batch) != maxRetainedLines {
		t.Errorf("retained %d lines, want %d", len(batch), maxRetainedLines)
	}
	if batch[0] != "line-50" {
		t.Errorf("oldest retained = %q, want line-50", batch[0])
	}
	if last := batch[len(batch)-1]; last != fmt.Sprintf("line-%d", total-1) {
		t.Errorf("newest retained = %q, want line-%d", last, total-1)
	}
	// The absolute index accounts for what was dropped, so following on from
	// here does not re-deliver the whole window.
	if next != total {
		t.Errorf("next = %d, want %d", next, total)
	}
	if again, _, _ := drain(rs, next); len(again) != 0 {
		t.Errorf("expected nothing new, got %v", again)
	}
}

// A reader whose position was truncated away resumes from the oldest retained
// line rather than erroring or blocking — it has already missed those lines.
func TestRunProgressFastForwardsATruncatedReader(t *testing.T) {
	rs := newAgentRunState()
	for i := 0; i < maxRetainedLines+10; i++ {
		rs.appendLine(fmt.Sprintf("line-%d", i))
	}
	batch, next, _ := drain(rs, 0)
	if len(batch) == 0 {
		t.Fatal("expected a batch")
	}
	if next != maxRetainedLines+10 {
		t.Errorf("next = %d, want %d", next, maxRetainedLines+10)
	}
	if batch[0] != "line-10" {
		t.Errorf("resumed at %q, want line-10", batch[0])
	}
}

// finish must release a reader parked on the wait channel, or the SSE handler
// hangs until the client gives up instead of reporting completion.
func TestRunProgressFinishWakesAWaitingReader(t *testing.T) {
	rs := newAgentRunState()
	_, _, _, wait := rs.readFrom(0)

	go func() {
		time.Sleep(10 * time.Millisecond)
		rs.finish(nil)
	}()

	select {
	case <-wait:
	case <-time.After(2 * time.Second):
		t.Fatal("finish did not wake the waiting reader")
	}
}

// An append must wake a parked reader too — otherwise live progress only
// appears when something else happens to broadcast.
func TestRunProgressAppendWakesAWaitingReader(t *testing.T) {
	rs := newAgentRunState()
	_, _, _, wait := rs.readFrom(0)

	go func() {
		time.Sleep(10 * time.Millisecond)
		rs.appendLine("hello")
	}()

	select {
	case <-wait:
	case <-time.After(2 * time.Second):
		t.Fatal("append did not wake the waiting reader")
	}
}

// The elapsed clock is served by the server so a page returned to mid-run shows
// a continuous time. It must reflect the run's own start, not the attach.
func TestRunProgressSinceMeasuresFromTheRunStart(t *testing.T) {
	rs := newAgentRunState()
	rs.startedAt = time.Now().Add(-90 * time.Second)
	if got := rs.since(); got < 89*time.Second {
		t.Errorf("since() = %v, want at least ~90s", got)
	}
}

// The producer appends from the run goroutine while SSE handlers read; this is
// the shape `go test -race` is there to check.
func TestRunProgressIsSafeUnderConcurrentReaders(t *testing.T) {
	rs := newAgentRunState()
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			rs.appendLine(fmt.Sprintf("line-%d", i))
		}
		rs.finish(nil)
	}()

	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			next := 0
			for {
				_, advanced, done, wait := rs.readFrom(next)
				next = advanced
				if done {
					return
				}
				<-wait
			}
		}()
	}
	wg.Wait()

	if !rs.isDone() {
		t.Error("run should be done")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
